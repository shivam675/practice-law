-- Evaluation, reports, model accounting and durable timers.

-- Immutable. A correction is a new row that supersedes an old one, so a
-- disputed grade always has a complete history.
CREATE TABLE evaluations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assignment_id   uuid NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    stage_id        text NOT NULL,
    session_id      uuid REFERENCES sessions(id) ON DELETE SET NULL,
    rubric_id       uuid NOT NULL REFERENCES rubrics(id) ON DELETE RESTRICT,
    evaluator_kind  text NOT NULL DEFAULT 'ai'
                        CHECK (evaluator_kind IN ('ai', 'human')),
    evaluator_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    ai_profile_id   uuid REFERENCES ai_profiles(id) ON DELETE SET NULL,
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'running', 'completed',
                                          'failed', 'superseded')),
    -- computed in application code from criterion_scores, never by a model
    weighted_total  numeric(7,3),
    max_total       numeric(7,3),
    prompt_version  text NOT NULL DEFAULT '',
    model_version   text NOT NULL DEFAULT '',
    supersedes_id   uuid REFERENCES evaluations(id) ON DELETE SET NULL,
    -- components that were unavailable; surfaced as caveats in the report
    caveats         text[] NOT NULL DEFAULT '{}',
    failure_reason  text,
    started_at      timestamptz,
    completed_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX evaluations_assignment_idx ON evaluations (assignment_id, stage_id);
CREATE INDEX evaluations_org_status_idx ON evaluations (organization_id, status);
CREATE UNIQUE INDEX evaluations_current_uniq
    ON evaluations (assignment_id, stage_id, evaluator_kind)
    WHERE status = 'completed' AND supersedes_id IS NULL;

CREATE TABLE criterion_scores (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    evaluation_id   uuid NOT NULL REFERENCES evaluations(id) ON DELETE CASCADE,
    criterion_id    uuid NOT NULL REFERENCES rubric_criteria(id) ON DELETE RESTRICT,
    score           numeric(6,2) NOT NULL,
    max_score       numeric(6,2) NOT NULL,
    reasoning       text NOT NULL DEFAULT '',
    -- set when a teacher overrides the AI score
    overridden_score numeric(6,2),
    overridden_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    override_reason text,
    overridden_at   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (score >= 0 AND score <= max_score),
    CHECK (overridden_score IS NULL OR
           (overridden_score >= 0 AND overridden_score <= max_score))
);

CREATE UNIQUE INDEX criterion_scores_eval_criterion_uniq
    ON criterion_scores (evaluation_id, criterion_id);

-- A span with verified = false never reaches a student. See docs/ai-harness.md.
CREATE TABLE evidence_spans (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    criterion_score_id  uuid NOT NULL REFERENCES criterion_scores(id) ON DELETE CASCADE,
    source_kind         text NOT NULL
                            CHECK (source_kind IN ('submission', 'transcript',
                                                   'authority', 'problem', 'other')),
    source_ref          uuid,
    locator             text NOT NULL DEFAULT '',
    quote               text NOT NULL,
    -- verified against the source text: exact match, then fuzzy above 0.9
    verified            boolean NOT NULL DEFAULT false,
    match_score         real,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX evidence_spans_score_idx ON evidence_spans (criterion_score_id);
CREATE INDEX evidence_spans_unverified_idx ON evidence_spans (organization_id)
    WHERE NOT verified;

CREATE TABLE reports (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assignment_id   uuid NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    version         int NOT NULL DEFAULT 1,
    status          text NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'awaiting_review', 'published')),
    -- composed structure: overall, per-criterion, strengths, weaknesses,
    -- questions handled, missed arguments, recommendations
    content         jsonb NOT NULL DEFAULT '{}'::jsonb,
    overall_score   numeric(7,3),
    max_score       numeric(7,3),
    teacher_notes   text,
    reviewed_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at     timestamptz,
    published_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX reports_assignment_version_uniq ON reports (assignment_id, version);
CREATE INDEX reports_org_status_idx ON reports (organization_id, status);
CREATE TRIGGER reports_updated_at BEFORE UPDATE ON reports
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Model accounting. Also the billing source, which is why it lives in Postgres
-- rather than only in metrics.
CREATE TABLE ai_requests (
    id              bigserial PRIMARY KEY,
    organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    session_id      uuid REFERENCES sessions(id) ON DELETE SET NULL,
    assignment_id   uuid REFERENCES assignments(id) ON DELETE SET NULL,
    trace_id        text NOT NULL DEFAULT '',
    purpose         text NOT NULL,          -- 'monitor_bid', 'judge_question', 'grade_criterion'
    provider        text NOT NULL,
    model           text NOT NULL,
    model_tier      text NOT NULL DEFAULT '',
    prompt_version  text NOT NULL DEFAULT '',
    input_tokens    int NOT NULL DEFAULT 0,
    output_tokens   int NOT NULL DEFAULT 0,
    audio_seconds   real NOT NULL DEFAULT 0,
    latency_ms      int NOT NULL DEFAULT 0,
    cost_micros     bigint NOT NULL DEFAULT 0,
    status          text NOT NULL DEFAULT 'ok'
                        CHECK (status IN ('ok', 'timeout', 'schema_rejected',
                                          'provider_error', 'cancelled')),
    error           text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ai_requests_org_created_idx ON ai_requests (organization_id, created_at DESC);
CREATE INDEX ai_requests_session_idx     ON ai_requests (session_id);
CREATE INDEX ai_requests_purpose_idx     ON ai_requests (purpose, created_at DESC);

-- Durable timers. Never an in-process timer, which dies with the process.
CREATE TABLE scheduled_transitions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    subject_kind    text NOT NULL
                        CHECK (subject_kind IN ('assignment', 'assignment_stage',
                                                'session', 'assessment')),
    subject_id      uuid NOT NULL,
    target_state    text NOT NULL,
    cause           text NOT NULL,
    -- makes a transition idempotent across retries and duplicate schedulers
    cause_key       text NOT NULL,
    run_at          timestamptz NOT NULL,
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'running', 'done',
                                          'failed', 'cancelled')),
    attempts        int NOT NULL DEFAULT 0,
    last_error      text,
    locked_by       text,
    locked_at       timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX scheduled_transitions_idempotency_uniq
    ON scheduled_transitions (subject_kind, subject_id, cause_key);
CREATE INDEX scheduled_transitions_due_idx ON scheduled_transitions (run_at)
    WHERE status = 'pending';
CREATE TRIGGER scheduled_transitions_updated_at BEFORE UPDATE ON scheduled_transitions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Audio is plausibly special-category data. Consent is recorded, not assumed.
CREATE TABLE consent_records (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope           text NOT NULL
                        CHECK (scope IN ('audio_recording', 'transcript_retention',
                                         'ai_evaluation', 'terms')),
    granted         boolean NOT NULL,
    version         text NOT NULL DEFAULT 'v1',
    ip              inet,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX consent_records_user_idx ON consent_records (user_id, scope, created_at DESC);
