package main

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/authz"
	"github.com/intelimek/megamoot/apps/api/internal/config"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/users"
)

func newRouter(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool,
	authSvc *auth.Service, auditLog *audit.Logger) http.Handler {

	r := chi.NewRouter()

	r.Use(httpx.RequestID)
	r.Use(httpx.WithLogger(log))
	r.Use(httpx.Recover)
	r.Use(httpx.AccessLog)
	r.Use(httpx.SecurityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-Id"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	live, ready := healthHandlers(pool)
	r.Get("/healthz", live)
	r.Get("/readyz", ready)

	authHandlers := auth.NewHandlers(authSvc, cfg.CookieDomain, cfg.CookieSecure)
	userHandlers := users.NewHandlers(pool, auditLog)

	// Credential endpoints get their own bucket. Everything else shares a
	// looser one; per-tenant quotas arrive with usage accounting.
	loginLimiter := httpx.NewRateLimiter(20, 5)
	apiLimiter := httpx.NewRateLimiter(600, 120)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(apiLimiter.LimitByIP)

		r.Route("/auth", func(r chi.Router) {
			r.With(loginLimiter.LimitByIP).Group(authHandlers.Routes)
		})

		r.Group(func(r chi.Router) {
			r.Use(authSvc.Authenticate)

			r.Get("/me", authHandlers.Me)

			r.Route("/users", func(r chi.Router) {
				r.With(authz.Require("user.view")).Get("/", userHandlers.List)
				r.With(authz.Require("user.create")).Post("/", userHandlers.Create)
				r.With(authz.Require("user.assign_role")).
					Put("/{userID}/roles", userHandlers.SetRoles)
			})
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, httpx.ErrNotFound())
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, httpx.Err(http.StatusMethodNotAllowed,
			"method_not_allowed", "That method is not allowed here."))
	})

	return r
}
