package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/slmlabs/megamoot/apps/api/internal/aiclient"
	"github.com/slmlabs/megamoot/apps/api/internal/aiprofiles"
	"github.com/slmlabs/megamoot/apps/api/internal/assessments"
	"github.com/slmlabs/megamoot/apps/api/internal/audit"
	"github.com/slmlabs/megamoot/apps/api/internal/auth"
	"github.com/slmlabs/megamoot/apps/api/internal/blob"
	"github.com/slmlabs/megamoot/apps/api/internal/config"
	"github.com/slmlabs/megamoot/apps/api/internal/grading"
	"github.com/slmlabs/megamoot/apps/api/internal/harness"
	"github.com/slmlabs/megamoot/apps/api/internal/llm"
	"github.com/slmlabs/megamoot/apps/api/internal/platformcfg"
	"github.com/slmlabs/megamoot/apps/api/internal/retrieval"
	"github.com/slmlabs/megamoot/apps/api/internal/rubrics"
	"github.com/slmlabs/megamoot/apps/api/internal/secrets"
	"github.com/slmlabs/megamoot/apps/api/internal/sessions"
	"github.com/slmlabs/megamoot/apps/api/internal/submissions"
	"github.com/slmlabs/megamoot/apps/api/internal/teams"
	"github.com/slmlabs/megamoot/apps/api/internal/templates"
	"github.com/slmlabs/megamoot/apps/api/internal/workflow"
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
	ledger      *llm.Ledger
	platform    *platformcfg.Store
	harness     *harness.Harness
	retrieval   *retrieval.Store
	indexer     *retrieval.Indexer
	grading     *grading.Store
	grader      *grading.Worker
	aiProfiles  *aiprofiles.Store
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

	// Refused at start-up rather than at the first credential write: a box
	// that cannot seal is a settings page that silently loses API keys.
	box, err := secrets.NewBox(cfg.ConfigEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("CONFIG_ENCRYPTION_KEY: %w", err)
	}
	a.ledger = llm.NewLedger(pool, log)
	a.platform = platformcfg.NewStore(pool, box)
	a.aiProfiles = aiprofiles.NewStore(pool)
	a.harness = harness.New(a.platform, a.ledger, log)
	a.retrieval = retrieval.NewStore(pool, a.harness, log)
	a.indexer = retrieval.NewIndexer(a.retrieval, log)

	a.rubrics = rubrics.NewStore(pool)
	a.templates = templates.NewStore(pool, a.rubrics)
	a.teams = teams.NewStore(pool)
	a.assessments = assessments.NewStore(pool, a.templates, a.teams, a.engine)
	a.submissions = submissions.NewStore(pool, a.blobs, a.ai, a.templates, a.engine)

	a.grading = grading.NewStore(pool, a.harness, a.retrieval, a.rubrics, log)
	a.grader = grading.NewWorker(a.grading, a.engine, log)

	a.engine.SetHook(a.onTransition)
	return a, nil
}

// onTransition is how one state machine drives another. A stage becoming
// active starts its assignment; a stage settling advances to whatever comes
// next. All of it is recorded state, none of it is a model's suggestion.
func (a *app) onTransition(ctx context.Context, req workflow.Request, from string) error {
	if req.Subject == workflow.SubjectSession && req.To == workflow.SessionEnded {
		var stageID uuid.UUID
		err := a.pool.QueryRow(ctx, `SELECT st.id FROM sessions s JOIN assignment_stages st ON st.assignment_id=s.assignment_id AND st.stage_id=s.stage_id AND st.organization_id=s.organization_id WHERE s.id=$1 AND s.organization_id=$2`, req.SubjectID, req.OrganizationID).Scan(&stageID)
		if err != nil {
			return err
		}
		err = a.engine.Apply(ctx, workflow.Request{Subject: workflow.SubjectStage, SubjectID: stageID, OrganizationID: req.OrganizationID, To: workflow.StageCompleted, Cause: "session_ended", Actor: workflow.SystemActor()})
		if errors.Is(err, workflow.ErrNoop) {
			return nil
		}
		return err
	}
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

// startBackground launches the durable timer worker, the document indexer and
// the grading worker. It returns once the context is cancelled.
func (a *app) startBackground(ctx context.Context) {
	if err := a.scheduler.ReleaseStale(ctx); err != nil && !errors.Is(err, context.Canceled) {
		a.log.Error("release stale scheduled transitions", "error", err)
	}
	go a.scheduler.Run(ctx)
	go a.indexer.Run(ctx)
	go a.grader.Run(ctx)
	go sessions.RunQuestionBank(ctx, a.pool, a.harness, a.log)
}
