-- What a student thought of an oral round, asked once it ends. It tunes the
-- judge; it never feeds a grade. One row per student per session, so a
-- correction replaces the earlier answer.
CREATE TABLE session_feedback (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- overall experience with the judge
    rating          smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    -- were the judge's questions about the student's case
    relevance       text NOT NULL CHECK (relevance IN ('yes', 'partly', 'no')),
    interruptions   text NOT NULL
                        CHECK (interruptions IN ('about_right', 'too_many', 'too_few')),
    problems        text[] NOT NULL DEFAULT '{}'
                        CHECK (problems <@ ARRAY['audio', 'transcript', 'judge_silent',
                                                 'judge_repeated', 'other']::text[]),
    comment         text NOT NULL DEFAULT '' CHECK (length(comment) <= 2000),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, user_id)
);

CREATE INDEX session_feedback_org_created_idx
    ON session_feedback (organization_id, created_at DESC);
