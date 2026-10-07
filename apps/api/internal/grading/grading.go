// Package grading scores a submitted artifact against a rubric.
//
// One model call per criterion, not one per document. A single call asked to
// produce eight scores produces eight correlated scores: it anchors on its own
// first judgement and the rest follow. Separate calls also mean a disputed
// criterion is re-gradable on its own, and a criterion whose model call fails
// does not cost the other seven.
//
// The model never emits a total and never decides anything. It returns a
// per-criterion score with quoted evidence; this package checks the bounds
// against the rubric row, verifies every quote against the source text, and
// computes the weighted total in Go.
package grading

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/harness"
	"github.com/intelimek/megamoot/apps/api/internal/llm"
	"github.com/intelimek/megamoot/apps/api/internal/retrieval"
	"github.com/intelimek/megamoot/apps/api/internal/rubrics"
)

// PromptVersion is stamped on every evaluation and every ledger row. Bump it
// whenever the wording below changes, so a disputed grade is reproducible and
// a prompt change can drive a targeted re-grade.
const PromptVersion = "grade.criterion.v1"

// maxInlineChars is how much submission text goes into a prompt whole. Above
// it, the criterion's own retrieval decides what the model sees; a memorial
// that long would otherwise crowd out the rubric guidance.
const maxInlineChars = 48000

// contextChunks is how many passages of the problem and the authorities are
// retrieved per criterion. Enough to ground a citation claim, not so many
// that the submission stops being the thing under examination.
const contextChunks = 6

type Store struct {
	pool      *pgxpool.Pool
	harness   *harness.Harness
	retrieval *retrieval.Store
	rubrics   *rubrics.Store
	log       *slog.Logger
}

func NewStore(pool *pgxpool.Pool, h *harness.Harness, r *retrieval.Store,
	rb *rubrics.Store, log *slog.Logger) *Store {

	return &Store{pool: pool, harness: h, retrieval: r, rubrics: rb, log: log}
}

/* ------------------------------------------------------- the model contract */

type evidenceItem struct {
	SourceKind string `json:"source_kind"`
	Locator    string `json:"locator"`
	Quote      string `json:"quote"`
}

// criterionResult is the only shape a grading call may return. maxScore is
// not part of the JSON: it comes from the rubric row, so a model cannot raise
// its own ceiling by claiming a different denominator.
type criterionResult struct {
	Score     float64        `json:"score"`
	Reasoning string         `json:"reasoning"`
	Evidence  []evidenceItem `json:"evidence"`

	maxScore float64
}

// Validate runs inside the structured gate, so an impossible score costs one
// repair round rather than reaching the database and failing a CHECK.
func (r *criterionResult) Validate() error {
	if math.IsNaN(r.Score) || math.IsInf(r.Score, 0) || r.Score < 0 || r.Score > r.maxScore {
		return fmt.Errorf("score %.2f is outside 0..%.2f", r.Score, r.maxScore)
	}
	if strings.TrimSpace(r.Reasoning) == "" {
		return errors.New("reasoning is required")
	}
	for i, e := range r.Evidence {
		if strings.TrimSpace(e.Quote) == "" {
			return fmt.Errorf("evidence %d has no quote", i)
		}
	}
	return nil
}

var allowedSourceKinds = map[string]struct{}{
	"submission": {}, "transcript": {}, "authority": {},
	"problem": {}, "other": {},
}

/* ------------------------------------------------------------------- input */

// Target names what is being graded. Everything else is read from the
// database, because a caller that can choose the rubric can choose a lenient
// one.
type Target struct {
	OrganizationID uuid.UUID
	AssignmentID   uuid.UUID
	StageID        string
	// RubricScope limits the run to these criterion keys, which is what the
	// stage spec's automated_evaluation config carries. Empty means all of
	// them, and an unknown key is ignored rather than fatal: a rubric edit
	// must not wedge every assignment using it.
	RubricScope []string
	Sources     []string
	TraceID     string
}

