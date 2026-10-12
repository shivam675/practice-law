package grading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/slmlabs/megamoot/apps/api/internal/httpx"
	"github.com/slmlabs/megamoot/apps/api/internal/spec"
)

// Grading never starts by itself. A teacher presses Autograde, which writes a
// pending evaluation; the worker picks it up between live rounds. A run that
// fails stays failed until a teacher asks again, so a model that cannot do the
// job costs one attempt, not one every few minutes for a day.

// liveSQL is true while any oral round holds the GPU.
const liveSQL = `SELECT EXISTS(SELECT 1 FROM sessions WHERE status IN ('running','paused','degraded'))`

func (s *Store) liveSessionRunning(ctx context.Context) (bool, error) {
	var live bool
	err := s.pool.QueryRow(ctx, liveSQL).Scan(&live)
	return live, err
}

// waitWhileLive blocks while an oral round runs. The worker's deadline still
// applies, so a round that never ends fails the run rather than hanging it.
func (s *Store) waitWhileLive(ctx context.Context) error {
	for {
		live, err := s.liveSessionRunning(ctx)
		if err != nil || !live {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

type gradableStage struct {
	stageID string
	config  spec.AutomatedEvaluationConfig
	rubric  uuid.UUID
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// gradable finds the evaluation stage a teacher may grade now: active, or
// completed and not yet published, with every earlier stage settled.
func gradable(ctx context.Context, q querier, org, assignment uuid.UUID) (gradableStage, error) {
	var st gradableStage
	var raw []byte
	err := q.QueryRow(ctx, `
		SELECT s.stage_id, coalesce(v.value->'config','{}'::jsonb), tv.rubric_id
		FROM assignment_stages s
		JOIN assignments a ON a.id = s.assignment_id AND a.organization_id = s.organization_id
		JOIN assessments ass ON ass.id = a.assessment_id AND ass.organization_id = a.organization_id
		JOIN assessment_template_versions tv ON tv.id = ass.template_version_id
		LEFT JOIN LATERAL (SELECT value FROM jsonb_array_elements(tv.stages) value
		                   WHERE value->>'id' = s.stage_id LIMIT 1) v ON true
		WHERE s.assignment_id = $1 AND s.organization_id = $2
		  AND s.stage_kind = 'automated_evaluation' AND s.status IN ('active','completed')
		  AND a.status <> 'finalized'
		  AND NOT EXISTS (SELECT 1 FROM assignment_stages prior
		      WHERE prior.assignment_id = s.assignment_id AND prior.organization_id = s.organization_id
		        AND prior.sort_order < s.sort_order
		        AND prior.status NOT IN ('completed','expired','skipped','failed'))
		ORDER BY (s.status = 'active') DESC, s.sort_order
		LIMIT 1`, assignment, org).Scan(&st.stageID, &raw, &st.rubric)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(raw, &st.config); err != nil {
		return st, fmt.Errorf("decode evaluation config: %w", err)
	}
	return st, nil
}

// Request records a teacher's Autograde press as a pending evaluation.
func (s *Store) Request(ctx context.Context, org, assignment, actor uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// One request at a time per assignment, however fast the button is pressed.
	if _, err = tx.Exec(ctx, `SELECT 1 FROM assignments WHERE id = $1 AND organization_id = $2 FOR UPDATE`, assignment, org); err != nil {
		return err
	}
	st, err := gradable(ctx, tx, org, assignment)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrConflict("Grading opens once the stages it covers are finished, and closes when results are published.")
	}
	if err != nil {
		return err
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM evaluations WHERE assignment_id = $1 AND organization_id = $2 AND evaluator_kind = 'ai' AND status IN ('pending','running'))`, assignment, org).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return httpx.ErrConflict("Grading is already in progress for this assignment.")
	}
	if _, err = s.loadSubmission(ctx, Target{OrganizationID: org, AssignmentID: assignment, StageID: st.stageID, Sources: st.config.Sources}); err != nil {
		return httpx.ErrConflict("There is no parsed submission or saved transcript to grade yet.")
	}
	var id uuid.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO evaluations (organization_id, assignment_id, stage_id, rubric_id, evaluator_kind, status, prompt_version)
		VALUES ($1,$2,$3,$4,'ai','pending',$5) RETURNING id`,
		org, assignment, st.stageID, st.rubric, PromptVersion).Scan(&id); err != nil {
		return fmt.Errorf("request evaluation: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (organization_id, actor_user_id, actor_kind, action, target_kind, target_id, after_state)
		VALUES ($1,$2,'user','assessment.autograde','evaluation',$3,jsonb_build_object('stage_id',$4::text))`,
		org, actor, id, st.stageID); err != nil {
		return fmt.Errorf("audit autograde: %w", err)
	}
	return tx.Commit(ctx)
}

type CriterionProgress struct {
	Name      string   `json:"name"`
	MaxScore  float64  `json:"max_score"`
	Status    string   `json:"status"` // queued, grading, scored, not_scored
	Score     *float64 `json:"score"`
	Reasoning string   `json:"reasoning"`
}

type Progress struct {
	Status         string              `json:"status"` // none, pending, running, completed, failed
	StageID        string              `json:"stage_id"`
	RequestedAt    *time.Time          `json:"requested_at"`
	StartedAt      *time.Time          `json:"started_at"`
	CompletedAt    *time.Time          `json:"completed_at"`
	FailureReason  string              `json:"failure_reason"`
	WaitingForLive bool                `json:"waiting_for_live"`
	CanStart       bool                `json:"can_start"`
	Criteria       []CriterionProgress `json:"criteria"`
}

// Progress reports the latest AI run criterion by criterion. Criteria are
// graded in rubric order, so the first scored-plus-caveated count of them are
// done, the next one is in hand, and the rest are queued.
func (s *Store) Progress(ctx context.Context, org, assignment uuid.UUID) (Progress, error) {
	out := Progress{Status: "none", Criteria: []CriterionProgress{}}
	var id, rubricID uuid.UUID
	var caveats []string
	var reason *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, stage_id, status, rubric_id, caveats, failure_reason, created_at, started_at, completed_at
		FROM evaluations WHERE assignment_id = $1 AND organization_id = $2 AND evaluator_kind = 'ai'
		  AND status <> 'superseded'
		ORDER BY created_at DESC LIMIT 1`, assignment, org).
		Scan(&id, &out.StageID, &out.Status, &rubricID, &caveats, &reason, &out.RequestedAt, &out.StartedAt, &out.CompletedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if reason != nil {
		out.FailureReason = *reason
	}
	busy := out.Status == "pending" || out.Status == "running"
	if !busy {
		_, gerr := gradable(ctx, s.pool, org, assignment)
		if gerr != nil && !errors.Is(gerr, pgx.ErrNoRows) {
			return out, gerr
		}
		out.CanStart = gerr == nil
	} else if out.WaitingForLive, err = s.liveSessionRunning(ctx); err != nil {
		return out, err
	}
	if id == uuid.Nil {
		return out, nil
	}
	rubric, err := s.loadRubric(ctx, org, rubricID)
	if err != nil {
		return out, err
	}
	var cfg spec.AutomatedEvaluationConfig
	var raw []byte
	if err = s.pool.QueryRow(ctx, `
		SELECT coalesce(v.value->'config','{}'::jsonb) FROM assignments a
		JOIN assessments ass ON ass.id = a.assessment_id AND ass.organization_id = a.organization_id
		JOIN assessment_template_versions tv ON tv.id = ass.template_version_id
		LEFT JOIN LATERAL (SELECT value FROM jsonb_array_elements(tv.stages) value WHERE value->>'id' = $3 LIMIT 1) v ON true
		WHERE a.id = $1 AND a.organization_id = $2`, assignment, org, out.StageID).Scan(&raw); err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return out, fmt.Errorf("decode evaluation config: %w", err)
	}
	type scored struct {
		score     float64
		reasoning string
	}
	scores := map[uuid.UUID]scored{}
	rows, err := s.pool.Query(ctx, `SELECT criterion_id, score, reasoning FROM criterion_scores WHERE evaluation_id = $1 AND organization_id = $2`, id, org)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c uuid.UUID
		var v scored
		if err = rows.Scan(&c, &v.score, &v.reasoning); err != nil {
			rows.Close()
			return out, err
		}
		scores[c] = v
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	done := len(scores) + len(caveats)
	for i, c := range inScope(rubric.Criteria, cfg.RubricScope) {
		p := CriterionProgress{Name: c.Name, MaxScore: c.MaxScore, Status: "queued"}
		if v, ok := scores[c.ID]; ok {
			p.Status, p.Score, p.Reasoning = "scored", &v.score, strings.TrimSpace(v.reasoning)
		} else if i < done || !busy {
			p.Status = "not_scored"
		} else if i == done && out.Status == "running" {
			p.Status = "grading"
		}
		out.Criteria = append(out.Criteria, p)
	}
	return out, nil
}
