package reports

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/grading"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/workflow"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"math"
	"net/http"
	"strings"
	"time"
)

type Handlers struct {
	pool   *pgxpool.Pool
	engine *workflow.Engine
	grader *grading.Store
}

func New(pool *pgxpool.Pool, engine *workflow.Engine, grader *grading.Store) *Handlers {
	return &Handlers{pool, engine, grader}
}

func (h *Handlers) Regrade(w http.ResponseWriter, r *http.Request) {
	id, org, err := h.access(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var in struct {
		ScoreID uuid.UUID `json:"score_id"`
		Reason  string    `json:"reason"`
	}
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	// Long records need several bounded model calls; extend only this response.
	if err = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Minute)); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 14*time.Minute)
	defer cancel()
	if err = h.grader.Regrade(ctx, org, id, in.ScoreID, auth.MustPrincipal(r.Context()).UserID, in.Reason); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, map[string]bool{"saved": true})
}

type Score struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Score     float64         `json:"score"`
	Max       float64         `json:"max_score"`
	Weight    float64         `json:"weight"`
	Reasoning string          `json:"reasoning"`
	Override  *float64        `json:"overridden_score"`
	Reason    *string         `json:"override_reason"`
	Evidence  json.RawMessage `json:"evidence"`
}
type Report struct {
	Status  string   `json:"status"`
	Scores  []Score  `json:"scores"`
	Total   float64  `json:"total"`
	Maximum float64  `json:"maximum"`
	Notes   string   `json:"notes"`
	Caveats []string `json:"caveats"`
}
type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func build(ctx context.Context, q querier, org, id uuid.UUID) (Report, error) {
	out := Report{Status: "awaiting_review", Scores: []Score{}, Caveats: []string{}}
	rows, err := q.Query(ctx, `SELECT cs.id,rc.name,cs.score,cs.max_score,rc.weight,cs.reasoning,cs.overridden_score,cs.override_reason,
 coalesce((SELECT jsonb_agg(jsonb_build_object('quote',es.quote,'locator',es.locator,'source_kind',es.source_kind)) FROM evidence_spans es WHERE es.criterion_score_id=cs.id AND es.organization_id=$1 AND es.verified),'[]'::jsonb),e.caveats
 FROM evaluations e JOIN criterion_scores cs ON cs.evaluation_id=e.id AND cs.organization_id=e.organization_id JOIN rubric_criteria rc ON rc.id=cs.criterion_id
 WHERE e.organization_id=$1 AND e.assignment_id=$2 AND e.status='completed' ORDER BY e.stage_id,rc.sort_order`, org, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var score Score
		var caveats []string
		if err = rows.Scan(&score.ID, &score.Name, &score.Score, &score.Max, &score.Weight, &score.Reasoning, &score.Override, &score.Reason, &score.Evidence, &caveats); err != nil {
			return out, err
		}
		out.Scores = append(out.Scores, score)
		for _, c := range caveats {
			if !seen[c] {
				out.Caveats = append(out.Caveats, c)
				seen[c] = true
			}
		}
	}
	out.Total, out.Maximum = totals(out.Scores)
	return out, rows.Err()
}
func totals(scores []Score) (float64, float64) {
	var total, max float64
	for _, s := range scores {
		if s.Max <= 0 {
			continue
		}
		value := s.Score
		if s.Override != nil {
			value = *s.Override
		}
		total += s.Weight * value / s.Max
		max += s.Weight
	}
	return total, max
}

