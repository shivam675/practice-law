-- Durable generation status for general and assignment-specific question banks.
CREATE TABLE question_bank_jobs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assessment_id   uuid NOT NULL REFERENCES assessments(id) ON DELETE CASCADE,
    assignment_id   uuid REFERENCES assignments(id) ON DELETE CASCADE,
    side            text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'running', 'failed', 'done')),
    attempts        int NOT NULL DEFAULT 0,
    run_at          timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (assessment_id, assignment_id)
);

CREATE INDEX question_bank_jobs_due_idx ON question_bank_jobs (run_at)
    WHERE status IN ('pending', 'failed');
CREATE TRIGGER question_bank_jobs_updated_at BEFORE UPDATE ON question_bank_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
