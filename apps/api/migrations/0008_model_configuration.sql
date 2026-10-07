-- Model configuration.
--
-- Deliberately NOT tenant scoped. There is one GPU box and one set of
-- providers behind it; which model answers a judge is an operator decision,
-- not an organisation's. Writes are gated on platform.model.configure, the
-- platform permission no organisation role can be granted.
--
-- What an organisation DOES control is ai_profiles: prompt, temperature,
-- personality, capabilities. Those stay org scoped.

CREATE TABLE model_providers (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key            text NOT NULL UNIQUE,
    name           text NOT NULL,
    kind           text NOT NULL DEFAULT 'openai_compatible'
                       CHECK (kind IN ('openai_compatible', 'anthropic')),
    -- Origin, with or without the /v1 suffix; the client normalises it.
    base_url       text NOT NULL,
    -- AES-256-GCM, keyed by CONFIG_ENCRYPTION_KEY. No route returns this
    -- column, encrypted or otherwise.
    api_key_cipher bytea,
    -- Last four characters, so an operator can tell which key is loaded
    -- without the key being readable.
    api_key_hint   text NOT NULL DEFAULT '',
    is_active      boolean NOT NULL DEFAULT true,
    created_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER model_providers_updated_at BEFORE UPDATE ON model_providers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One binding per tier, platform wide. The tier is the primary key because
-- two models answering the same tier is a misconfiguration, not a feature.
CREATE TABLE model_bindings (
    tier        text PRIMARY KEY
                    CHECK (tier IN ('monitor', 'judge', 'grader', 'embedding')),
    provider_id uuid NOT NULL REFERENCES model_providers(id) ON DELETE RESTRICT,
    model       text NOT NULL,
    temperature numeric(3,2) NOT NULL DEFAULT 0.40
                    CHECK (temperature >= 0 AND temperature <= 2),
    top_p       numeric(3,2) NOT NULL DEFAULT 1.00
                    CHECK (top_p > 0 AND top_p <= 1),
    max_tokens  int NOT NULL DEFAULT 1024
                    CHECK (max_tokens BETWEEN 1 AND 131072),
    -- Live tiers have a latency budget. See docs/live-session.md.
    timeout_ms  int NOT NULL DEFAULT 3000
                    CHECK (timeout_ms BETWEEN 200 AND 600000),
    updated_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER model_bindings_updated_at BEFORE UPDATE ON model_bindings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