// Result is what the caller needs to decide the next transition. Caveats name
// what was not available, and the report repeats them to the student.
type Result struct {
	EvaluationID  uuid.UUID
	WeightedTotal float64
	MaxTotal      float64
	Scored        int
	Caveats       []string
}

/* ------------------------------------------------------------------- grade */

// GradeSubmission scores the artifact submitted for a stage.
//
// A criterion whose model call fails is recorded as a caveat and dropped from
// both the total and the maximum, so the percentage reflects what was actually
// assessed. Inventing a zero would punish a student for a provider outage, and
// failing the whole evaluation would hold up every other criterion.
func (s *Store) GradeSubmission(ctx context.Context, t Target) (result Result, resultErr error) {
	sub, err := s.loadSubmission(ctx, t)
	if err != nil {
		return Result{}, err
	}

	rubric, err := s.loadRubric(ctx, t.OrganizationID, sub.RubricID)
	if err != nil {
		return Result{}, err
	}
	criteria := inScope(rubric.Criteria, t.RubricScope)
	if len(criteria) == 0 {
		return Result{}, fmt.Errorf("no rubric criteria in scope for stage %q", t.StageID)
	}

	evaluationID, err := s.openEvaluation(ctx, t, sub.RubricID)
	if err != nil {
		return Result{}, err
	}

	defer func() {
		if resultErr != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := s.failEvaluation(cleanup, evaluationID, reason(resultErr)); err != nil {
				s.log.Error("record failed evaluation", "error", err)
			}
		}
	}()
	sourceText := sub.Text

	res := Result{EvaluationID: evaluationID, Caveats: []string{}}
	for _, c := range criteria {
		scored, err := s.gradeOne(ctx, t, sub, c, sourceText)
		if err != nil {
			s.log.Warn("grading: criterion not scored",
				"assignment_id", t.AssignmentID, "criterion", c.Key, "error", err)
			res.Caveats = append(res.Caveats,
				fmt.Sprintf("%s was not scored automatically: %s", c.Name, reason(err)))
			continue
		}

		if err := s.persistCriterion(ctx, t, evaluationID, sub, c, scored, sourceText); err != nil {
			return res, err
		}
		res.Scored++
		res.WeightedTotal += c.Weight * (scored.Score / c.MaxScore)
		res.MaxTotal += c.Weight
	}

	if res.Scored != len(criteria) {
		return res, fmt.Errorf("grade assignment %s stage %s: only %d of %d criteria scored",
			t.AssignmentID, t.StageID, res.Scored, len(criteria))
	}

	if err := s.closeEvaluation(ctx, evaluationID, res); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Store) gradeOne(ctx context.Context, t Target, sub submission,
	c rubrics.Criterion, sourceText string) (*criterionResult, error) {

	blocks := []harness.Untrusted{{
		Label: "assessment_record",
		Text:  clip(sourceText, maxInlineChars),
	}}

	// Case materials ground a claim about authority. They are teacher-supplied
	// and still arrive as an untrusted block: the cost is one tag, and the
	// alternative is a trust boundary that depends on who uploaded a PDF.
	if materials := s.caseMaterials(ctx, t, sub, c); materials != "" {
		blocks = append(blocks, harness.Untrusted{Label: "case_materials", Text: materials})
	}

	out := &criterionResult{maxScore: c.MaxScore}
	err := s.harness.Structured(ctx, harness.Call{
		Tier:           "grader",
		Purpose:        "grade_criterion",
		PromptVersion:  PromptVersion,
		System:         systemPrompt,
		Untrusted:      blocks,
		User:           criterionPrompt(c),
		OrganizationID: &t.OrganizationID,
		AssignmentID:   &t.AssignmentID,
		TraceID:        t.TraceID,
	}, out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// caseMaterials retrieves the passages of the problem and the authorities that
// bear on this criterion. An empty string is a normal answer: a document that
// has not been indexed yet must delay nothing.
func (s *Store) caseMaterials(ctx context.Context, t Target, sub submission,
	c rubrics.Criterion) string {

	hits, err := s.retrieval.Search(ctx, retrieval.Query{
		OrganizationID: t.OrganizationID,
		AssessmentID:   &sub.AssessmentID,
		SourceKinds:    []string{"problem", "authority", "statute"},
		Text:           c.Name + ". " + c.Description + " " + c.Guidance,
		Limit:          contextChunks,
	})
	if err != nil {
		s.log.Warn("grading: case materials unavailable",
			"criterion", c.Key, "error", err)
		return ""
	}

	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "[%s]\n%s\n\n", h.Locator, h.Content)
	}
	return strings.TrimSpace(b.String())
}

