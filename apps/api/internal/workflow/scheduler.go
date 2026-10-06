package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/intelimek/megamoot/apps/api/internal/httpx"
)

// Scheduler executes durable timers: submission deadlines, grace expiry,
// session join windows.
//
// Claiming uses FOR UPDATE SKIP LOCKED, so several API replicas can run this
// concurrently without any of them doing the same work twice.
type Scheduler struct {
	engine   *Engine
	log      *slog.Logger
	nodeID   string
	interval time.Duration
	batch    int
	// maxAttempts bounds retries. A transition that keeps failing is a bug,
	// and retrying it forever hides the bug behind a busy log.
	maxAttempts int
}

func NewScheduler(engine *Engine, log *slog.Logger, nodeID string) *Scheduler {
	return &Scheduler{
		engine:      engine,
		log:         log.With("component", "scheduler"),
		nodeID:      nodeID,
		interval:    5 * time.Second,
		batch:       50,
		maxAttempts: 5,
	}
}

// Run blocks until the context is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.log.Info("scheduler started", "node", s.nodeID, "interval", s.interval.String())

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopped")
			return
		case <-ticker.C:
			if n, err := s.tick(ctx); err != nil {
				if !errors.Is(err, context.Canceled) {
					s.log.Error("scheduler tick failed", "error", err)
				}
			} else if n > 0 {
				s.log.Info("scheduled transitions processed", "count", n)
			}
		}
	}
}

type dueTransition struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Subject        Subject
	SubjectID      uuid.UUID
	TargetState    string
	Cause          string
	Attempts       int
}

func (s *Scheduler) tick(ctx context.Context) (int, error) {
	due, err := s.claim(ctx)
	if err != nil {
		return 0, err
	}

	for _, t := range due {
		s.execute(ctx, t)
	}
	return len(due), nil
}

// claim atomically moves a batch of due rows to 'running' and returns them.
func (s *Scheduler) claim(ctx context.Context) ([]dueTransition, error) {
	rows, err := s.engine.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM scheduled_transitions
			WHERE status = 'pending' AND run_at <= now()
			ORDER BY run_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE scheduled_transitions st
		SET status = 'running', attempts = st.attempts + 1,
		    locked_by = $2, locked_at = now()
		FROM due
		WHERE st.id = due.id
		RETURNING st.id, st.organization_id, st.subject_kind, st.subject_id,
		          st.target_state, st.cause, st.attempts`,
		s.batch, s.nodeID)
	if err != nil {
		return nil, fmt.Errorf("claim due transitions: %w", err)
	}
	defer rows.Close()

	var out []dueTransition
	for rows.Next() {
		var t dueTransition
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.Subject, &t.SubjectID,
			&t.TargetState, &t.Cause, &t.Attempts); err != nil {
			return nil, fmt.Errorf("scan due transition: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Scheduler) execute(ctx context.Context, t dueTransition) {
	log := s.log.With("transition_id", t.ID, "subject", t.Subject,
		"subject_id", t.SubjectID, "target", t.TargetState, "cause", t.Cause)

	err := s.engine.Apply(ctx, Request{
		Subject:        t.Subject,
		SubjectID:      t.SubjectID,
		OrganizationID: t.OrganizationID,
		To:             t.TargetState,
		Cause:          t.Cause,
		Actor:          SystemActor(),
	})

	switch {
	case err == nil:
		s.finish(ctx, t.ID, "done", "")
		log.Info("scheduled transition applied")

	case errors.Is(err, ErrNoop):
		// The subject reached the state by another route, which is the normal
		// outcome when a student submits before their deadline fires.
		s.finish(ctx, t.ID, "done", "")
		log.Debug("scheduled transition already satisfied")

	case isStale(err):
		// The subject moved on; the timer is stale rather than broken. A
		// submission deadline on a stage a teacher already completed is the
		// usual case.
		s.finish(ctx, t.ID, "cancelled", err.Error())
		log.Info("scheduled transition no longer applies", "reason", err)

	case t.Attempts >= s.maxAttempts:
		s.finish(ctx, t.ID, "failed", err.Error())
		log.Error("scheduled transition gave up", "attempts", t.Attempts, "error", err)

	default:
		s.retry(ctx, t, err)
		log.Warn("scheduled transition failed, will retry",
			"attempts", t.Attempts, "error", err)
	}
}

func (s *Scheduler) finish(ctx context.Context, id uuid.UUID, status, reason string) {
	ctx = context.WithoutCancel(ctx)
	if _, err := s.engine.pool.Exec(ctx, `
		UPDATE scheduled_transitions
		SET status = $2, last_error = nullif($3, ''), locked_by = NULL, locked_at = NULL
		WHERE id = $1`, id, status, reason); err != nil {
		s.log.Error("record scheduled transition outcome",
			"transition_id", id, "status", status, "error", err)
	}
}

// retry pushes the row back to pending with exponential backoff.
func (s *Scheduler) retry(ctx context.Context, t dueTransition, cause error) {
	backoff := time.Duration(1<<t.Attempts) * 10 * time.Second
	if backoff > 10*time.Minute {
		backoff = 10 * time.Minute
	}
	ctx = context.WithoutCancel(ctx)
	if _, err := s.engine.pool.Exec(ctx, `
		UPDATE scheduled_transitions
		SET status = 'pending', run_at = now() + $2::interval,
		    last_error = $3, locked_by = NULL, locked_at = NULL
		WHERE id = $1`, t.ID, backoff.String(), cause.Error()); err != nil {
		s.log.Error("reschedule failed transition", "transition_id", t.ID, "error", err)
	}
}

// ReleaseStale returns rows abandoned by a crashed node to the queue. Without
// it a replica dying mid-batch would strand those timers in 'running'.
func (s *Scheduler) ReleaseStale(ctx context.Context) error {
	tag, err := s.engine.pool.Exec(ctx, `
		UPDATE scheduled_transitions
		SET status = 'pending', locked_by = NULL, locked_at = NULL
		WHERE status = 'running' AND locked_at < now() - interval '5 minutes'`)
	if err != nil {
		return fmt.Errorf("release stale transitions: %w", err)
	}
	if n := tag.RowsAffected(); n > 0 {
		s.log.Warn("released stale scheduled transitions", "count", n)
	}
	return nil
}

// isStale reports whether a failure means the timer no longer applies rather
// than that something is broken: the subject moved on, or it was deleted.
func isStale(err error) bool {
	var apiErr *httpx.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case "illegal_transition", "conflict", "not_found":
		return true
	}
	return false
}
