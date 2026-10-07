package llm

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ledger appends to ai_requests. Every model call is recorded, successful or
// not: it is the usage view, the billing source, and the only honest answer
// to "why was this session slow".
//
// A failed ledger write never fails the caller's operation, for the same
// reason the audit log does not.
type Ledger struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func NewLedger(pool *pgxpool.Pool, log *slog.Logger) *Ledger {
	return &Ledger{pool: pool, log: log}
}

type Entry struct {
	OrganizationID *uuid.UUID
	SessionID      *uuid.UUID
	AssignmentID   *uuid.UUID
	TraceID        string
	Purpose        string // 'connection_test', 'grade_criterion', 'judge_question'
	Provider       string
	Model          string
	ModelTier      string
	PromptVersion  string
	InputTokens    int
	OutputTokens   int
	Latency        time.Duration
	Err            error
}

func (l *Ledger) Record(ctx context.Context, e Entry) {
	var errText any
	if e.Err != nil {
		errText = truncate(e.Err.Error(), 1000)
	}

	if _, err := l.pool.Exec(context.WithoutCancel(ctx), `
		INSERT INTO ai_requests
			(organization_id, session_id, assignment_id, trace_id, purpose,
			 provider, model, model_tier, prompt_version,
			 input_tokens, output_tokens, latency_ms, status, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		e.OrganizationID, e.SessionID, e.AssignmentID, e.TraceID, e.Purpose,
		e.Provider, e.Model, e.ModelTier, e.PromptVersion,
		e.InputTokens, e.OutputTokens, e.Latency.Milliseconds(), StatusFor(e.Err), errText,
	); err != nil {
		l.log.Error("ledger: write ai_request failed",
			"purpose", e.Purpose, "model", e.Model, "error", err)
	}
}

// StatusFor maps an error to the ai_requests.status vocabulary. Separating a
// timeout from a provider error matters: the first is a capacity problem and
// the second is a configuration one.
func StatusFor(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}

	var provErr *Error
	if errors.As(err, &provErr) && provErr.Status == 0 &&
		strings.Contains(provErr.Detail, "timed out") {
		return "timeout"
	}
	return "provider_error"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
