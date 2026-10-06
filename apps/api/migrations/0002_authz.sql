-- Authorisation: permissions are the unit of truth, roles are collections.
-- Nothing in the application checks a role name.

CREATE TABLE permissions (
    key         text PRIMARY KEY,          -- 'assessment.grade'
    resource    text NOT NULL,             -- 'assessment'
    action      text NOT NULL,             -- 'grade'
    description text NOT NULL,
    -- super-admin only; never grantable to an organisation role
    is_platform boolean NOT NULL DEFAULT false
);

CREATE TABLE roles (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- NULL means a platform-level role (super admin) owned by no tenant
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    key             text NOT NULL,         -- 'student', 'teacher', 'admin'
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    -- system roles are seeded and cannot be deleted; custom roles can
    is_system       boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX roles_org_key_uniq
    ON roles (organization_id, key) WHERE organization_id IS NOT NULL;
CREATE UNIQUE INDEX roles_platform_key_uniq
    ON roles (key) WHERE organization_id IS NULL;

CREATE TRIGGER roles_updated_at
    BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE role_permissions (
    role_id        uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_key text NOT NULL REFERENCES permissions(key) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE user_roles (
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    granted_by uuid REFERENCES users(id) ON DELETE SET NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX user_roles_role_idx ON user_roles (role_id);

-- Rotating refresh tokens with reuse detection. A replayed token revokes the
-- entire family, because replay means the token leaked.
CREATE TABLE refresh_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    family_id       uuid NOT NULL,
    token_hash      bytea NOT NULL UNIQUE,   -- sha256 of the opaque token
    parent_id       uuid REFERENCES refresh_tokens(id) ON DELETE SET NULL,
    user_agent      text NOT NULL DEFAULT '',
    ip              inet,
    expires_at      timestamptz NOT NULL,
    used_at         timestamptz,
    revoked_at      timestamptz,
    revoked_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX refresh_tokens_user_idx   ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_expiry_idx ON refresh_tokens (expires_at)
    WHERE revoked_at IS NULL;

-- Append-only. Never updated, never deleted by application code.
CREATE TABLE audit_logs (
    id              bigserial PRIMARY KEY,
    organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    actor_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
    actor_kind      text NOT NULL DEFAULT 'user'
                        CHECK (actor_kind IN ('user', 'system', 'ai')),
    action          text NOT NULL,          -- 'assessment.grade.override'
    target_kind     text NOT NULL DEFAULT '',
    target_id       uuid,
    reason          text,
    before_state    jsonb,
    after_state     jsonb,
    request_id      text NOT NULL DEFAULT '',
    ip              inet,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_logs_org_created_idx ON audit_logs (organization_id, created_at DESC);
CREATE INDEX audit_logs_target_idx      ON audit_logs (target_kind, target_id);
CREATE INDEX audit_logs_actor_idx       ON audit_logs (actor_user_id, created_at DESC);
