package assessments

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/auth"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
	"github.com/intelimek/megamoot/apps/api/internal/spec"
	"github.com/intelimek/megamoot/apps/api/internal/workflow"
)

type Assignment struct {
	ID             uuid.UUID         `json:"id"`
	AssessmentID   uuid.UUID         `json:"assessment_id"`
	AssessmentName string            `json:"assessment_title,omitempty"`
	TeamID         uuid.UUID         `json:"team_id"`
	TeamName       string            `json:"team_name,omitempty"`
	Side           string            `json:"side"`
	Status         string            `json:"status"`
	CurrentStageID *string           `json:"current_stage_id"`
	Stages         []AssignmentStage `json:"stages,omitempty"`
	AssignedAt     time.Time         `json:"assigned_at"`
}

type AssignmentStage struct {
	ID          uuid.UUID  `json:"id"`
	StageID     string     `json:"stage_id"`
	StageKind   string     `json:"stage_kind"`
	Label       string     `json:"label,omitempty"`
	Status      string     `json:"status"`
	SortOrder   int        `json:"sort_order"`
	OpensAt     *time.Time `json:"opens_at"`
	DueAt       *time.Time `json:"due_at"`
	GraceUntil  *time.Time `json:"grace_until"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// timeDriven reports whether a stage starts on the clock or when its
// predecessor finishes.
//
// Waiting, submitting and speaking all happen at a scheduled time. Evaluating
// and reviewing happen as soon as there is something to evaluate or review, so
// they are driven by the preceding stage completing.
func timeDriven(kind spec.Kind) bool {
	switch kind {
	case spec.KindWait, spec.KindArtifactSubmission, spec.KindLiveTurn:
		return true
	}
	return false
}

type AssignInput struct {
	TeamID uuid.UUID `json:"team_id"`
	Side   string    `json:"side"`
}

// Assign attaches a team to an assessment and materialises its stage
// timeline in one transaction.
//
// Every deadline becomes an absolute timestamp and a durable scheduled
// transition here, rather than being recomputed later from offsets. A student
// and a teacher must see the same deadline, and the server must enforce it
// even if nothing asks.
func (s *Store) Assign(ctx context.Context, orgID, actorID, assessmentID uuid.UUID,
	in AssignInput) (Assignment, error) {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Assignment{}, fmt.Errorf("begin assign: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	assessment, err := s.Get(ctx, tx, orgID, assessmentID)
	if err != nil {
		return Assignment{}, err
	}
	if assessment.Status != StatusPublished && assessment.Status != StatusRunning {
		return Assignment{}, httpx.ErrConflict("Assessments must be published before teams are assigned.")
	}
	if assessment.OpensAt == nil {
		return Assignment{}, httpx.ErrConflict("That assessment has no opening time.")
	}

	version, err := s.templates.Version(ctx, tx, orgID, assessment.TemplateVersionID)
	if err != nil {
		return Assignment{}, err
	}

	if !version.Participation.HasSide(in.Side) {
		return Assignment{}, httpx.ErrBadRequest(fmt.Sprintf(
			"Unknown side %q; this assessment expects one of %v",
			in.Side, version.Participation.Sides))
	}

	members, speakers, err := s.teams.Size(ctx, tx, orgID, in.TeamID)
	if err != nil {
		return Assignment{}, err
	}
	if members == 0 {
		return Assignment{}, httpx.ErrBadRequest("That team does not exist or has no members.")
	}
	p := version.Participation
	if members < p.MinTeamSize || members > p.MaxTeamSize {
		return Assignment{}, httpx.ErrBadRequest(fmt.Sprintf(
			"This assessment expects teams of %d to %d; that team has %d members.",
			p.MinTeamSize, p.MaxTeamSize, members))
	}
	if speakers != p.Speakers {
		return Assignment{}, httpx.ErrBadRequest(fmt.Sprintf(
			"This assessment expects %d speaker(s); that team has %d.", p.Speakers, speakers))
	}

	out := Assignment{AssessmentID: assessmentID, TeamID: in.TeamID, Side: in.Side,
		Status: workflow.AssignmentAssigned}

	err = tx.QueryRow(ctx, `
		INSERT INTO assignments (organization_id, assessment_id, team_id, side, assigned_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, assigned_at`,
		orgID, assessmentID, in.TeamID, in.Side, actorID).Scan(&out.ID, &out.AssignedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return Assignment{}, httpx.ErrConflict("That team is already assigned to this assessment.")
		}
		return Assignment{}, fmt.Errorf("create assignment: %w", err)
	}

	base := *assessment.OpensAt
	for i, stage := range version.Stages {
		row, err := s.materialiseStage(ctx, tx, orgID, out.ID, base, i, stage)
		if err != nil {
			return Assignment{}, err
		}
		out.Stages = append(out.Stages, row)
	}

	if err := tx.Commit(ctx); err != nil {
		return Assignment{}, fmt.Errorf("commit assign: %w", err)
	}
	return out, nil
}

func (s *Store) materialiseStage(ctx context.Context, tx pgx.Tx, orgID, assignmentID uuid.UUID,
	base time.Time, index int, stage spec.Stage) (AssignmentStage, error) {

	row := AssignmentStage{StageID: stage.ID, StageKind: string(stage.Kind),
		Label: stage.Label, Status: workflow.StagePending, SortOrder: index}

	var opensAt, dueAt, graceUntil *time.Time
	if timeDriven(stage.Kind) {
		t := base.Add(time.Duration(stage.OpensAfterS) * time.Second)
		opensAt = &t
	}
	if stage.DueAfterS > 0 {
		t := base.Add(time.Duration(stage.DueAfterS) * time.Second)
		dueAt = &t
		if stage.GraceS > 0 {
			g := t.Add(time.Duration(stage.GraceS) * time.Second)
			graceUntil = &g
		}
	}
	row.OpensAt, row.DueAt, row.GraceUntil = opensAt, dueAt, graceUntil

	err := tx.QueryRow(ctx, `
		INSERT INTO assignment_stages
			(organization_id, assignment_id, stage_id, stage_kind, sort_order,
			 opens_at, due_at, grace_until)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		orgID, assignmentID, stage.ID, string(stage.Kind), index,
		opensAt, dueAt, graceUntil).Scan(&row.ID)
	if err != nil {
		return AssignmentStage{}, fmt.Errorf("materialise stage %q: %w", stage.ID, err)
	}

	if opensAt != nil {
		if err := workflow.Schedule(ctx, tx, orgID, workflow.SubjectStage, row.ID,
			workflow.StageActive, "stage_opened", "open", *opensAt); err != nil {
			return AssignmentStage{}, err
		}
	}

	// A deadline moves the stage into grace if one is configured, otherwise
	// straight to expired. Either way the server enforces it without being
	// asked.
	//
	// A waiting window is different: reaching the end of it is success, not a
	// missed deadline, so it completes rather than expiring.
	if dueAt != nil {
		target, cause := workflow.StageExpired, "deadline_passed"
		switch {
		case stage.Kind == spec.KindWait:
			target, cause = workflow.StageCompleted, "window_elapsed"
		case graceUntil != nil:
			target, cause = workflow.StageGrace, "deadline_passed_grace_open"
		}
		if err := workflow.Schedule(ctx, tx, orgID, workflow.SubjectStage, row.ID,
			target, cause, "due", *dueAt); err != nil {
			return AssignmentStage{}, err
		}
	}
	if graceUntil != nil {
		if err := workflow.Schedule(ctx, tx, orgID, workflow.SubjectStage, row.ID,
			workflow.StageExpired, "grace_elapsed", "grace", *graceUntil); err != nil {
			return AssignmentStage{}, err
		}
	}

	return row, nil
}

