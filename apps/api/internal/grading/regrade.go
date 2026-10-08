package grading

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/rubrics"
	"github.com/intelimek/megamoot/apps/api/internal/spec"
	"github.com/jackc/pgx/v5"
)

// Regrade replaces one criterion, committing a complete new evaluation only
// after the model succeeds. The published snapshot is never changed.
func (s *Store) Regrade(ctx context.Context, org, assignment, scoreID, actor uuid.UUID, reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 4000 {
		return httpx.ErrBadRequest("A reason of up to 4000 characters is required.")
	}
	var prior, criterion uuid.UUID
	var stage string
	var raw []byte
	var overriddenAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT e.id,e.stage_id,cs.criterion_id,v.value->'config',coalesce(cs.overridden_at,'epoch'::timestamptz)
 FROM criterion_scores cs JOIN evaluations e ON e.id=cs.evaluation_id AND e.organization_id=cs.organization_id
 JOIN assignments a ON a.id=e.assignment_id AND a.organization_id=e.organization_id
 JOIN assessments ass ON ass.id=a.assessment_id AND ass.organization_id=a.organization_id JOIN assessment_template_versions tv ON tv.id=ass.template_version_id
 CROSS JOIN LATERAL jsonb_array_elements(tv.stages) v(value)
 WHERE cs.id=$1 AND cs.organization_id=$2 AND a.id=$3 AND a.status<>'finalized' AND e.status='completed' AND v.value->>'id'=e.stage_id`, scoreID, org, assignment).Scan(&prior, &stage, &criterion, &raw, &overriddenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrConflict("Only a current, unpublished score can be regraded.")
	}
	if err != nil {
		return err
	}
	var cfg spec.AutomatedEvaluationConfig
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	target := Target{OrganizationID: org, AssignmentID: assignment, StageID: stage, Sources: cfg.Sources}
	sub, err := s.loadSubmission(ctx, target)
	if err != nil {
		return err
	}
	rubric, err := s.loadRubric(ctx, org, sub.RubricID)
	if err != nil {
		return err
	}
	var selected rubrics.Criterion
	for _, c := range rubric.Criteria {
		if c.ID == criterion {
			selected = c
			break
		}
	}
	if selected.ID == uuid.Nil {
		return httpx.ErrNotFound()
	}
	scored, err := s.gradeOne(ctx, target, sub, selected, sub.Text)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var state string
	if err = tx.QueryRow(ctx, `SELECT status FROM assignments WHERE id=$1 AND organization_id=$2 FOR UPDATE`, assignment, org).Scan(&state); err != nil {
		return err
	}
	if state == "finalized" {
		return httpx.ErrConflict("The report was published while grading. Its results were preserved.")
	}
	tag, err := tx.Exec(ctx, `UPDATE evaluations SET status='superseded' WHERE id=$1 AND organization_id=$2 AND status='completed' AND EXISTS(SELECT 1 FROM criterion_scores WHERE id=$3 AND coalesce(overridden_at,'epoch'::timestamptz)=$4)`, prior, org, scoreID, overriddenAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return httpx.ErrConflict("The score changed while grading. Refresh and try again.")
	}
	var next uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO evaluations(organization_id,assignment_id,stage_id,rubric_id,evaluator_kind,status,prompt_version,supersedes_id,caveats,started_at)
 SELECT organization_id,assignment_id,stage_id,rubric_id,'ai','running',$3,id,caveats,now() FROM evaluations WHERE id=$1 AND organization_id=$2 RETURNING id`, prior, org, PromptVersion).Scan(&next)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO criterion_scores(organization_id,evaluation_id,criterion_id,score,max_score,reasoning,overridden_score,overridden_by,override_reason,overridden_at)
 SELECT organization_id,$3,criterion_id,score,max_score,reasoning,overridden_score,overridden_by,override_reason,overridden_at FROM criterion_scores WHERE evaluation_id=$1 AND organization_id=$2 AND id<>$4`, prior, org, next, scoreID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO evidence_spans(organization_id,criterion_score_id,source_kind,source_ref,locator,quote,verified,match_score)
 SELECT es.organization_id,n.id,es.source_kind,es.source_ref,es.locator,es.quote,es.verified,es.match_score FROM criterion_scores old JOIN evidence_spans es ON es.criterion_score_id=old.id AND es.organization_id=old.organization_id JOIN criterion_scores n ON n.criterion_id=old.criterion_id AND n.evaluation_id=$3 AND n.organization_id=old.organization_id WHERE old.evaluation_id=$1 AND old.organization_id=$2`, prior, org, next)
	if err != nil {
		return err
	}
	if err = s.persistCriterionTx(ctx, tx, target, next, sub, selected, scored, sub.Text); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT coalesce(cs.overridden_score,cs.score),cs.max_score,rc.weight FROM criterion_scores cs JOIN rubric_criteria rc ON rc.id=cs.criterion_id WHERE cs.evaluation_id=$1 AND cs.organization_id=$2`, next, org)
	if err != nil {
		return err
	}
	var total, maximum float64
	for rows.Next() {
		var value, maxScore, weight float64
		if err = rows.Scan(&value, &maxScore, &weight); err != nil {
			rows.Close()
			return err
		}
		total += weight * value / maxScore
		maximum += weight
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE evaluations SET status='completed',weighted_total=$3,max_total=$4,completed_at=now() WHERE id=$1 AND organization_id=$2`, next, org, total, maximum)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(organization_id,actor_user_id,actor_kind,action,target_kind,target_id,reason,before_state,after_state) VALUES($1,$2,'user','assessment.regrade','evaluation',$3,$4,jsonb_build_object('evaluation_id',$5::text),jsonb_build_object('evaluation_id',$3::text,'criterion_id',$6::text))`, org, actor, next, strings.TrimSpace(reason), prior, criterion)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
