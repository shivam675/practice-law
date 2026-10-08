package sessions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/intelimek/megamoot/apps/api/internal/harness"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

// Database absence is the durable work queue; a restart simply tries again.
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

func prepareBank(ctx context.Context, pool *pgxpool.Pool, h *harness.Harness) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
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
	var org, assessment uuid.UUID
	var assignment *uuid.UUID
	var side string
	err = conn.QueryRow(ctx, `SELECT organization_id,assessment_id,assignment_id,side FROM (
 SELECT a.organization_id,a.id assessment_id,NULL::uuid assignment_id,''::text side,a.created_at FROM assessments a
 WHERE a.status='published' AND NOT EXISTS(SELECT 1 FROM question_bank_items q WHERE q.organization_id=a.organization_id AND q.assessment_id=a.id AND q.assignment_id IS NULL)
 UNION ALL
 SELECT a.organization_id,a.assessment_id,a.id,a.side,a.assigned_at FROM assignments a
 WHERE EXISTS(SELECT 1 FROM evaluations e WHERE e.assignment_id=a.id AND e.organization_id=a.organization_id AND e.status='completed')
 AND NOT EXISTS(SELECT 1 FROM question_bank_items q WHERE q.organization_id=a.organization_id AND q.assignment_id=a.id)
 ) work ORDER BY created_at LIMIT 1`).Scan(&org, &assessment, &assignment, &side)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var materials string
	err = conn.QueryRow(ctx, `SELECT coalesce(left(string_agg(body,E'\n'),48000),'') FROM (
 SELECT k.title||E'\n'||d.extracted_text body FROM knowledge_sources k JOIN documents d ON d.knowledge_source_id=k.id AND d.organization_id=k.organization_id WHERE k.organization_id=$1 AND k.assessment_id=$2 AND d.parse_status='parsed' AND k.visibility<>'staff' AND (k.visibility='all' OR ($3::uuid IS NOT NULL AND k.visibility=$4))
 UNION ALL SELECT d.extracted_text FROM artifacts ar JOIN documents d ON d.id=ar.document_id AND d.organization_id=ar.organization_id WHERE ar.organization_id=$1 AND ar.assignment_id=$3 AND d.parse_status='parsed'
 ) sources`, org, assessment, assignment, side).Scan(&materials)
	if err != nil {
		return err
	}
	out := bankQuestions{Questions: fallbackQuestions}
	if strings.TrimSpace(materials) != "" {
		err = h.Structured(ctx, harness.Call{Tier: "judge", Purpose: "prepare_questions", PromptVersion: "bank.v1", MaxTokens: 1400, Timeout: 75 * time.Second, System: "Prepare 8 short examiner questions grounded in the supplied assessment materials. Ask about reasoning, evidence and counterarguments. Do not invent facts or authorities or reveal scores. Return JSON {\"questions\":[\"...\"]}. Each question at most 30 words.", Untrusted: []harness.Untrusted{{Label: "materials", Text: materials}}, User: "Candidate side: " + side, OrganizationID: &org, AssignmentID: assignment}, &out)
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
		if _, err = tx.Exec(ctx, `INSERT INTO question_bank_items(organization_id,assessment_id,assignment_id,side,text,generated_by) VALUES($1,$2,$3,nullif($4,''),$5,'bank.v1')`, org, assessment, assignment, side, strings.TrimSpace(q)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