// Progress advances an assignment after one of its stages settles.
//
// It is the only place that decides what happens next, and it is driven by
// recorded state rather than by anything a model produced.
func (s *Store) Progress(ctx context.Context, orgID, stageRowID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin progress: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var assignmentID uuid.UUID
	var sortOrder int
	var assignmentStatus string
	err = tx.QueryRow(ctx, `
		SELECT s.assignment_id, s.sort_order, a.status
		FROM assignment_stages s
		JOIN assignments a ON a.id = s.assignment_id
		WHERE s.id = $1 AND s.organization_id = $2`, stageRowID, orgID).
		Scan(&assignmentID, &sortOrder, &assignmentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load stage for progress: %w", err)
	}

	// A settled stage's deadline timers are no longer meaningful.
	if err := workflow.CancelScheduled(ctx, tx, workflow.SubjectStage, stageRowID); err != nil {
		return err
	}

	var pending []struct {
		id   uuid.UUID
		kind string
	}
	rows, err := tx.Query(ctx, `
		SELECT id, stage_kind FROM assignment_stages
		WHERE assignment_id = $1 AND sort_order > $2 AND status = 'pending'
		ORDER BY sort_order`, assignmentID, sortOrder)
	if err != nil {
		return fmt.Errorf("load following stages: %w", err)
	}
	for rows.Next() {
		var e struct {
			id   uuid.UUID
			kind string
		}
		if err := rows.Scan(&e.id, &e.kind); err != nil {
			rows.Close()
			return fmt.Errorf("scan following stage: %w", err)
		}
		pending = append(pending, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var activate *uuid.UUID
	if len(pending) > 0 && !timeDriven(spec.Kind(pending[0].kind)) {
		activate = &pending[0].id
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit progress: %w", err)
	}

	if activate != nil {
		err := s.engine.Apply(ctx, workflow.Request{
			Subject: workflow.SubjectStage, SubjectID: *activate, OrganizationID: orgID,
			To: workflow.StageActive, Cause: "predecessor_completed", Actor: workflow.SystemActor(),
		})
		if err != nil && !errors.Is(err, workflow.ErrNoop) {
			return fmt.Errorf("activate next stage: %w", err)
		}
		return nil
	}

	if len(pending) == 0 {
		return s.finishAssignment(ctx, orgID, assignmentID, assignmentStatus)
	}
	return nil
}

func (s *Store) finishAssignment(ctx context.Context, orgID, assignmentID uuid.UUID, status string) error {
	// Finalisation is never automatic: a human signs off before results
	// publish. The assignment only moves as far as awaiting review.
	if status != workflow.AssignmentInProgress {
		return nil
	}
	err := s.engine.Apply(ctx, workflow.Request{
		Subject: workflow.SubjectAssignment, SubjectID: assignmentID, OrganizationID: orgID,
		To: workflow.AssignmentAwaitingReview, Cause: "all_stages_settled",
		Actor: workflow.SystemActor(),
	})
	if err != nil && !errors.Is(err, workflow.ErrNoop) {
		return fmt.Errorf("move assignment to review: %w", err)
	}
	return nil
}

// StartAssignment moves an assignment to in_progress the first time one of its
// stages becomes active.
func (s *Store) StartAssignment(ctx context.Context, orgID, stageRowID uuid.UUID) error {
	var assignmentID uuid.UUID
	var stageID string
	err := s.pool.QueryRow(ctx, `
		SELECT assignment_id, stage_id FROM assignment_stages
		WHERE id = $1 AND organization_id = $2`, stageRowID, orgID).Scan(&assignmentID, &stageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load stage for start: %w", err)
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE assignments SET current_stage_id = $2 WHERE id = $1`,
		assignmentID, stageID); err != nil {
		return fmt.Errorf("record current stage: %w", err)
	}

	err = s.engine.Apply(ctx, workflow.Request{
		Subject: workflow.SubjectAssignment, SubjectID: assignmentID, OrganizationID: orgID,
		To: workflow.AssignmentInProgress, Cause: "first_stage_active", Actor: workflow.SystemActor(),
	})
	if err != nil && !errors.Is(err, workflow.ErrNoop) {
		var apiErr *httpx.Error
		// An assignment already past 'assigned' is the normal case for every
		// stage after the first.
		if errors.As(err, &apiErr) && apiErr.Code == "illegal_transition" {
			return nil
		}
		return fmt.Errorf("start assignment: %w", err)
	}
	return nil
}

// ListAssignments returns assignments for an assessment, or across the whole
// organisation when assessmentID is nil.
func (s *Store) ListAssignments(ctx context.Context, orgID uuid.UUID,
	assessmentID *uuid.UUID, forUser *uuid.UUID) ([]Assignment, error) {

	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.assessment_id, ass.title, a.team_id, t.name, a.side,
		       a.status, a.current_stage_id, a.assigned_at
		FROM assignments a
		JOIN assessments ass ON ass.id = a.assessment_id
		JOIN teams t ON t.id = a.team_id
		WHERE a.organization_id = $1
		  AND ($2::uuid IS NULL OR a.assessment_id = $2)
		  AND ($3::uuid IS NULL OR EXISTS (
		        SELECT 1 FROM team_members m
		        WHERE m.team_id = a.team_id AND m.user_id = $3))
		ORDER BY a.assigned_at DESC`, orgID, assessmentID, forUser)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	defer rows.Close()

	out := []Assignment{}
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.ID, &a.AssessmentID, &a.AssessmentName, &a.TeamID,
			&a.TeamName, &a.Side, &a.Status, &a.CurrentStageID, &a.AssignedAt); err != nil {
			return nil, fmt.Errorf("scan assignment: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAssignment returns one assignment with its stage timeline. When forUser
// is set the caller only sees assignments for teams they belong to.
func (s *Store) GetAssignment(ctx context.Context, orgID, id uuid.UUID,
	forUser *uuid.UUID) (Assignment, error) {

	var a Assignment
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.assessment_id, ass.title, a.team_id, t.name, a.side,
		       a.status, a.current_stage_id, a.assigned_at
		FROM assignments a
		JOIN assessments ass ON ass.id = a.assessment_id
		JOIN teams t ON t.id = a.team_id
		WHERE a.id = $1 AND a.organization_id = $2
		  AND ($3::uuid IS NULL OR EXISTS (
		        SELECT 1 FROM team_members m
		        WHERE m.team_id = a.team_id AND m.user_id = $3))`,
		id, orgID, forUser).
		Scan(&a.ID, &a.AssessmentID, &a.AssessmentName, &a.TeamID, &a.TeamName,
			&a.Side, &a.Status, &a.CurrentStageID, &a.AssignedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Assignment{}, httpx.ErrNotFound()
	}
	if err != nil {
		return Assignment{}, fmt.Errorf("load assignment: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, stage_id, stage_kind, status, sort_order, opens_at, due_at,
		       grace_until, started_at, completed_at
		FROM assignment_stages
		WHERE assignment_id = $1 ORDER BY sort_order`, id)
	if err != nil {
		return Assignment{}, fmt.Errorf("load assignment stages: %w", err)
	}
	defer rows.Close()

	a.Stages = []AssignmentStage{}
	for rows.Next() {
		var st AssignmentStage
		if err := rows.Scan(&st.ID, &st.StageID, &st.StageKind, &st.Status, &st.SortOrder,
			&st.OpensAt, &st.DueAt, &st.GraceUntil, &st.StartedAt, &st.CompletedAt); err != nil {
			return Assignment{}, fmt.Errorf("scan assignment stage: %w", err)
		}
		a.Stages = append(a.Stages, st)
	}
	return a, rows.Err()
}

func (h *Handlers) Assign(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	assessmentID, err := uuid.Parse(chi.URLParam(r, "assessmentID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assessment id."))
		return
	}

	var in AssignInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	out, err := h.store.Assign(r.Context(), p.OrganizationID, p.UserID, assessmentID, in)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	h.audit.Record(r.Context(), audit.Entry{
		OrganizationID: &p.OrganizationID, ActorUserID: &p.UserID,
		Action: "assessment.assign", TargetKind: "assignment", TargetID: &out.ID,
		After: map[string]any{"team_id": out.TeamID, "side": out.Side,
			"stages": len(out.Stages)},
		RequestID: httpx.RequestIDFrom(r.Context()),
	})
	httpx.JSON(w, r, http.StatusCreated, out)
}

func (h *Handlers) ListAssignments(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	var assessmentID *uuid.UUID
	if raw := chi.URLParam(r, "assessmentID"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assessment id."))
			return
		}
		assessmentID = &id
	}

	// Without the organisation-wide permission a caller sees only their own
	// team's assignments, whatever they ask for.
	var forUser *uuid.UUID
	if !p.Can("assessment.view") {
		forUser = &p.UserID
	}

	out, err := h.store.ListAssignments(r.Context(), p.OrganizationID, assessmentID, forUser)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"assignments": out})
}

func (h *Handlers) GetAssignment(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "assignmentID"))
	if err != nil {
		httpx.Fail(w, r, httpx.ErrBadRequest("Invalid assignment id."))
		return
	}

	var forUser *uuid.UUID
	if !p.Can("assessment.view") {
		forUser = &p.UserID
	}

	out, err := h.store.GetAssignment(r.Context(), p.OrganizationID, id, forUser)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
