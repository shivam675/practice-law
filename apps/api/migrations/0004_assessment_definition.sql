-- Assessment definition: types, templates, immutable template versions,
-- rubrics and AI profiles. Nothing here knows what a moot court is.

CREATE TABLE assessment_types (
    key         text PRIMARY KEY,           -- 'moot_court', 'viva', 'interview'
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    icon        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO assessment_types (key, name, description) VALUES
  ('moot_court', 'Moot Court',          'Written memorial plus oral argument before a bench'),
  ('viva',       'Oral Examination',    'Examiner-led oral questioning'),
  ('interview',  'Interview',           'Structured interview with an AI interviewer')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE rubrics (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key             text NOT NULL,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX rubrics_org_key_uniq ON rubrics (organization_id, key);
CREATE TRIGGER rubrics_updated_at BEFORE UPDATE ON rubrics
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE rubric_criteria (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rubric_id   uuid NOT NULL REFERENCES rubrics(id) ON DELETE CASCADE,
    key         text NOT NULL,              -- 'legal_reasoning'
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    -- relative weight; totals are normalised in application code so weights
    -- need not sum to 100
    weight      numeric(6,2) NOT NULL CHECK (weight > 0),
    max_score   numeric(6,2) NOT NULL CHECK (max_score > 0),
    -- which stage kinds can produce a score for this criterion
    scope       text[] NOT NULL DEFAULT '{}',
    -- guidance injected into the grading prompt for this criterion only
    guidance    text NOT NULL DEFAULT '',
    sort_order  int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX rubric_criteria_rubric_key_uniq ON rubric_criteria (rubric_id, key);
CREATE INDEX rubric_criteria_rubric_idx ON rubric_criteria (rubric_id, sort_order);

CREATE TABLE assessment_templates (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assessment_type_key  text NOT NULL REFERENCES assessment_types(key),
    key                  text NOT NULL,
    name                 text NOT NULL,
    description          text NOT NULL DEFAULT '',
    created_by           uuid REFERENCES users(id) ON DELETE SET NULL,
    archived_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX assessment_templates_org_key_uniq
    ON assessment_templates (organization_id, key);
CREATE INDEX assessment_templates_org_type_idx
    ON assessment_templates (organization_id, assessment_type_key);
CREATE TRIGGER assessment_templates_updated_at BEFORE UPDATE ON assessment_templates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A version becomes immutable the moment an assessment references it, so a
-- template can keep evolving while assessments are in flight.
CREATE TABLE assessment_template_versions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id uuid NOT NULL REFERENCES assessment_templates(id) ON DELETE CASCADE,
    version     int NOT NULL CHECK (version > 0),
    rubric_id   uuid NOT NULL REFERENCES rubrics(id) ON DELETE RESTRICT,
    -- ordered list of stage specs; see docs/state-machine.md
    stages      jsonb NOT NULL,
    -- participant shape: sides, team size, speaker count, AI actor slots
    participation jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- default assessment-level settings this version expects
    defaults    jsonb NOT NULL DEFAULT '{}'::jsonb,
    status      text NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'published', 'deprecated')),
    published_at timestamptz,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX template_versions_uniq ON assessment_template_versions (template_id, version);
CREATE INDEX template_versions_status_idx  ON assessment_template_versions (template_id, status);

-- AI actor configuration lives in the database, not in repository YAML, so a
-- teacher can retune a judge without a deploy.
CREATE TABLE ai_profiles (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key             text NOT NULL,
    name            text NOT NULL,
    role            text NOT NULL
                        CHECK (role IN ('judge', 'examiner', 'interviewer',
                                        'opponent', 'moderator', 'evaluator')),
    version         int NOT NULL DEFAULT 1,
    model_tier      text NOT NULL DEFAULT 'judge'
                        CHECK (model_tier IN ('monitor', 'judge', 'grader')),
    system_prompt   text NOT NULL DEFAULT '',
    temperature     numeric(3,2) NOT NULL DEFAULT 0.40
                        CHECK (temperature >= 0 AND temperature <= 2),
    voice           text NOT NULL DEFAULT '',
    -- firmness, interruption_frequency, patience
    personality     jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- min_priority, cooldown_s, max_per_stage
    interruption_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    focus           text[] NOT NULL DEFAULT '{}',
    -- enforced by the coordinator in code, never by the prompt
    capabilities    text[] NOT NULL DEFAULT '{ask_question,evaluate}',
    rag_sources     text[] NOT NULL DEFAULT '{}',
    is_active       boolean NOT NULL DEFAULT true,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX ai_profiles_org_key_version_uniq
    ON ai_profiles (organization_id, key, version);
CREATE INDEX ai_profiles_org_role_idx ON ai_profiles (organization_id, role)
    WHERE is_active;
CREATE TRIGGER ai_profiles_updated_at BEFORE UPDATE ON ai_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
