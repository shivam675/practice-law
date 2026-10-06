// Command api is the MegaMoot control plane: authentication, authorisation,
// tenancy, the assessment workflow engine and session orchestration.
//
// It never touches live audio. See docs/architecture.md.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/config"
	"github.com/intelimek/megamoot/apps/api/internal/db"
	"github.com/intelimek/megamoot/apps/api/internal/orgs"
	"github.com/intelimek/megamoot/apps/api/internal/seeddata"
	"github.com/intelimek/megamoot/apps/api/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)
	log.Info("starting", "env", cfg.Env, "addr", cfg.HTTPAddr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Info("database connected")

	if err := db.Migrate(ctx, pool, migrations.FS, log); err != nil {
		return err
	}

	orgID, err := orgs.Bootstrap(ctx, pool, orgs.BootstrapInput{
		OrgName:       cfg.SeedOrgName,
		AdminEmail:    cfg.SeedAdminEmail,
		AdminPassword: cfg.SeedAdminPassword,

		SuperAdminEmail:    cfg.SeedSuperAdminEmail,
		SuperAdminPassword: cfg.SeedSuperAdminPassword,
	}, log)
	if err != nil {
		return err
	}
	if orgID != uuid.Nil {
		if err := seeddata.Seed(ctx, pool, orgID, log); err != nil {
			return err
		}
	}

	application, err := newApp(cfg, log, pool)
	if err != nil {
		return err
	}
	application.startBackground(ctx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newRouter(application),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Long enough for a large report payload, short enough that a stalled
		// client cannot hold a connection open indefinitely. Streaming
		// endpoints live on the media plane, not here.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("stopped cleanly")
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// healthHandlers keeps readiness honest: /healthz means the process is up,
// /readyz means it can actually serve, which requires the database.
func healthHandlers(pool *pgxpool.Pool) (live, ready http.HandlerFunc) {
	live = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
	ready = func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"degraded","reason":"database"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}
	return live, ready
}
