-- Live sessions. session_events is append-only and is the source of truth;
-- transcript_segments and assertions are projections of it.

CREATE TABLE sessions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assignment_id   uuid NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    stage_id        text NOT NULL,
    status          text NOT NULL DEFAULT 'scheduled'
                        CHECK (status IN ('scheduled', 'lobby', 'device_check',
                                          'running', 'paused', 'degraded',
                                          'ended', 'aborted', 'evaluating',
                                          'evaluated')),
    scheduled_at    timestamptz NOT NULL,
    -- how long after scheduled_at the student may still join
    join_window_s   int NOT NULL DEFAULT 900 CHECK (join_window_s >= 0),
    started_at      timestamptz,
    ended_at        timestamptz,
    -- which process currently owns this session (single-writer actor)
    owner_node      text,
    owner_heartbeat_at timestamptz,
    -- cumulative pause time allowed before the session aborts
    pause_budget_s  int NOT NULL DEFAULT 300 CHECK (pause_budget_s >= 0),
    paused_total_s  int NOT NULL DEFAULT 0,
    -- components that failed; drives the DEGRADED state and report caveats
    degraded_components text[] NOT NULL DEFAULT '{}',
    last_seq        bigint NOT NULL DEFAULT 0,
    recording_key   text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_org_status_idx    ON sessions (organization_id, status);
CREATE INDEX sessions_scheduled_idx     ON sessions (scheduled_at)
    WHERE status IN ('scheduled', 'lobby');
CREATE INDEX sessions_assignment_idx    ON sessions (assignment_id);
CREATE INDEX sessions_owner_idx         ON sessions (owner_node)
    WHERE status IN ('running', 'paused', 'degraded');
CREATE TRIGGER sessions_updated_at BEFORE UPDATE ON sessions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One table for human and AI participants. Student-versus-student in V4 is two
-- rows with kind = 'human'. See ADR 0004 and docs/domain-model.md.
CREATE TABLE session_participants (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind            text NOT NULL CHECK (kind IN ('human', 'ai')),
    role            text NOT NULL
                        CHECK (role IN ('candidate', 'judge', 'examiner',
                                        'opponent', 'observer', 'moderator')),
    -- exactly one of user_id / ai_profile_id, enforced below
    user_id         uuid REFERENCES users(id) ON DELETE SET NULL,
    ai_profile_id   uuid REFERENCES ai_profiles(id) ON DELETE RESTRICT,
    display_name    text NOT NULL,
    side            text,
    speaking_order  int,
    -- presiding judge arbitrates; exactly one per session when judges exist
    is_presiding    boolean NOT NULL DEFAULT false,
    joined_at       timestamptz,
    left_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT session_participants_identity CHECK (
        (kind = 'human' AND user_id IS NOT NULL AND ai_profile_id IS NULL) OR
        (kind = 'ai'    AND ai_profile_id IS NOT NULL AND user_id IS NULL)
    )
);

CREATE INDEX session_participants_session_idx ON session_participants (session_id, role);
CREATE INDEX session_participants_user_idx    ON session_participants (user_id);
CREATE UNIQUE INDEX session_participants_presiding_uniq
    ON session_participants (session_id) WHERE is_presiding;

-- Append-only. Never updated, never deleted by application code. The audit
-- log, the replay source, the reconnect delta and the analytics source.
CREATE TABLE session_events (
    session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq             bigint NOT NULL,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    type            text NOT NULL,
    actor_kind      text NOT NULL DEFAULT 'system'
                        CHECK (actor_kind IN ('human', 'ai', 'system')),
    actor_id        uuid,
    payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- milliseconds since session start, for replay alignment
    offset_ms       bigint,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, seq)
);

CREATE INDEX session_events_type_idx ON session_events (session_id, type, seq);
CREATE INDEX session_events_org_created_idx ON session_events (organization_id, created_at DESC);

-- Projection of transcript_final events.
CREATE TABLE transcript_segments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    participant_id  uuid REFERENCES session_participants(id) ON DELETE SET NULL,
    seq             bigint NOT NULL,
    start_ms        bigint NOT NULL,
    end_ms          bigint NOT NULL,
    text            text NOT NULL,
    confidence      real,
    is_final        boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (end_ms >= start_ms)
);

CREATE UNIQUE INDEX transcript_segments_session_seq_uniq ON transcript_segments (session_id, seq);
CREATE INDEX transcript_segments_session_time_idx ON transcript_segments (session_id, start_ms);

