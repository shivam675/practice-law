// Package audit appends to the immutable audit log. Nothing in the
// application updates or deletes these rows.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Entry struct {
	OrganizationID *uuid.UUID
	ActorUserID    *uuid.UUID
	ActorKind      string // user | system | ai
	Action         string // 'auth.login', 'assessment.grade.override'
	TargetKind     string
	TargetID       *uuid.UUID
	Reason         string
	Before         any
	After          any
	RequestID      string
	IP             net.IP
}

type Logger struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Logger {
	return &Logger{pool: pool, log: log}
}

// Record writes one audit entry. A failure is logged but does not fail the
// caller's operation: losing an audit row is bad, but refusing a legitimate
// action because the audit insert failed is worse.
func (l *Logger) Record(ctx context.Context, e Entry) {
	if e.ActorKind == "" {
		e.ActorKind = "user"
	}

	before, err := marshalOrNil(e.Before)
	if err != nil {
		l.log.Error("audit: marshal before state", "action", e.Action, "error", err)
	}
	after, err := marshalOrNil(e.After)
	if err != nil {
		l.log.Error("audit: marshal after state", "action", e.Action, "error", err)
	}

	var ip any
	if e.IP != nil {
		ip = e.IP.String()
	}

	var reason any
	if e.Reason != "" {
		reason = e.Reason
	}

	if _, err := l.pool.Exec(ctx, `
		INSERT INTO audit_logs
			(organization_id, actor_user_id, actor_kind, action, target_kind,
			 target_id, reason, before_state, after_state, request_id, ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		e.OrganizationID, e.ActorUserID, e.ActorKind, e.Action, e.TargetKind,
		e.TargetID, reason, before, after, e.RequestID, ip,
	); err != nil {
		l.log.Error("audit: write entry failed",
			"action", e.Action, "request_id", e.RequestID, "error", err)
	}
}

func marshalOrNil(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
