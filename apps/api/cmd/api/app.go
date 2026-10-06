package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/aiclient"
	"github.com/intelimek/megamoot/apps/api/internal/assessments"
	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/blob"
	"github.com/intelimek/megamoot/apps/api/internal/config"
	"github.com/intelimek/megamoot/apps/api/internal/rubrics"
	"github.com/intelimek/megamoot/apps/api/internal/submissions"
	"github.com/intelimek/megamoot/apps/api/internal/teams"
	"github.com/intelimek/megamoot/apps/api/internal/templates"
	"github.com/intelimek/megamoot/apps/api/internal/workflow"
)

// app holds the wired dependency graph. Everything is constructed once at
// start-up and nothing reaches for a global.
type app struct {
	cfg   config.Config
	log   *slog.Logger
	pool  *pgxpool.Pool
	audit *audit.Logger

	auth        *auth.Service
	engine      *workflow.Engine
	scheduler   *workflow.Scheduler
	blobs       blob.Store
	ai          *aiclient.Client
	rubrics     *rubrics.Store
	templates   *templates.Store
	teams       *teams.Store
	assessments *assessments.Store
	submissions *submissions.Store
}

func newApp(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) (*app, error) {
	auditLog := audit.New(pool, log)

	a := &app{
		cfg:   cfg,
		log:   log,
		pool:  pool,
		audit: auditLog,
	}

	a.auth = auth.NewService(
		auth.NewStore(pool),
		auth.NewTokenIssuer(cfg.JWTSigningKey, cfg.PublicURL,
			cfg.AccessTokenTTL, cfg.RefreshTokenTTL),
		auditLog,
	)

	a.engine = workflow.NewEngine(pool, auditLog, log)
	a.scheduler = workflow.NewScheduler(a.engine, log, uuid.NewString())

	blobs, err := blob.Open(cfg.BlobDriver, cfg.BlobFSRoot)
	if err != nil {
		return nil, err
	}
	a.blobs = blobs
	log.Info("blob store ready", "store", blobs.Describe())

	a.ai = aiclient.New(cfg.AIServiceURL, cfg.AIServiceToken)

	a.rubrics = rubrics.NewStore(pool)
	a.templates = templates.NewStore(pool, a.rubrics)
	a.teams = teams.NewStore(pool)
	a.assessments = assessments.NewStore(pool, a.templates, a.teams, a.engine)
	a.submissions = submissions.NewStore(pool, a.blobs, a.ai, a.templates, a.engine)

	a.engine.SetHook(a.onTransition)
	return a, nil
}

// onTransition is how one state machine drives another. A stage becoming
// active starts its assignment; a stage settling advances to whatever comes
// next. All of it is recorded state, none of it is a model's suggestion.
func (a *app) onTransition(ctx context.Context, req workflow.Request, from string) error {
	if req.Subject != workflow.SubjectStage {
		return nil
	}

	switch req.To {
	case workflow.StageActive:
		return a.assessments.StartAssignment(ctx, req.OrganizationID, req.SubjectID)

	case workflow.StageCompleted, workflow.StageExpired,
		workflow.StageSkipped, workflow.StageFailed:
		return a.assessments.Progress(ctx, req.OrganizationID, req.SubjectID)
	}
	return nil
}

// startBackground launches the durable timer worker. It returns once the
// context is cancelled.
func (a *app) startBackground(ctx context.Context) {
	if err := a.scheduler.ReleaseStale(ctx); err != nil && !errors.Is(err, context.Canceled) {
		a.log.Error("release stale scheduled transitions", "error", err)
	}
	go a.scheduler.Run(ctx)
}
