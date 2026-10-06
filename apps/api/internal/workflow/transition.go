// Package workflow owns every state change in the assessment lifecycle.
//
// Three machines, not one: an assignment runs for days, a stage runs inside
// it, and a live session runs inside a stage. Collapsing them is why
// assessment platforms rot. See docs/state-machine.md.
//
// Legal transitions live in a table. Nothing outside this package writes a
// status column, and no AI actor can reach it: agents propose, the engine
// decides.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/audit"
	"github.com/intelimek/megamoot/apps/api/internal/httpx"
)

type Subject string

const (
	SubjectAssignment Subject = "assignment"
	SubjectStage      Subject = "assignment_stage"
	SubjectSession    Subject = "session"
)

// Assignment states.
const (
	AssignmentAssigned       = "assigned"
	AssignmentInProgress     = "in_progress"
	AssignmentAwaitingReview = "awaiting_review"
	AssignmentFinalized      = "finalized"
	AssignmentAbandoned      = "abandoned"
	AssignmentWithdrawn      = "withdrawn"
)

// Stage states.
const (
	StagePending   = "pending"
	StageActive    = "active"
	StageGrace     = "grace"
	StageCompleted = "completed"
	StageExpired   = "expired"
	StageSkipped   = "skipped"
	StageFailed    = "failed"
)

// Session states.
const (
	SessionScheduled   = "scheduled"
	SessionLobby       = "lobby"
	SessionDeviceCheck = "device_check"
	SessionRunning     = "running"
	SessionPaused      = "paused"
	SessionDegraded    = "degraded"
	SessionEnded       = "ended"
	SessionAborted     = "aborted"
	SessionEvaluating  = "evaluating"
	SessionEvaluated   = "evaluated"
)

type edge struct{ from, to string }

// machines is the whole truth about what may follow what. A transition absent
// from this table is a bug, not an edge case.
var machines = map[Subject]map[edge]struct{}{
	SubjectAssignment: edges(
		AssignmentAssigned, AssignmentInProgress,
		AssignmentAssigned, AssignmentWithdrawn,
		AssignmentAssigned, AssignmentAbandoned,
		AssignmentInProgress, AssignmentAwaitingReview,
		AssignmentInProgress, AssignmentAbandoned,
		AssignmentInProgress, AssignmentWithdrawn,
		AssignmentAwaitingReview, AssignmentFinalized,
		// A teacher may reopen a review that was finalised in error.
		AssignmentAwaitingReview, AssignmentInProgress,
		AssignmentFinalized, AssignmentAwaitingReview,
		// An excused student resumes where they left off.
		AssignmentAbandoned, AssignmentInProgress,
	),
	SubjectStage: edges(
		StagePending, StageActive,
		StagePending, StageSkipped,
		StagePending, StageExpired,
		StageActive, StageCompleted,
		StageActive, StageGrace,
		StageActive, StageFailed,
		StageGrace, StageCompleted,
		StageGrace, StageExpired,
		// Teacher override, always audited with a reason.
		StageExpired, StageCompleted,
		StageExpired, StageActive,
		StageFailed, StageActive,
	),
	SubjectSession: edges(
		SessionScheduled, SessionLobby,
		SessionScheduled, SessionAborted,
		SessionLobby, SessionDeviceCheck,
		SessionLobby, SessionAborted,
		SessionDeviceCheck, SessionRunning,
		SessionDeviceCheck, SessionLobby,
		SessionDeviceCheck, SessionAborted,
		SessionRunning, SessionPaused,
		SessionRunning, SessionDegraded,
		SessionRunning, SessionEnded,
		SessionRunning, SessionAborted,
		SessionPaused, SessionRunning,
		SessionPaused, SessionDegraded,
		SessionPaused, SessionAborted,
		SessionPaused, SessionEnded,
		// Degraded is a first-class state: a failed component must not end a
		// graded session. See docs/live-session.md.
		SessionDegraded, SessionRunning,
		SessionDegraded, SessionEnded,
		SessionDegraded, SessionAborted,
		SessionEnded, SessionEvaluating,
		SessionAborted, SessionEvaluating,
		SessionEvaluating, SessionEvaluated,
		// A re-grade after a prompt change replays evaluation.
		SessionEvaluated, SessionEvaluating,
	),
}

