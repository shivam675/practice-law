-- Durable work that one committed state transition triggers in another machine.
CREATE TABLE workflow_followups (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    subject_kind    text NOT NULL
                        CHECK (subject_kind IN ('assignment', 'assignment_stage', 'session')),
    subject_id      uuid NOT NULL,
    from_state      text NOT NULL,
    target_state    text NOT NULL,
    cause           text NOT NULL,
    reason          text NOT NULL DEFAULT '',
    actor_kind      text NOT NULL CHECK (actor_kind IN ('user', 'system')),
    actor_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
    run_at          timestamptz NOT NULL DEFAULT now(),
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'running', 'done')),
    attempts        int NOT NULL DEFAULT 0,
    last_error      text,
    locked_by       text,
    locked_at       timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX workflow_followups_due_idx ON workflow_followups (run_at)
    WHERE status = 'pending';
CREATE TRIGGER workflow_followups_updated_at BEFORE UPDATE ON workflow_followups
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