/* ----------------------------------------------------------------- prompts */

const systemPrompt = `You are an assessment evaluator marking one ` +
	`rubric criterion against the supplied written work or oral transcript.

Mark what is on the page. Reward argument that is supported by authority and ` +
	`anchored in the record; penalise assertion without support, authority ` +
	`cited for a proposition it does not establish, and reasoning that does ` +
	`not engage the other side's best point.

Every judgement you make must be anchored to a quotation you copied verbatim ` +
	`from the material given to you. A quotation that does not appear in that ` +
	`material is checked and discarded, and the student never sees it, so an ` +
	`approximate quote wastes the only evidence you had.

Reply with one JSON object and nothing else:

{
  "score": <number, 0 to the maximum stated in the request>,
  "reasoning": "<two to four sentences addressed to the student, saying what ` +
	`earned the mark and what would have earned more>",
  "evidence": [
    {"source_kind": "submission" | "transcript" | "problem" | "authority",
     "locator": "<the heading, section or transcript turn the quote sits under>",
     "quote": "<copied verbatim, one or two sentences>"}
  ]
}

Between one and three pieces of evidence. No prose outside the object.`

func criterionPrompt(c rubrics.Criterion) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Mark this criterion.\n\nCriterion: %s\nMaximum score: %g\n",
		c.Name, c.MaxScore)
	if c.Description != "" {
		fmt.Fprintf(&b, "\nWhat it measures:\n%s\n", c.Description)
	}
	if c.Guidance != "" {
		fmt.Fprintf(&b, "\nMarking guidance:\n%s\n", c.Guidance)
	}
	fmt.Fprintf(&b, "\nReturn the JSON object. The score must be between 0 and %g.", c.MaxScore)
	return b.String()
}

/* ------------------------------------------------------------- persistence */

type submission struct {
	AssessmentID uuid.UUID
	DocumentID   uuid.UUID
	RubricID     uuid.UUID
	Text         string
}