func (h *Handlers) access(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	p := auth.MustPrincipal(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "assignmentID"))
	if err != nil {
		return uuid.Nil, uuid.Nil, httpx.ErrNotFound()
	}
	var allowed bool
	err = h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM assignments a WHERE a.id=$1 AND a.organization_id=$2 AND ($4 OR EXISTS(SELECT 1 FROM team_members m WHERE m.team_id=a.team_id AND m.user_id=$3)))`, id, p.OrganizationID, p.UserID, p.Can("report.view")).Scan(&allowed)
	if err != nil {
		return id, p.OrganizationID, err
	}
	if !allowed {
		return id, p.OrganizationID, httpx.ErrNotFound()
	}
	return id, p.OrganizationID, nil
}
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	id, org, err := h.access(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var content json.RawMessage
	err = h.pool.QueryRow(r.Context(), `SELECT content FROM reports WHERE assignment_id=$1 AND organization_id=$2 AND status='published' ORDER BY version DESC LIMIT 1`, id, org).Scan(&content)
	if err == nil {
		httpx.JSON(w, r, 200, content)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		httpx.Fail(w, r, err)
		return
	}
	if !auth.MustPrincipal(r.Context()).Can("report.view") {
		httpx.JSON(w, r, 200, map[string]any{"status": "unpublished", "scores": []Score{}, "caveats": []string{}})
		return
	}
	out, err := build(r.Context(), h.pool, org, id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, out)
}
func (h *Handlers) Override(w http.ResponseWriter, r *http.Request) {
	id, org, err := h.access(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	p := auth.MustPrincipal(r.Context())
	var in struct {
		ScoreID uuid.UUID `json:"score_id"`
		Score   float64   `json:"score"`
		Reason  string    `json:"reason"`
	}
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if math.IsNaN(in.Score) || math.IsInf(in.Score, 0) || in.Score < 0 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4000 {
		httpx.Fail(w, r, httpx.ErrBadRequest("Enter a valid score and a reason."))
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(r.Context()))
	var status string
	if err = tx.QueryRow(r.Context(), `SELECT status FROM assignments WHERE id=$1 AND organization_id=$2 FOR UPDATE`, id, org).Scan(&status); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if status == workflow.AssignmentFinalized {
		httpx.Fail(w, r, httpx.ErrConflict("Published results cannot be changed."))
		return
	}
	var before float64
	err = tx.QueryRow(r.Context(), `SELECT coalesce(cs.overridden_score,cs.score) FROM criterion_scores cs JOIN evaluations e ON e.id=cs.evaluation_id WHERE cs.id=$1 AND cs.organization_id=$2 AND e.assignment_id=$3 AND e.status='completed' AND $4<=cs.max_score FOR UPDATE OF cs`, in.ScoreID, org, id, in.Score).Scan(&before)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Fail(w, r, httpx.ErrBadRequest("The score is unavailable or exceeds its maximum."))
		return
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE criterion_scores SET overridden_score=$1,overridden_by=$2,override_reason=$3,overridden_at=now() WHERE id=$4 AND organization_id=$5`, in.Score, p.UserID, strings.TrimSpace(in.Reason), in.ScoreID, org)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO audit_logs(organization_id,actor_user_id,actor_kind,action,target_kind,target_id,reason,before_state,after_state) VALUES($1,$2,'user','assessment.override_grade','criterion_score',$3,$4,jsonb_build_object('score',$5::numeric),jsonb_build_object('score',$6::numeric))`, org, p.UserID, in.ScoreID, in.Reason, before, in.Score)
	}
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, 200, map[string]bool{"saved": true})
}
func (h *Handlers) Publish(w http.ResponseWriter, r *http.Request) {
	id, org, err := h.access(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	p := auth.MustPrincipal(r.Context())
	var in struct {
		Notes string `json:"notes"`
	}
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(in.Notes) > 12000 {
		httpx.Fail(w, r, httpx.ErrBadRequest("Teacher notes are too long."))
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(r.Context()))
	var status string
	if err = tx.QueryRow(r.Context(), `SELECT status FROM assignments WHERE id=$1 AND organization_id=$2 FOR UPDATE`, id, org).Scan(&status); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if status == workflow.AssignmentFinalized {
		httpx.Fail(w, r, httpx.ErrConflict("This report is already published."))
		return
	}
	var pending int
	err = tx.QueryRow(r.Context(), `SELECT count(*) FROM assignment_stages WHERE assignment_id=$1 AND organization_id=$2 AND
 ((stage_kind='automated_evaluation' AND status<>'completed') OR
 (stage_kind<>'human_review' AND status NOT IN ('completed','expired','skipped','failed')) OR
 (stage_kind='human_review' AND status='pending'))`, id, org).Scan(&pending)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if pending > 0 {
		httpx.Fail(w, r, httpx.ErrConflict("Complete the assessment and its evaluations before publishing."))
		return
	}
	report, err := build(r.Context(), tx, org, id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(report.Scores) == 0 {
		httpx.Fail(w, r, httpx.ErrConflict("There are no scores to publish."))
		return
	}
	report.Status = "published"
	report.Notes = strings.TrimSpace(in.Notes)
	content, err := json.Marshal(report)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	rows, err := tx.Query(r.Context(), `SELECT id FROM assignment_stages WHERE assignment_id=$1 AND organization_id=$2 AND stage_kind='human_review' AND status='active'`, id, org)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var stages []uuid.UUID
	for rows.Next() {
		var stage uuid.UUID
		if err = rows.Scan(&stage); err != nil {
			rows.Close()
			httpx.Fail(w, r, err)
			return
		}
		stages = append(stages, stage)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	for _, stage := range stages {
		_, err = h.engine.ApplyTx(r.Context(), tx, workflow.Request{Subject: workflow.SubjectStage, SubjectID: stage, OrganizationID: org, To: workflow.StageCompleted, Cause: "teacher_reviewed", Actor: workflow.UserActor(p.UserID)})
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}
	if status == workflow.AssignmentInProgress {
		_, err = h.engine.ApplyTx(r.Context(), tx, workflow.Request{Subject: workflow.SubjectAssignment, SubjectID: id, OrganizationID: org, To: workflow.AssignmentAwaitingReview, Cause: "teacher_reviewed", Actor: workflow.UserActor(p.UserID)})
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
	}
	req := workflow.Request{Subject: workflow.SubjectAssignment, SubjectID: id, OrganizationID: org, To: workflow.AssignmentFinalized, Cause: "report_published", Actor: workflow.UserActor(p.UserID)}
	from, err := h.engine.ApplyTx(r.Context(), tx, req)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO reports(organization_id,assignment_id,status,content,overall_score,max_score,teacher_notes,reviewed_by,reviewed_at,published_at) VALUES($1,$2,'published',$3,$4,$5,$6,$7,$8,$8)`, org, id, content, report.Total, report.Maximum, report.Notes, p.UserID, time.Now())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	h.engine.RecordAudit(r.Context(), req, from)
	httpx.JSON(w, r, 200, report)
}
