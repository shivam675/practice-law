CREATE TABLE speech_tickets (
    id              uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_ip       inet NOT NULL,
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX speech_tickets_expiry_idx ON speech_tickets (expires_at)
    WHERE consumed_at IS NULL;