func (s *Store) loadSubmission(ctx context.Context, t Target) (submission, error) {
	var sub submission
	err := s.pool.QueryRow(ctx, `
		SELECT a.assessment_id, tv.rubric_id
		FROM assignments a
		JOIN assessments ass     ON ass.id = a.assessment_id
		JOIN assessment_template_versions tv ON tv.id = ass.template_version_id
		WHERE a.id = $1 AND a.organization_id = $2`, t.AssignmentID, t.OrganizationID).
		Scan(&sub.AssessmentID, &sub.RubricID)
	if errors.Is(err, pgx.ErrNoRows) {
		return submission{}, fmt.Errorf("no submission for assignment %s stage %q",
			t.AssignmentID, t.StageID)
	}
	if err != nil {
		return submission{}, fmt.Errorf("load submission: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		WITH sources AS (
		 SELECT st.stage_id FROM assignment_stages st
		 JOIN assignment_stages ev ON ev.assignment_id = st.assignment_id
		  AND ev.organization_id = st.organization_id AND ev.stage_id = $2
		 WHERE st.assignment_id = $1 AND st.organization_id = $3
		  AND st.sort_order < ev.sort_order
		  AND (cardinality($4::text[]) = 0 OR st.stage_id = ANY($4::text[]))
		), written AS (
		 SELECT DISTINCT ON (ar.stage_id) ar.stage_id, d.extracted_text AS body
		 FROM artifacts ar JOIN documents d ON d.id = ar.document_id AND d.organization_id = ar.organization_id
		 WHERE ar.assignment_id = $1 AND ar.organization_id = $3
		  AND ar.stage_id IN (SELECT stage_id FROM sources) AND d.parse_status = 'parsed'
		 ORDER BY ar.stage_id, ar.version DESC
		)
		SELECT stage_id, body FROM written
		UNION ALL
		SELECT se.stage_id, string_agg(coalesce(tr.payload->>'speaker', 'Participant') || ': ' || (tr.payload->>'text'), E'\n' ORDER BY tr.seq)
		FROM sessions se JOIN session_events tr ON tr.session_id = se.id AND tr.organization_id = se.organization_id
		WHERE se.assignment_id = $1 AND se.organization_id = $3
		 AND tr.type IN ('transcript_final','judge_question')
		 AND se.stage_id IN (SELECT stage_id FROM sources) AND se.status IN ('ended','evaluating','evaluated')
		GROUP BY se.id, se.stage_id`, t.AssignmentID, t.StageID, t.OrganizationID, nonNilSources(t.Sources))
	if err != nil {
		return sub, fmt.Errorf("load evaluation sources: %w", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var stage, body string
		if err := rows.Scan(&stage, &body); err != nil {
			return sub, err
		}
		if strings.TrimSpace(body) != "" {
			fmt.Fprintf(&b, "[%s]\n%s\n\n", stage, body)
		}
	}
	if err := rows.Err(); err != nil {
		return sub, err
	}
	sub.Text = b.String()
	if strings.TrimSpace(sub.Text) == "" {
		return sub, fmt.Errorf("no source material for evaluation stage %q", t.StageID)
	}
	return sub, nil
}

func nonNilSources(sources []string) []string {
	if sources == nil {
		return []string{}
	}
	return sources
}

func (s *Store) loadRubric(ctx context.Context, orgID, rubricID uuid.UUID) (rubrics.Rubric, error) {
	all, err := s.rubrics.List(ctx, orgID)
	if err != nil {
		return rubrics.Rubric{}, err
	}
	for _, r := range all {
		if r.ID == rubricID {
			return r, nil
		}
	}
	return rubrics.Rubric{}, fmt.Errorf("rubric %s not found in organisation %s", rubricID, orgID)
}

func inScope(all []rubrics.Criterion, scope []string) []rubrics.Criterion {
	if len(scope) == 0 {
		return all
	}
	want := make(map[string]struct{}, len(scope))
	for _, k := range scope {
		want[k] = struct{}{}
	}
	out := make([]rubrics.Criterion, 0, len(scope))
	for _, c := range all {
		if _, ok := want[c.Key]; ok {
			out = append(out, c)
		}
	}
	return out
}

// openEvaluation supersedes any completed evaluation for this stage rather
// than updating it. Evaluation rows are immutable: a re-grade must leave the
// original readable, because a student may already have seen it.
func (s *Store) openEvaluation(ctx context.Context, t Target, rubricID uuid.UUID) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin evaluation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var supersedes *uuid.UUID
	var prior uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM evaluations
		WHERE assignment_id = $1 AND stage_id = $2 AND evaluator_kind = 'ai'
		  AND status = 'completed' AND supersedes_id IS NULL`,
		t.AssignmentID, t.StageID).Scan(&prior)
	switch {
	case err == nil:
		supersedes = &prior
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return uuid.Nil, fmt.Errorf("find prior evaluation: %w", err)
	}

	// The partial unique index allows exactly one current completed AI
	// evaluation per stage, so the prior one is marked superseded before the
	// new one can claim the slot.
	if supersedes != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE evaluations SET status = 'superseded' WHERE id = $1`, prior); err != nil {
			return uuid.Nil, fmt.Errorf("supersede evaluation %s: %w", prior, err)
		}
	}

	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO evaluations
			(organization_id, assignment_id, stage_id, rubric_id, evaluator_kind,
			 status, prompt_version, supersedes_id, started_at)
		VALUES ($1,$2,$3,$4,'ai','running',$5,$6, now())
		RETURNING id`,
		t.OrganizationID, t.AssignmentID, t.StageID, rubricID,
		PromptVersion, supersedes).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("open evaluation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit open evaluation: %w", err)
	}
	return id, nil
}

