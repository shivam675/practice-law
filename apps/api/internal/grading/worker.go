package grading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/slmlabs/megamoot/apps/api/internal/spec"
	"github.com/slmlabs/megamoot/apps/api/internal/workflow"
)

// Worker runs the grading a teacher requested with Autograde.
//
// A poll, not a hook. Grading is several model calls and takes minutes;
// running it inside the request would hold the teacher's connection open for
// the duration and lose the work if the process restarted. The worker never
// starts grading by itself and never retries a failed run: a pending row is
// the only claim, and only a teacher writes one.
type Worker struct {
	store    *Store
	engine   *workflow.Engine
	log      *slog.Logger
	Interval time.Duration
}

func NewWorker(store *Store, engine *workflow.Engine, log *slog.Logger) *Worker {
	return &Worker{store: store, engine: engine, log: log, Interval: 5 * time.Second}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	w.log.Info("grading worker started", "interval", w.Interval)
	for {
		select {
		case <-ctx.Done():
			w.log.Info("grading worker stopped")
			return
		case <-ticker.C:
			w.once(ctx)
		}
	}
}

type pendingRun struct {
	evaluationID uuid.UUID
	stageRowID   uuid.UUID
	orgID        uuid.UUID
	assignmentID uuid.UUID
	stageID      string
	config       []byte
}

func (w *Worker) once(ctx context.Context) {
	// ponytail: one grading worker across API processes. Replace this lock with
	// per-evaluation claims when parallel grading is required.
	conn, err := w.store.pool.Acquire(ctx)
	if err != nil {
		w.log.Error("acquire grading lock connection", "error", err)
		return
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(672041)`).Scan(&locked); err != nil || !locked {
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT pg_advisory_unlock(672041)`); err != nil {
			_ = conn.Conn().Close(cleanup)
		}
	}()
	// The lock proves no worker is still using a running row. A process crash
	// releases the lock; the interrupted run is closed, not resumed.
	if _, err := conn.Exec(ctx, `UPDATE evaluations SET status = 'failed', failure_reason = 'Grading was interrupted. Press Autograde to start again.', completed_at = now() WHERE status = 'running' AND evaluator_kind = 'ai'`); err != nil {
		w.log.Error("recover grading", "error", err)
		return
	}
	// A live round owns the GPU. Requests wait for it to end.
	if live, err := w.store.liveSessionRunning(ctx); err != nil || live {
		return
	}
	runs, err := w.claimable(ctx)
	if err != nil {
		w.log.Error("grading worker: find requested evaluations failed", "error", err)
		return
	}
	for _, run := range runs {
		if err := w.grade(ctx, run); err != nil {
			w.log.Warn("grading worker: stage not graded",
				"assignment_id", run.assignmentID, "stage_id", run.stageID, "error", err)
			// Close the request whatever failed, so nothing claims it again.
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if _, err := w.store.pool.Exec(cleanup, `UPDATE evaluations SET status = 'failed', failure_reason = $2, completed_at = now() WHERE id = $1 AND status IN ('pending','running')`, run.evaluationID, reason(err)); err != nil {
				w.log.Error("close failed grading request", "error", err)
			}
			cancel()
		}
	}
}

// claimable lists pending requests whose stage can still be graded. A request
// whose stage moved on is closed by grade, not left to be claimed forever.
func (w *Worker) claimable(ctx context.Context) ([]pendingRun, error) {
	rows, err := w.store.pool.Query(ctx, `
		SELECT e.id, s.id, s.organization_id, s.assignment_id, s.stage_id,
		       coalesce(spec_stage.value -> 'config', 'null'::jsonb)
		FROM evaluations e
		JOIN assignment_stages s ON s.assignment_id = e.assignment_id
		 AND s.organization_id = e.organization_id AND s.stage_id = e.stage_id
		JOIN assignments a        ON a.id = s.assignment_id AND a.organization_id = s.organization_id
		JOIN assessments ass      ON ass.id = a.assessment_id AND ass.organization_id = a.organization_id
		JOIN assessment_template_versions tv ON tv.id = ass.template_version_id
		LEFT JOIN LATERAL (
			SELECT value FROM jsonb_array_elements(tv.stages) AS value
			WHERE value ->> 'id' = s.stage_id
			LIMIT 1
		) AS spec_stage ON true
		WHERE e.status = 'pending' AND e.evaluator_kind = 'ai'
		ORDER BY e.created_at
		LIMIT 3`)
	if err != nil {
		return nil, fmt.Errorf("query requested evaluations: %w", err)
	}
	defer rows.Close()

	var out []pendingRun
	for rows.Next() {
		var run pendingRun
		if err := rows.Scan(&run.evaluationID, &run.stageRowID, &run.orgID, &run.assignmentID,
			&run.stageID, &run.config); err != nil {
			return nil, fmt.Errorf("scan requested evaluation: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

var errStageClosed = errors.New("the stage can no longer be graded")

func (w *Worker) grade(ctx context.Context, run pendingRun) error {
	// Waiting out a live round counts against this budget.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	var cfg spec.AutomatedEvaluationConfig
	if len(run.config) > 0 {
		if err := json.Unmarshal(run.config, &cfg); err != nil {
			return fmt.Errorf("decode evaluation config: %w", err)
		}
	}
	if _, err := gradable(ctx, w.store.pool, run.orgID, run.assignmentID); errors.Is(err, pgx.ErrNoRows) {
		return errStageClosed
	} else if err != nil {
		return err
	}

	started := time.Now()
	res, err := w.store.GradeSubmission(ctx, Target{
		OrganizationID: run.orgID,
		AssignmentID:   run.assignmentID,
		StageID:        run.stageID,
		RubricScope:    cfg.RubricScope,
		Sources:        cfg.Sources,
		EvaluationID:   run.evaluationID,
	})
	if err != nil {
		return err
	}
	w.log.Info("graded submission",
		"assignment_id", run.assignmentID, "stage_id", run.stageID,
		"scored", res.Scored, "total", res.WeightedTotal, "max", res.MaxTotal,
		"caveats", len(res.Caveats), "took", time.Since(started))

	// The engine decides the transition, as it does for every other state
	// change. The grader supplies a result; it does not advance anything.
	err = w.engine.Apply(ctx, workflow.Request{
		Subject:        workflow.SubjectStage,
		SubjectID:      run.stageRowID,
		OrganizationID: run.orgID,
		To:             workflow.StageCompleted,
		Cause:          "evaluation_completed",
		Actor:          workflow.SystemActor(),
	})
	if err != nil && !errors.Is(err, workflow.ErrNoop) {
		return fmt.Errorf("complete evaluation stage: %w", err)
	}
	return nil
}
