package sessions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slmlabs/megamoot/apps/api/internal/harness"
)

type bankQuestions struct {
	Questions []string `json:"questions"`
}

func (b *bankQuestions) Validate() error {
	if len(b.Questions) < 3 || len(b.Questions) > 12 {
		return fmt.Errorf("return 3 to 12 questions")
	}
	for _, q := range b.Questions {
		if strings.TrimSpace(q) == "" || len(q) > 600 {
			return fmt.Errorf("invalid question length")
		}
	}
	return nil
}

func RunQuestionBank(ctx context.Context, pool *pgxpool.Pool, h *harness.Harness, log *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := prepareBank(ctx, pool, h); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("prepare question bank", "error", err)
		}
	}
}

type bankJob struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	AssessmentID   uuid.UUID
	AssignmentID   *uuid.UUID
	Side           string
	Attempts       int
}

func prepareBank(ctx context.Context, pool *pgxpool.Pool, h *harness.Harness) (err error) {
	// The whole job must outlast one grader call, whose own budget is the
	// grader binding's timeout_ms. A large model on a shared box answers in
	// minutes, not seconds.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(672043)`).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT pg_advisory_unlock(672043)`); err != nil {
			_ = conn.Conn().Close(cleanup)
		}
	}()
	var live bool
	if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE status IN ('running','paused','degraded'))`).Scan(&live); err != nil || live {
		return err
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO question_bank_jobs
			(organization_id, assessment_id, assignment_id, side)
		SELECT organization_id, assessment_id, assignment_id, side FROM (
			SELECT a.organization_id, a.id assessment_id, NULL::uuid assignment_id,
			       ''::text side, a.created_at
			FROM assessments a
			WHERE a.status = 'published'
			  AND NOT EXISTS (
				SELECT 1 FROM question_bank_items q
				WHERE q.organization_id = a.organization_id
				  AND q.assessment_id = a.id AND q.assignment_id IS NULL)
			UNION ALL
			SELECT a.organization_id, a.assessment_id, a.id, a.side, a.assigned_at
			FROM assignments a
			WHERE EXISTS (
				SELECT 1 FROM evaluations e
				WHERE e.assignment_id = a.id AND e.organization_id = a.organization_id
				  AND e.status = 'completed')
			  AND NOT EXISTS (
				SELECT 1 FROM question_bank_items q
				WHERE q.organization_id = a.organization_id
				  AND q.assignment_id = a.id)
		) work
		ON CONFLICT (assessment_id, assignment_id) DO UPDATE
		SET status = 'pending', run_at = now(), last_error = NULL
		WHERE question_bank_jobs.status = 'done'`); err != nil {
		return err
	}

	var job bankJob
	err = conn.QueryRow(ctx, `
		WITH next AS (
			SELECT id FROM question_bank_jobs
			WHERE (status IN ('pending','failed') AND run_at <= now())
			   OR (status = 'running' AND updated_at < now() - interval '5 minutes')
			ORDER BY run_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE question_bank_jobs j
		SET status = 'running', attempts = j.attempts + 1, last_error = NULL
		FROM next WHERE j.id = next.id
		RETURNING j.id, j.organization_id, j.assessment_id, j.assignment_id,
		          j.side, j.attempts`).
		Scan(&job.ID, &job.OrganizationID, &job.AssessmentID, &job.AssignmentID,
			&job.Side, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		if err == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, updateErr := conn.Exec(cleanup, `
			UPDATE question_bank_jobs
			SET status = 'failed', run_at = now() + $2::interval, last_error = $3
			WHERE id = $1`, job.ID, bankRetryDelay(job.Attempts).String(), err.Error())
		if updateErr != nil {
			err = errors.Join(err, fmt.Errorf("record question bank failure: %w", updateErr))
		}
	}()

	var materials string
	err = conn.QueryRow(ctx, `SELECT coalesce(left(string_agg(body,E'\n'),48000),'') FROM (
 SELECT k.title||E'\n'||d.extracted_text body FROM knowledge_sources k JOIN documents d ON d.knowledge_source_id=k.id AND d.organization_id=k.organization_id WHERE k.organization_id=$1 AND k.assessment_id=$2 AND d.parse_status='parsed' AND k.visibility<>'staff' AND (k.visibility='all' OR ($3::uuid IS NOT NULL AND k.visibility=$4))
 UNION ALL SELECT d.extracted_text FROM artifacts ar JOIN documents d ON d.id=ar.document_id AND d.organization_id=ar.organization_id WHERE ar.organization_id=$1 AND ar.assignment_id=$3 AND d.parse_status='parsed'
 ) sources`, job.OrganizationID, job.AssessmentID, job.AssignmentID, job.Side).Scan(&materials)
	if err != nil {
		return err
	}
	out := bankQuestions{Questions: fallbackQuestions}
	if strings.TrimSpace(materials) != "" {
		err = h.Structured(ctx, harness.Call{Tier: "grader", Purpose: "prepare_questions", PromptVersion: "bank.v1", MaxTokens: 1400, System: "Prepare 8 short examiner questions grounded in the supplied assessment materials. Ask about reasoning, evidence and counterarguments. Do not invent facts or authorities or reveal scores. Return JSON {\"questions\":[\"...\"]}. Each question at most 30 words.", Untrusted: []harness.Untrusted{{Label: "materials", Text: materials}}, User: "Candidate side: " + job.Side, OrganizationID: &job.OrganizationID, AssignmentID: job.AssignmentID}, &out)
		if err != nil {
			return err
		}
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	for _, q := range out.Questions {
		if _, err = tx.Exec(ctx, `INSERT INTO question_bank_items(organization_id,assessment_id,assignment_id,side,text,generated_by) VALUES($1,$2,$3,nullif($4,''),$5,'bank.v1')`, job.OrganizationID, job.AssessmentID, job.AssignmentID, job.Side, strings.TrimSpace(q)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE question_bank_jobs SET status='done', last_error=NULL WHERE id=$1`, job.ID); err != nil {
		return err
	}
	err = tx.Commit(ctx)
	return err
}

func bankRetryDelay(attempt int) time.Duration {
	delay := time.Minute << min(max(attempt-1, 0), 6)
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}