func (s *Store) persistCriterion(ctx context.Context, t Target, evaluationID uuid.UUID,
	sub submission, c rubrics.Criterion, scored *criterionResult, sourceText string) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin persist criterion: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var scoreID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO criterion_scores
			(organization_id, evaluation_id, criterion_id, score, max_score, reasoning)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id`,
		t.OrganizationID, evaluationID, c.ID, scored.Score, c.MaxScore,
		strings.TrimSpace(scored.Reasoning)).Scan(&scoreID); err != nil {
		return fmt.Errorf("insert criterion score %s: %w", c.Key, err)
	}

	for _, e := range scored.Evidence {
		kind := strings.ToLower(strings.TrimSpace(e.SourceKind))
		if _, ok := allowedSourceKinds[kind]; !ok {
			kind = "other"
		}

		// Only a quote claimed to come from the submission can be checked
		// against the submission. A quote attributed to an authority is
		// verified against the retrieved passages of that authority instead.
		against := sourceText
		var sourceRef *uuid.UUID
		if kind == "submission" || kind == "transcript" {
			// Composite records retain their stage locator instead of inventing
			// a document ID for a transcript or several source documents.
		} else {
			against = s.materialFor(ctx, t, sub, e.Quote)
		}

		match := Verify(e.Quote, against)
		verified := match >= verifyThreshold
		if !verified {
			s.log.Warn("grading: evidence quote did not verify",
				"assignment_id", t.AssignmentID, "criterion", c.Key,
				"source_kind", kind, "match", match)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO evidence_spans
				(organization_id, criterion_score_id, source_kind, source_ref,
				 locator, quote, verified, match_score)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			t.OrganizationID, scoreID, kind, sourceRef,
			strings.TrimSpace(e.Locator), strings.TrimSpace(e.Quote),
			verified, match); err != nil {
			return fmt.Errorf("insert evidence span: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit criterion %s: %w", c.Key, err)
	}
	return nil
}

// materialFor fetches the case-material passages nearest a quote, which is
// what that quote is then verified against. Searching by the quote itself is
// the cheapest way to find the passage it was supposed to come from.
func (s *Store) materialFor(ctx context.Context, t Target, sub submission, quote string) string {
	hits, err := s.retrieval.Search(ctx, retrieval.Query{
		OrganizationID: t.OrganizationID,
		AssessmentID:   &sub.AssessmentID,
		SourceKinds:    []string{"problem", "authority", "statute"},
		Text:           quote,
		Limit:          3,
	})
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, h := range hits {
		b.WriteString(h.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}

func (s *Store) closeEvaluation(ctx context.Context, id uuid.UUID, res Result) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE evaluations
		SET status = 'completed', weighted_total = $2, max_total = $3,
		    caveats = $4, completed_at = now()
		WHERE id = $1`,
		id, res.WeightedTotal, res.MaxTotal, res.Caveats); err != nil {
		return fmt.Errorf("close evaluation %s: %w", id, err)
	}
	return nil
}

func (s *Store) failEvaluation(ctx context.Context, id uuid.UUID, why string) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE evaluations
		SET status = 'failed', failure_reason = $2, completed_at = now()
		WHERE id = $1`, id, why); err != nil {
		return fmt.Errorf("fail evaluation %s: %w", id, err)
	}
	return nil
}

/* ------------------------------------------------------------------- utils */

// reason turns an error into something a student can read in a report caveat.
// The underlying error still goes to the log in full.
func reason(err error) string {
	switch {
	case errors.Is(err, llm.ErrUnstructured):
		return "the assessor did not return a usable result"
	case errors.Is(err, context.DeadlineExceeded):
		return "the assessor timed out"
	default:
		return "the assessor was unavailable"
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a paragraph break so the model does not read half a sentence as
	// the end of the argument.
	cut := s[:n]
	if i := strings.LastIndex(cut, "\n\n"); i > n/2 {
		cut = cut[:i]
	}
	return cut + "\n\n[document truncated]"
}