-- The assertion ledger. Backbone of interruption, contradiction detection and
-- evaluation. Without it, detecting a contradiction means re-reading the whole
-- transcript with an LLM every few seconds.
CREATE TABLE assertions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    participant_id  uuid REFERENCES session_participants(id) ON DELETE SET NULL,
    ref             text NOT NULL,          -- short handle used in prompts: 'a_17'
    text            text NOT NULL,
    claim_type      text NOT NULL DEFAULT 'argument'
                        CHECK (claim_type IN ('argument', 'fact', 'legal_proposition',
                                              'authority_citation', 'concession',
                                              'prayer', 'other')),
    cited_authority text,
    support_status  text NOT NULL DEFAULT 'unsupported'
                        CHECK (support_status IN ('supported', 'unsupported',
                                                  'disputed', 'conceded', 'unknown')),
    contradicts     uuid[] NOT NULL DEFAULT '{}',
    start_ms        bigint,
    end_ms          bigint,
    embedding       vector(1024),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX assertions_session_ref_uniq ON assertions (session_id, ref);
CREATE INDEX assertions_session_idx ON assertions (session_id, created_at);
CREATE INDEX assertions_embedding_idx ON assertions USING hnsw (embedding vector_cosine_ops);
CREATE TRIGGER assertions_updated_at BEFORE UPDATE ON assertions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Every bid, approved or dropped, with the reason. Approved bids become the
-- judge turns the student actually hears.
CREATE TABLE judge_actions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    participant_id  uuid NOT NULL REFERENCES session_participants(id) ON DELETE CASCADE,
    action          text NOT NULL
                        CHECK (action IN ('interrupt', 'ask_question', 'follow_up',
                                          'challenge_authority', 'ask_hypothetical',
                                          'request_clarification', 'continue', 'note')),
    priority        real NOT NULL DEFAULT 0 CHECK (priority >= 0 AND priority <= 1),
    reason          text NOT NULL DEFAULT '',
    claim_ref       text,
    question_text   text,
    source          text NOT NULL DEFAULT 'bank'
                        CHECK (source IN ('bank', 'generated', 'tier0', 'human')),
    question_bank_item_id uuid,
    outcome         text NOT NULL DEFAULT 'dropped'
                        CHECK (outcome IN ('approved', 'dropped', 'superseded', 'failed')),
    outcome_reason  text NOT NULL DEFAULT '',
    decided_at      timestamptz NOT NULL DEFAULT now(),
    spoken_at       timestamptz,
    latency_ms      int
);

CREATE INDEX judge_actions_session_idx ON judge_actions (session_id, decided_at);
CREATE INDEX judge_actions_outcome_idx ON judge_actions (session_id, outcome);

-- Questions precomputed at publish time and after each submission is graded.
-- The live hot path retrieves from here rather than generating. See ADR 0006.
CREATE TABLE question_bank_items (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assessment_id   uuid NOT NULL REFERENCES assessments(id) ON DELETE CASCADE,
    -- NULL means a general question for the whole assessment and side
    assignment_id   uuid REFERENCES assignments(id) ON DELETE CASCADE,
    side            text,
    text            text NOT NULL,
    category        text NOT NULL DEFAULT 'general',
    difficulty      real NOT NULL DEFAULT 0.5 CHECK (difficulty >= 0 AND difficulty <= 1),
    targets_claim_type text,
    source_refs     jsonb NOT NULL DEFAULT '[]'::jsonb,
    expected_points text NOT NULL DEFAULT '',
    embedding       vector(1024),
    generated_by    text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX question_bank_assessment_idx ON question_bank_items (assessment_id, side, category);
CREATE INDEX question_bank_assignment_idx ON question_bank_items (assignment_id);
CREATE INDEX question_bank_embedding_idx  ON question_bank_items
    USING hnsw (embedding vector_cosine_ops);

-- Admission control. The GPU supports a handful of concurrent sessions; the
-- sixth queues in the waiting room instead of degrading the other five.
CREATE TABLE session_slots (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    session_id      uuid NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE CASCADE,
    status          text NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'held', 'released')),
    queued_at       timestamptz NOT NULL DEFAULT now(),
    held_at         timestamptz,
    released_at     timestamptz,
    node            text
);

CREATE INDEX session_slots_queue_idx ON session_slots (organization_id, status, queued_at);