func edges(pairs ...string) map[edge]struct{} {
	if len(pairs)%2 != 0 {
		panic("workflow: edges needs from/to pairs")
	}
	out := make(map[edge]struct{}, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out[edge{pairs[i], pairs[i+1]}] = struct{}{}
	}
	return out
}

// Allowed reports whether a subject may move between two states.
func Allowed(subject Subject, from, to string) bool {
	m, ok := machines[subject]
	if !ok {
		return false
	}
	_, ok = m[edge{from, to}]
	return ok
}

// States returns every state a subject's machine can be in. Used by tests and
// by the admin UI; the authoritative list is the transition table itself.
func States(subject Subject) []string {
	seen := map[string]struct{}{}
	for e := range machines[subject] {
		seen[e.from] = struct{}{}
		seen[e.to] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	return out
}

var subjectTables = map[Subject]string{
	SubjectAssignment: "assignments",
	SubjectStage:      "assignment_stages",
	SubjectSession:    "sessions",
}

// timestampColumn is the column stamped when a subject reaches a state. An
// empty result means the state records no timestamp of its own.
func timestampColumn(subject Subject, to string) string {
	switch subject {
	case SubjectAssignment:
		if to == AssignmentFinalized {
			return "finalized_at"
		}
	case SubjectStage:
		switch to {
		case StageActive:
			return "started_at"
		case StageCompleted, StageExpired, StageSkipped, StageFailed:
			return "completed_at"
		}
	case SubjectSession:
		switch to {
		case SessionRunning:
			return "started_at"
		case SessionEnded, SessionAborted:
			return "ended_at"
		}
	}
	return ""
}

type ActorKind string

const (
	ActorUser   ActorKind = "user"
	ActorSystem ActorKind = "system"
)

type Actor struct {
	Kind   ActorKind
	UserID *uuid.UUID
}

func SystemActor() Actor { return Actor{Kind: ActorSystem} }

func UserActor(id uuid.UUID) Actor { return Actor{Kind: ActorUser, UserID: &id} }

type Request struct {
	Subject        Subject
	SubjectID      uuid.UUID
	OrganizationID uuid.UUID
	To             string
	// Cause is a machine-readable trigger: 'deadline_passed', 'teacher_override'.
	Cause  string
	Reason string
	Actor  Actor
	// Expect, when set, requires the subject to currently be in this state.
	// Use it where a stale read would otherwise cause a lost update.
	Expect string
}

var (
	// ErrNoop means the subject already holds the target state. Returned so
	// retried and duplicated triggers are safe.
	ErrNoop = errors.New("workflow: already in the target state")
)

// Hook runs after a transition commits. It is how one machine drives
// another: a stage completing advances its assignment.
//
// Hooks never run inside the transaction, so a hook failure cannot roll back
// a state change that already happened. They are wired once at start-up.
type Hook func(ctx context.Context, req Request, from string) error

type Engine struct {
	pool  *pgxpool.Pool
	audit *audit.Logger
	log   *slog.Logger
	hook  Hook
}

func NewEngine(pool *pgxpool.Pool, auditLog *audit.Logger, log *slog.Logger) *Engine {
	return &Engine{pool: pool, audit: auditLog, log: log}
}

// SetHook installs the post-transition hook. Called once during start-up,
// before the server accepts requests.
func (e *Engine) SetHook(h Hook) { e.hook = h }

func (e *Engine) Pool() *pgxpool.Pool { return e.pool }

// Apply performs one validated, audited transition in its own transaction.
func (e *Engine) Apply(ctx context.Context, req Request) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transition: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	from, err := e.ApplyTx(ctx, tx, req)
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transition: %w", err)
	}

	e.recordAudit(ctx, req, from)

	if e.hook != nil {
		if err := e.hook(ctx, req, from); err != nil {
			// The transition itself is committed and correct; only the
			// follow-on work failed. Surfacing it as a request failure would
			// wrongly suggest the state change did not happen.
			e.log.Error("post-transition hook failed",
				"subject", req.Subject, "subject_id", req.SubjectID,
				"from", from, "to", req.To, "error", err)
		}
	}
	return nil
}

