-- Assessment instances, source material, teams, assignments and artifacts.

CREATE TABLE assessments (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    template_version_id uuid NOT NULL REFERENCES assessment_template_versions(id) ON DELETE RESTRICT,
    title               text NOT NULL,
    description         text NOT NULL DEFAULT '',
    status              text NOT NULL DEFAULT 'draft'
                            CHECK (status IN ('draft', 'published', 'running',
                                              'closed', 'archived')),
    -- per-assessment overrides merged over the template version defaults
    config              jsonb NOT NULL DEFAULT '{}'::jsonb,
    opens_at            timestamptz,
    closes_at           timestamptz,
    published_at        timestamptz,
    created_by          uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CHECK (closes_at IS NULL OR opens_at IS NULL OR closes_at > opens_at)
);

CREATE INDEX assessments_org_status_idx ON assessments (organization_id, status);
CREATE INDEX assessments_org_opens_idx  ON assessments (organization_id, opens_at);
CREATE TRIGGER assessments_updated_at BEFORE UPDATE ON assessments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Source material the AI is grounded in: the problem, statutes, authorities,
-- marking guidance.
CREATE TABLE knowledge_sources (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assessment_id   uuid REFERENCES assessments(id) ON DELETE CASCADE,
    kind            text NOT NULL
                        CHECK (kind IN ('problem', 'authority', 'statute',
                                        'evidence', 'guidance', 'other')),
    title           text NOT NULL,
    -- who may see it: 'all', 'applicant', 'respondent', 'staff'
    visibility      text NOT NULL DEFAULT 'all',
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX knowledge_sources_assessment_idx ON knowledge_sources (assessment_id, kind);

CREATE TABLE documents (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    knowledge_source_id uuid REFERENCES knowledge_sources(id) ON DELETE CASCADE,
    storage_key         text NOT NULL,
    filename            text NOT NULL,
    content_type        text NOT NULL,
    byte_size           bigint NOT NULL CHECK (byte_size >= 0),
    sha256              bytea NOT NULL,
    -- 'pending' | 'parsing' | 'parsed' | 'failed'
    parse_status        text NOT NULL DEFAULT 'pending'
                            CHECK (parse_status IN ('pending', 'parsing', 'parsed', 'failed')),
    parse_error         text,
    extracted_text      text,
    page_count          int,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX documents_source_idx ON documents (knowledge_source_id);
CREATE INDEX documents_org_status_idx ON documents (organization_id, parse_status);
CREATE TRIGGER documents_updated_at BEFORE UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Hybrid retrieval: dense (pgvector) fused with lexical (tsvector).
CREATE TABLE document_chunks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    document_id     uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    chunk_index     int NOT NULL,
    -- human-readable anchor used in evidence spans: 'p. 14', 'Section 3.2'
    locator         text NOT NULL DEFAULT '',
    content         text NOT NULL,
    token_count     int NOT NULL DEFAULT 0,
    embedding       vector(1024),
    content_tsv     tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX document_chunks_doc_index_uniq ON document_chunks (document_id, chunk_index);
CREATE INDEX document_chunks_tsv_idx ON document_chunks USING gin (content_tsv);
CREATE INDEX document_chunks_embedding_idx ON document_chunks
    USING hnsw (embedding vector_cosine_ops);

-- A solo candidate is a team of one. This removes the individual-versus-team
-- dual code path entirely. See ADR 0004.
CREATE TABLE teams (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX teams_org_idx ON teams (organization_id);
CREATE TRIGGER teams_updated_at BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE team_members (
    team_id       uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role          text NOT NULL DEFAULT 'speaker'
                      CHECK (role IN ('speaker', 'researcher')),
    -- 1 and 2 for a two-speaker moot; NULL for a researcher
    speaking_order int CHECK (speaking_order IS NULL OR speaking_order > 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);

CREATE UNIQUE INDEX team_members_speaking_order_uniq
    ON team_members (team_id, speaking_order) WHERE speaking_order IS NOT NULL;
CREATE INDEX team_members_user_idx ON team_members (user_id);

-- The unit of work, of grading and of reporting.
CREATE TABLE assignments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assessment_id   uuid NOT NULL REFERENCES assessments(id) ON DELETE CASCADE,
    team_id         uuid NOT NULL REFERENCES teams(id) ON DELETE RESTRICT,
    -- 'applicant' | 'respondent' | 'candidate' | free text per assessment type
    side            text NOT NULL DEFAULT 'candidate',
    status          text NOT NULL DEFAULT 'assigned'
                        CHECK (status IN ('assigned', 'in_progress', 'awaiting_review',
                                          'finalized', 'abandoned', 'withdrawn')),
    current_stage_id text,
    assigned_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    assigned_at     timestamptz NOT NULL DEFAULT now(),
    finalized_at    timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX assignments_assessment_team_uniq ON assignments (assessment_id, team_id);
CREATE INDEX assignments_org_status_idx ON assignments (organization_id, status);
CREATE INDEX assignments_assessment_idx ON assignments (assessment_id, side);
CREATE TRIGGER assignments_updated_at BEFORE UPDATE ON assignments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Per-assignment stage progress. The stage definition lives in the template
-- version; only progress lives here.
CREATE TABLE assignment_stages (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assignment_id   uuid NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    stage_id        text NOT NULL,          -- matches a stage spec id
    stage_kind      text NOT NULL
                        CHECK (stage_kind IN ('wait', 'artifact_submission',
                                              'live_turn', 'automated_evaluation',
                                              'human_review')),
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'active', 'grace', 'completed',
                                          'expired', 'skipped', 'failed')),
    sort_order      int NOT NULL DEFAULT 0,
    opens_at        timestamptz,
    due_at          timestamptz,
    grace_until     timestamptz,
    started_at      timestamptz,
    completed_at    timestamptz,
    failure_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX assignment_stages_uniq ON assignment_stages (assignment_id, stage_id);
CREATE INDEX assignment_stages_due_idx ON assignment_stages (due_at)
    WHERE status IN ('pending', 'active', 'grace');

CREATE TRIGGER assignment_stages_updated_at BEFORE UPDATE ON assignment_stages
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Uploaded submissions. Versions are append-only; locking is a conditional
-- UPDATE, which is the real deadline enforcement.
CREATE TABLE artifacts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assignment_id   uuid NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    stage_id        text NOT NULL,
    version         int NOT NULL CHECK (version > 0),
    document_id     uuid NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    uploaded_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    -- deterministic format compliance result; see docs/ai-harness.md
    compliance      jsonb NOT NULL DEFAULT '{}'::jsonb,
    submitted_at    timestamptz,
    locked_at       timestamptz,
    is_late         boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX artifacts_assignment_stage_version_uniq
    ON artifacts (assignment_id, stage_id, version);
CREATE INDEX artifacts_org_idx ON artifacts (organization_id, created_at DESC);
