-- Foundation: extensions, tenancy root, users.
-- Every tenant-scoped table from here on carries organization_id NOT NULL.

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS vector;

-- Reusable updated_at trigger.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE organizations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            text NOT NULL UNIQUE,
    name            text NOT NULL,
    status          text NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'suspended', 'archived')),
    -- branding, locale, retention policy, feature flags
    settings        jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- hard ceiling on simultaneous live sessions for this tenant
    max_concurrent_sessions int NOT NULL DEFAULT 4
                        CHECK (max_concurrent_sessions >= 0),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER organizations_updated_at
    BEFORE UPDATE ON organizations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           text NOT NULL,
    email_normalized text NOT NULL,
    full_name       text NOT NULL,
    password_hash   text,
    status          text NOT NULL DEFAULT 'active'
                        CHECK (status IN ('invited', 'active', 'suspended', 'deleted')),
    -- external identity (OIDC / SAML / LTI) lands here in V2
    external_subject text,
    last_login_at   timestamptz,
    failed_login_count int NOT NULL DEFAULT 0,
    locked_until    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Email is unique per tenant, not globally: the same person may belong to
-- two institutions.
CREATE UNIQUE INDEX users_org_email_key
    ON users (organization_id, email_normalized);
CREATE INDEX users_org_status_idx
    ON users (organization_id, status);
CREATE INDEX users_org_name_trgm_idx
    ON users USING gin (full_name gin_trgm_ops);

CREATE TRIGGER users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