// ApplyTx performs the transition inside a caller-supplied transaction, for
// the common case where a status change and its side effects must commit
// together. It returns the previous state.
//
// The caller is responsible for calling RecordAudit after a successful commit.
func (e *Engine) ApplyTx(ctx context.Context, tx pgx.Tx, req Request) (from string, err error) {
	if req.Actor.Kind != ActorUser && req.Actor.Kind != ActorSystem {
		// An AI actor reaching this function would mean a model is driving
		// business state. Agents propose; the engine decides.
		return "", fmt.Errorf("workflow: actor kind %q may not cause transitions", req.Actor.Kind)
	}
	table, ok := subjectTables[req.Subject]
	if !ok {
		return "", fmt.Errorf("workflow: unknown subject %q", req.Subject)
	}
	if req.Cause == "" {
		return "", fmt.Errorf("workflow: a transition needs a cause")
	}

	// Lock the row so two concurrent triggers cannot both read 'active'.
	err = tx.QueryRow(ctx,
		`SELECT status FROM `+table+` WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		req.SubjectID, req.OrganizationID).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", httpx.ErrNotFound()
	}
	if err != nil {
		return "", fmt.Errorf("load %s state: %w", req.Subject, err)
	}

	if from == req.To {
		return from, ErrNoop
	}
	if req.Expect != "" && from != req.Expect {
		return from, httpx.ErrConflict(fmt.Sprintf(
			"Expected state %q but found %q.", req.Expect, from))
	}
	if !Allowed(req.Subject, from, req.To) {
		return from, httpx.Err(http.StatusConflict, "illegal_transition",
			fmt.Sprintf("A %s cannot move from %q to %q.", req.Subject, from, req.To))
	}

	set := "status = $3"
	if col := timestampColumn(req.Subject, req.To); col != "" {
		set += ", " + col + " = coalesce(" + col + ", now())"
	}

	tag, err := tx.Exec(ctx,
		`UPDATE `+table+` SET `+set+` WHERE id = $1 AND organization_id = $2 AND status = $4`,
		req.SubjectID, req.OrganizationID, req.To, from)
	if err != nil {
		return from, fmt.Errorf("apply transition: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return from, httpx.ErrConflict("The record changed while the transition was being applied.")
	}

	return from, nil
}

// RecordAudit writes the audit entry for a transition applied through ApplyTx.
// Call it after the surrounding transaction commits, so a rolled-back change
// leaves no audit trail claiming it happened.
func (e *Engine) RecordAudit(ctx context.Context, req Request, from string) {
	e.recordAudit(ctx, req, from)
}

func (e *Engine) recordAudit(ctx context.Context, req Request, from string) {
	org := req.OrganizationID
	target := req.SubjectID
	actorKind := string(req.Actor.Kind)

	e.audit.Record(ctx, audit.Entry{
		OrganizationID: &org,
		ActorUserID:    req.Actor.UserID,
		ActorKind:      actorKind,
		Action:         string(req.Subject) + ".transition",
		TargetKind:     string(req.Subject),
		TargetID:       &target,
		Reason:         req.Reason,
		Before:         map[string]any{"status": from},
		After:          map[string]any{"status": req.To, "cause": req.Cause},
		RequestID:      httpx.RequestIDFrom(ctx),
	})
}

// Schedule records a durable timer. Never an in-process timer: those die with
// the process, and a missed deadline on an assessment platform is a dispute.
//
// causeKey makes the schedule idempotent, so a scheduler running twice does
// not queue the same deadline twice.
func Schedule(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, subject Subject,
	subjectID uuid.UUID, to, cause, causeKey string, runAt time.Time) error {

	_, err := tx.Exec(ctx, `
		INSERT INTO scheduled_transitions
			(organization_id, subject_kind, subject_id, target_state, cause,
			 cause_key, run_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (subject_kind, subject_id, cause_key) DO UPDATE
		SET run_at = EXCLUDED.run_at,
		    target_state = EXCLUDED.target_state,
		    status = 'pending',
		    last_error = NULL
		WHERE scheduled_transitions.status = 'pending'`,
		orgID, subject, subjectID, to, cause, causeKey, runAt)
	if err != nil {
		return fmt.Errorf("schedule transition: %w", err)
	}
	return nil
}

// CancelScheduled drops pending timers for a subject, used when a stage
// completes early and its deadline no longer applies.
func CancelScheduled(ctx context.Context, tx pgx.Tx, subject Subject, subjectID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE scheduled_transitions SET status = 'cancelled'
		WHERE subject_kind = $1 AND subject_id = $2 AND status = 'pending'`,
		subject, subjectID)
	if err != nil {
		return fmt.Errorf("cancel scheduled transitions: %w", err)
	}
	return nil
}
