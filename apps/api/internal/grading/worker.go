package grading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/slmlabs/megamoot/apps/api/internal/spec"
	"github.com/slmlabs/megamoot/apps/api/internal/workflow"
)

// Worker grades the automated_evaluation stages that are waiting.
//
// A poll, not a hook. Grading a memorial is several model calls and takes
// seconds to minutes; running it inside the transition that activates the
// stage would hold a student's upload request open for the duration and lose
// the work if the process restarted. The question "which stages are active
// and have no completed evaluation" is one the database can already answer.
type Worker struct {
	store    *Store
	engine   *workflow.Engine
	log      *slog.Logger
	Interval time.Duration
}

func NewWorker(store *Store, engine *workflow.Engine, log *slog.Logger) *Worker {
	return &Worker{store: store, engine: engine, log: log, Interval: 20 * time.Second}
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

type pendingStage struct {
	stageRowID   uuid.UUID
	orgID        uuid.UUID
	assignmentID uuid.UUID
	stageID      string
	config       []byte
}

func (w *Worker) once(ctx context.Context) {
	// ponytail: one grading worker across API processes. Replace this lock with
	// per-stage claims when parallel grading is required.
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
	// releases the lock, so interrupted evaluations can be retried safely.
	if _, err := conn.Exec(ctx, `UPDATE evaluations SET status = 'failed', failure_reason = 'Evaluation interrupted. Retrying.', completed_at = now() WHERE status = 'running' AND evaluator_kind = 'ai'`); err != nil {
		w.log.Error("recover grading", "error", err)
		return
	}
	stages, err := w.claimable(ctx)
	if err != nil {
		w.log.Error("grading worker: find pending evaluations failed", "error", err)
		return
	}

	for _, st := range stages {
		if err := w.grade(ctx, st); err != nil {
			w.log.Warn("grading worker: stage not graded",
				"assignment_id", st.assignmentID, "stage_id", st.stageID, "error", err)
			// A bad submission must not block other assignments.
			continue
		}
	}
}

// claimable finds active automated_evaluation stages with no current AI
// evaluation. The absence of a completed evaluation is the claim: nothing is
// marked in flight, so a crash mid-grade simply means the next tick tries
// again, and openEvaluation supersedes rather than duplicating.
func (w *Worker) claimable(ctx context.Context) ([]pendingStage, error) {
	rows, err := w.store.pool.Query(ctx, `
		SELECT s.id, s.organization_id, s.assignment_id, s.stage_id,
		       coalesce(spec_stage.value -> 'config', 'null'::jsonb)
		FROM assignment_stages s
		JOIN assignments a        ON a.id = s.assignment_id
		JOIN assessments ass      ON ass.id = a.assessment_id
		JOIN assessment_template_versions tv ON tv.id = ass.template_version_id
		LEFT JOIN LATERAL (
			SELECT value FROM jsonb_array_elements(tv.stages) AS value
			WHERE value ->> 'id' = s.stage_id
			LIMIT 1
		) AS spec_stage ON true
		WHERE s.stage_kind = 'automated_evaluation'
		  AND s.status = 'active'
		  AND NOT EXISTS (SELECT 1 FROM assignment_stages prior
		      WHERE prior.assignment_id=s.assignment_id AND prior.organization_id=s.organization_id
		      AND prior.sort_order<s.sort_order AND prior.status NOT IN ('completed','expired','skipped','failed'))
		  -- Backoff. A stage that just failed fails the same way on the next
		  -- tick, and a 20s retry loop over a model call pins the GPU for as
		  -- long as the cause survives. Ten minutes is long enough to fix a
		  -- binding and short enough that nobody waits on it.
		  AND NOT EXISTS (SELECT 1 FROM evaluations e
		      WHERE e.organization_id = s.organization_id
		        AND e.assignment_id = s.assignment_id
		        AND e.stage_id = s.stage_id
		        AND e.evaluator_kind = 'ai'
		        AND e.status = 'failed'
		        AND e.completed_at > now() - interval '10 minutes')
		ORDER BY s.started_at
		LIMIT 3`)
	if err != nil {
		return nil, fmt.Errorf("query pending evaluations: %w", err)
	}
	defer rows.Close()

	var out []pendingStage
	for rows.Next() {
		var st pendingStage
		if err := rows.Scan(&st.stageRowID, &st.orgID, &st.assignmentID,
			&st.stageID, &st.config); err != nil {
			return nil, fmt.Errorf("scan pending evaluation: %w", err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (w *Worker) grade(ctx context.Context, st pendingStage) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	var cfg spec.AutomatedEvaluationConfig
	if len(st.config) > 0 {
		// A config that will not decode is a template problem, not a reason
		// to skip the grade: every criterion is then in scope.
		if err := json.Unmarshal(st.config, &cfg); err != nil {
			return fmt.Errorf("decode evaluation config: %w", err)
		}
	}

	started := time.Now()
	var completed bool
	if err := w.store.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM evaluations WHERE organization_id = $1 AND assignment_id = $2 AND stage_id = $3 AND evaluator_kind = 'ai' AND status = 'completed')`, st.orgID, st.assignmentID, st.stageID).Scan(&completed); err != nil {
		return err
	}
	if !completed {
		res, err := w.store.GradeSubmission(ctx, Target{
			OrganizationID: st.orgID,
			AssignmentID:   st.assignmentID,
			StageID:        st.stageID,
			RubricScope:    cfg.RubricScope,
			Sources:        cfg.Sources,
		})
		if err != nil {
			return err
		}

		w.log.Info("graded submission",
			"assignment_id", st.assignmentID, "stage_id", st.stageID,
			"scored", res.Scored, "total", res.WeightedTotal, "max", res.MaxTotal,
			"caveats", len(res.Caveats), "took", time.Since(started))
	}

	// The engine decides the transition, as it does for every other state
	// change. The grader supplies a result; it does not advance anything.
	err := w.engine.Apply(ctx, workflow.Request{
		Subject:        workflow.SubjectStage,
		SubjectID:      st.stageRowID,
		OrganizationID: st.orgID,
		To:             workflow.StageCompleted,
		Cause:          "evaluation_completed",
		Actor:          workflow.SystemActor(),
	})
	if err != nil && !errors.Is(err, workflow.ErrNoop) {
		return fmt.Errorf("complete evaluation stage: %w", err)
	}
	return nil
}
