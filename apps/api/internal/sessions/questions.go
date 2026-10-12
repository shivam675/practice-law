package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/slmlabs/megamoot/apps/api/internal/grading"
	"github.com/slmlabs/megamoot/apps/api/internal/harness"
	"github.com/slmlabs/megamoot/apps/api/internal/spec"
)

type questionPolicy struct {
	CooldownS   int     `json:"cooldown_s"`
	MaxPerStage int     `json:"max_per_stage"`
	MinPriority float64 `json:"min_priority"`
}

func (p questionPolicy) allowed(mode spec.InterruptionPolicy, count int, last, now time.Time, priority float64) bool {
	cap := p.MaxPerStage
	if mode == spec.InterruptionsLimited {
		cap = min(cap, 2)
	}
	return mode != spec.InterruptionsDisabled && count < cap && priority >= p.MinPriority && now.Sub(last) >= time.Duration(p.CooldownS)*time.Second
}

var fallbackQuestions = []string{
	"Which part of the record most strongly supports your conclusion?",
	"What is the strongest objection to your argument, and how would you answer it?",
	"What evidence would cause you to change your conclusion?",
	"Could you explain the link between that evidence and the outcome you propose?",
	"Is there a less restrictive alternative that would achieve the same objective?",
	"Which assumption does your argument depend on most?",
	"How would your conclusion change if that assumption were incorrect?",
	"What precise outcome are you asking for, and why?",
}

var errNoMaterials = errors.New("live judge needs parsed case materials")

type monitorBid struct {
	SpeechKind    string  `json:"speech_kind"`
	Action        string  `json:"action"`
	Priority      float64 `json:"priority"`
	Seed          string  `json:"seed"`
	Reason        string  `json:"reason"`
	Quote         string  `json:"quote"`
	ConflictQuote string  `json:"conflict_quote"`
	Memory        string  `json:"memory"`
}

func (b *monitorBid) Validate() error {
	if !slices.Contains([]string{"argument", "answer", "procedural", "off_topic"}, b.SpeechKind) || !slices.Contains([]string{"interrupt", "continue", "note"}, b.Action) || b.Priority < 0 || b.Priority > 1 || len(b.Seed) > 1000 || len(b.Quote) > 1000 || len(b.ConflictQuote) > 1000 || len(b.Memory) > 2000 || !slices.Contains([]string{"off_topic", "procedural", "contradiction", "unsupported_claim", "clarification", "follow_up", "none"}, b.Reason) {
		return fmt.Errorf("invalid monitor bid")
	}
	return nil
}

// normalize makes the reason agree with the classification. A small model
// that files "the judge is unavailable" under contradiction has already said,
// one field earlier, that the speech was about the hearing. The kind wins:
// talk about the app gets a short answer and a way back, never a legal
// challenge, and a legal argument is never redirected as drift.
func (b *monitorBid) normalize() {
	switch b.SpeechKind {
	case "procedural", "off_topic":
		b.Reason = b.SpeechKind
	default:
		if b.Reason == "procedural" || b.Reason == "off_topic" {
			b.Action, b.Reason = "continue", "none"
		}
	}
}

// shouldInterrupt is the code half of the decision. The model proposes; this
// checks the proposal is grounded in words actually spoken and that the
// judging style allows it now.
func (b monitorBid) shouldInterrupt(style string, speaking bool, latest, record string) bool {
	if b.Action != "interrupt" || b.Reason == "none" || strings.TrimSpace(b.Seed) == "" || !spoken(latest, b.Quote) {
		return false
	}
	// A contradiction needs both sides on the record, not one quote and a hunch.
	if b.Reason == "contradiction" && (!spoken(record, b.ConflictQuote) || spoken(b.Quote, b.ConflictQuote)) {
		return false
	}
	if !speaking {
		return true
	}
	// Leaving the case is stopped at once, as a presiding judge would stop it:
	// every second of it is time out of counsel's round. Only a stage the
	// teacher set to patient waits for the pause.
	if b.Reason == "off_topic" || b.Reason == "procedural" {
		return style != "patient"
	}
	if style == "patient" || b.Priority < .85 {
		return false
	}
	return style == "strict" || b.Reason == "contradiction"
}

// liveQuestion carries no quote of its own. The monitor's quote is what ties
// the intervention to words counsel said, and it is checked before this call
// is made; asking a small model to copy a second one only adds a way to fail.
type liveQuestion struct {
	Text string `json:"text"`
}

func (q *liveQuestion) Validate() error {
	if strings.TrimSpace(q.Text) == "" || len(q.Text) > 600 || len(strings.Fields(q.Text)) > 35 || strings.Count(q.Text, "?") != 1 || !strings.HasSuffix(strings.TrimSpace(q.Text), "?") {
		return fmt.Errorf("invalid live question")
	}
	return nil
}

// spoken reports whether quote appears in text, ignoring case and punctuation.
// Speech recognition punctuates by guesswork, and a model copying a quote
// drops a comma; neither makes the words any less said. An empty quote is
// never spoken.
func spoken(text, quote string) bool {
	q := plainWords(quote)
	return q != "" && strings.Contains(" "+plainWords(text)+" ", " "+q+" ")
}

func plainWords(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

// repeats returns the earlier bench questions that ask what question asks.
// Rewording is not a new question: most of the shorter one's content words
// appearing in the other means the bench is saying the same thing again.
func repeats(question string, asked []string) []string {
	words := benchWords(question)
	var same []string
	for _, earlier := range asked {
		prior := benchWords(earlier)
		shared := 0
		for w := range words {
			if prior[w] {
				shared++
			}
		}
		if smaller := min(len(words), len(prior)); strings.EqualFold(strings.TrimSpace(question), strings.TrimSpace(earlier)) || smaller >= 3 && float64(shared)/float64(smaller) >= .7 {
			same = append(same, earlier)
		}
	}
	return same
}

// benchWords drops the bench's own framing, which every redirect shares and
// which says nothing about what is being asked.
func benchWords(text string) map[string]bool {
	words := grading.ContentWords(text)
	for _, w := range []string{"bench", "need", "submission", "continue", "answer", "noted", "listening", "issue", "case", "again"} {
		delete(words, w)
	}
	return words
}

const pressPrefix = "Counsel, the bench asks again: "

// frame is how a bench speaks for itself, done in code because a small model
// will not reliably do it. It asks once; when counsel dodges, it presses once
// with the same question, saying that it is pressing; it never asks a third
// time. A question about the hearing is acknowledged before counsel is
// returned to the case.
func frame(question, reason string, asked []string) (string, bool) {
	switch same := repeats(question, asked); {
	case len(same) >= 2:
		return "", false
	case len(same) == 1:
		if reason != "off_topic" && reason != "procedural" && reason != "follow_up" {
			return "", false
		}
		return pressPrefix + dropAddress(strings.TrimPrefix(same[0], pressPrefix)), true
	}
	if reason == "procedural" {
		lower := strings.ToLower(question)
		if !strings.HasPrefix(lower, "noted") && !strings.HasPrefix(lower, "understood") && !strings.HasPrefix(lower, "thank you") {
			return "Noted, Counsel. " + dropAddress(question), true
		}
	}
	return question, true
}

// dropAddress removes a leading "Counsel," so a framed reply names counsel once.
func dropAddress(question string) string {
	if rest, ok := strings.CutPrefix(question, "Counsel, "); ok && rest != "" {
		return strings.ToUpper(rest[:1]) + rest[1:]
	}
	return question
}

func (h *Handlers) question(ctx context.Context, c *Claims, assignment uuid.UUID, stage string, speaking bool) (string, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "judge:"+c.SessionID.String()).Scan(&locked); err != nil || !locked {
		return "", err
	}
	var raw, participation []byte
	var assessment uuid.UUID
	var side string
	err = tx.QueryRow(ctx, `SELECT v.value->'config',tv.participation,a.assessment_id,a.side FROM assignments a JOIN assessments ass ON ass.id=a.assessment_id AND ass.organization_id=a.organization_id JOIN assessment_template_versions tv ON tv.id=ass.template_version_id CROSS JOIN LATERAL jsonb_array_elements(tv.stages) v(value) WHERE a.id=$1 AND a.organization_id=$2 AND v.value->>'id'=$3`, assignment, c.OrgID, stage).Scan(&raw, &participation, &assessment, &side)
	if err != nil {
		return "", err
	}
	var cfg spec.LiveTurnConfig
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	if cfg.Interruptions == spec.InterruptionsDisabled {
		return "", nil
	}
	policy := questionPolicy{CooldownS: 25, MaxPerStage: 8, MinPriority: .65}
	name, tier, prompt, voice := "Examiner", "judge", "You are an examiner. Ask for clarification or evidence. Never invent authorities or disclose scores.", "af_heart"
	var participant, profile *uuid.UUID
	var caps, focus []string
	var temperature float64 = .4
	err = tx.QueryRow(ctx, `SELECT sp.id,p.id,sp.display_name,p.model_tier,p.system_prompt,p.voice,p.temperature,p.interruption_policy,p.capabilities,p.focus FROM session_participants sp JOIN ai_profiles p ON p.id=sp.ai_profile_id AND p.organization_id=sp.organization_id WHERE sp.session_id=$1 AND sp.organization_id=$2 AND sp.kind='ai' AND p.capabilities @> ARRAY['ask_question','interrupt']::text[] AND (SELECT count(*) FROM session_events e WHERE e.session_id=sp.session_id AND e.organization_id=sp.organization_id AND e.type='judge_question' AND e.payload->>'profile_id'=p.id::text)<coalesce((p.interruption_policy->>'max_per_stage')::int,8) AND (cardinality($3::text[])=0 OR p.key=ANY($3)) ORDER BY sp.is_presiding DESC,sp.id LIMIT 1`, c.SessionID, c.OrgID, nonNil(cfg.AIProfiles)).Scan(&participant, &profile, &name, &tier, &prompt, &voice, &temperature, &raw, &caps, &focus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if profile != nil {
		if !slices.Contains(caps, "ask_question") || !slices.Contains(caps, "interrupt") {
			return "", nil
		}
		if err = json.Unmarshal(raw, &policy); err != nil {
			return "", err
		}
		policy.CooldownS = max(1, policy.CooldownS)
		policy.MaxPerStage = max(0, policy.MaxPerStage)
	} else {
		var part spec.Participation
		if err = json.Unmarshal(participation, &part); err != nil {
			return "", err
		}
		if len(part.AIActors) > 0 {
			return "", nil
		}
	}
	var count int
	var last time.Time
	err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE $4 OR $3::uuid IS NULL OR payload->>'profile_id'=$3::text),coalesce(max(created_at),'epoch'::timestamptz) FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type='judge_question'`, c.SessionID, c.OrgID, profile, cfg.Interruptions == spec.InterruptionsLimited).Scan(&count, &last)
	if err != nil || !policy.allowed(cfg.Interruptions, count, last, time.Now(), 1) {
		return "", err
	}
	var record, latest, memory string
	var transcriptSeq, memoryTranscriptSeq int64
	err = tx.QueryRow(ctx, `SELECT payload->>'text',seq FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type='transcript_final' ORDER BY seq DESC LIMIT 1`, c.SessionID, c.OrgID).Scan(&latest, &transcriptSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Bounded on purpose: a small model reads a short record well and a long one
	// badly. The rolling memory carries what scrolled out of this window.
	err = tx.QueryRow(ctx, `SELECT coalesce(right(string_agg(text,E'\n' ORDER BY seq),6000),'') FROM (SELECT seq,coalesce(payload->>'speaker','Participant')||': '||(payload->>'text') text FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type IN ('transcript_final','judge_question') ORDER BY seq DESC LIMIT 12) t`, c.SessionID, c.OrgID).Scan(&record)
	if err != nil || strings.TrimSpace(record) == "" {
		return "", err
	}
	err = tx.QueryRow(ctx, `SELECT coalesce((SELECT payload->>'memory' FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type='judge_memory' ORDER BY seq DESC LIMIT 1),''),coalesce((SELECT (payload->>'transcript_seq')::bigint FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type='judge_memory' ORDER BY seq DESC LIMIT 1),0)`, c.SessionID, c.OrgID).Scan(&memory, &memoryTranscriptSeq)
	if err != nil {
		return "", err
	}
	if memoryTranscriptSeq >= transcriptSeq {
		return "", nil
	}
	var asked []string
	if err = tx.QueryRow(ctx, `SELECT coalesce(array_agg(payload->>'text' ORDER BY seq),'{}') FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type='judge_question'`, c.SessionID, c.OrgID).Scan(&asked); err != nil {
		return "", err
	}
	var materials string
	err = tx.QueryRow(ctx, `WITH sources AS (
 SELECT d.id,k.title,k.kind,d.extracted_text body FROM knowledge_sources k JOIN documents d ON d.knowledge_source_id=k.id AND d.organization_id=k.organization_id WHERE k.organization_id=$1 AND (k.assessment_id=$2 OR k.assessment_id IS NULL) AND d.parse_status='parsed' AND k.visibility<>'staff' AND (k.visibility='all' OR k.visibility=$4)
 UNION ALL SELECT d.id,'Candidate memorial','memorial',d.extracted_text FROM artifacts ar JOIN documents d ON d.id=ar.document_id AND d.organization_id=ar.organization_id WHERE ar.organization_id=$1 AND ar.assignment_id=$3 AND d.parse_status='parsed'
 ), search AS (SELECT websearch_to_tsquery('english',array_to_string(tsvector_to_array(to_tsvector('english',$5)), ' OR ')) terms), relevant AS (
 SELECT s.title,left(c.content,1200) body,1 section,ts_rank_cd(c.content_tsv,search.terms) relevance FROM sources s JOIN document_chunks c ON c.document_id=s.id AND c.organization_id=$1 CROSS JOIN search WHERE c.content_tsv @@ search.terms ORDER BY relevance DESC,c.id LIMIT 3
 ), excerpts AS (
 SELECT title,left(body,1500) body,CASE WHEN kind='problem' THEN 0 ELSE 2 END section,0::real relevance FROM sources
 UNION ALL SELECT title,body,section,relevance FROM relevant
 ) SELECT coalesce(left(string_agg(title||E'\n'||body,E'\n' ORDER BY section,relevance DESC,title),6000),'') FROM excerpts`, c.OrgID, assessment, assignment, side, latest+" "+memory).Scan(&materials)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(materials) == "" {
		return "", errNoMaterials
	}
	started := time.Now()
	style := cfg.JudgingStyle
	if style == "" {
		style = "balanced"
	}
	blocks := []harness.Untrusted{{Label: "case_materials", Text: materials}, {Label: "conversation_memory", Text: memory}, {Label: "transcript", Text: record}, {Label: "latest_speech", Text: latest}}
	var bid monitorBid
	err = h.harness.Structured(ctx, harness.Call{Tier: "monitor", Purpose: "live_monitor", PromptVersion: "monitor.v6", MaxTokens: 600, System: liveMonitorPrompt + " Focus: " + strings.Join(focus, ", "), Untrusted: blocks, User: fmt.Sprintf("Candidate side: %s. Judging style: %s. Candidate still speaking: %t.", side, style, speaking), OrganizationID: &c.OrgID, AssignmentID: &assignment, SessionID: &c.SessionID}, &bid)
	if err != nil {
		return "", err
	}
	bid.normalize()
	// The rolling memory keeps earlier claims and unanswered questions without replaying the whole round.
	memoryPayload, _ := json.Marshal(map[string]any{"memory": bid.Memory, "transcript_seq": transcriptSeq})
	var memorySeq int64
	saveMemory := func() error {
		// Lock after inference so the next query sees turns saved while the model ran.
		if err := tx.QueryRow(ctx, `SELECT last_seq FROM sessions WHERE id=$1 AND organization_id=$2 FOR UPDATE`, c.SessionID, c.OrgID).Scan(&memorySeq); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE id=$1 AND organization_id=$2 AND status='running' AND NOT EXISTS(SELECT 1 FROM session_events WHERE session_id=$1 AND type='transcript_final' AND seq>$3) RETURNING last_seq`, c.SessionID, c.OrgID, transcriptSeq).Scan(&memorySeq); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO session_events(session_id,seq,organization_id,type,payload) VALUES($1,$2,$3,'judge_memory',$4)`, c.SessionID, memorySeq, c.OrgID, memoryPayload)
		return err
	}
	drop := func(why string) (string, error) {
		if err := saveMemory(); errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		} else if err != nil {
			return "", err
		}
		if participant != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO judge_actions(organization_id,session_id,participant_id,action,priority,reason,outcome,outcome_reason,latency_ms) VALUES($1,$2,$3,$4,$5,$6,'dropped',$7,$8)`, c.OrgID, c.SessionID, participant, bid.Action, bid.Priority, bid.SpeechKind+"/"+bid.Reason, why, time.Since(started).Milliseconds()); err != nil {
				return "", err
			}
		}
		return "", tx.Commit(ctx)
	}
	if !bid.shouldInterrupt(style, speaking, latest, record) || !policy.allowed(cfg.Interruptions, count, last, time.Now(), bid.Priority) {
		return drop("continue or policy limit")
	}
	// Prepared questions are offered to the bench, never to the monitor: a
	// ready-made question about the case reads as a reason to ask it.
	type prepared struct {
		id   uuid.UUID
		text string
	}
	var bank []prepared
	if bid.Reason != "procedural" && bid.Reason != "off_topic" {
		rows, err := tx.Query(ctx, `SELECT q.id,q.text FROM question_bank_items q CROSS JOIN (SELECT websearch_to_tsquery('english',array_to_string(tsvector_to_array(to_tsvector('english',$5)), ' OR ')) terms) search WHERE q.organization_id=$1 AND q.assessment_id=$2 AND (q.assignment_id IS NULL OR q.assignment_id=$3) AND (q.side IS NULL OR q.side=$4) AND to_tsvector('english',q.text) @@ search.terms ORDER BY ts_rank(to_tsvector('english',q.text),search.terms) DESC,q.assignment_id NULLS LAST,q.id LIMIT 6`, c.OrgID, assessment, assignment, side, bid.Seed+" "+bid.Quote)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var p prepared
			if err = rows.Scan(&p.id, &p.text); err != nil {
				rows.Close()
				return "", err
			}
			if len(bank) < 3 && len(repeats(p.text, asked)) == 0 {
				bank = append(bank, p)
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return "", err
		}
	}
	var options []string
	for _, p := range bank {
		options = append(options, p.text)
	}
	decision, _ := json.Marshal(bid)
	questionBlocks := append(blocks,
		harness.Untrusted{Label: "bench_questions", Text: strings.Join(asked, "\n")},
		harness.Untrusted{Label: "prepared_questions", Text: strings.Join(options, "\n")},
		harness.Untrusted{Label: "monitor_decision", Text: string(decision)})
	var out liveQuestion
	err = h.harness.Structured(ctx, harness.Call{Tier: tier, Purpose: "live_question", PromptVersion: "live.v7", MaxTokens: 160, Temperature: &temperature, System: prompt + liveQuestionPrompt + " Focus: " + strings.Join(focus, ", "), Untrusted: questionBlocks, User: "Respond to the latest speech and the approved intervention. Candidate side: " + side, OrganizationID: &c.OrgID, AssignmentID: &assignment, SessionID: &c.SessionID}, &out)
	if err != nil {
		return "", err
	}
	question := strings.TrimSpace(out.Text)
	source := "generated"
	var bankID *uuid.UUID
	for _, p := range bank {
		if strings.EqualFold(strings.TrimSpace(p.text), question) {
			source, bankID = "bank", &p.id
		}
	}
	question, ok := frame(question, bid.Reason, asked)
	if !ok {
		return drop("question already asked")
	}
	if err = saveMemory(); errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	var seq int64
	err = tx.QueryRow(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE id=$1 AND organization_id=$2 AND status='running' AND NOT EXISTS(SELECT 1 FROM scheduled_transitions t WHERE t.subject_id=$1 AND t.cause_key IN ('end','drain_end') AND t.run_at-CASE WHEN t.cause_key='drain_end' THEN interval '120 seconds' ELSE interval '0 seconds' END<=now()) RETURNING last_seq`, c.SessionID, c.OrgID).Scan(&seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]any{"speaker": name, "text": question, "profile_id": profile, "voice": voice, "source": source, "reason": bid.Reason})
	_, err = tx.Exec(ctx, `INSERT INTO session_events(session_id,seq,organization_id,type,actor_kind,actor_id,payload) VALUES($1,$2,$3,'judge_question','ai',$4,$5)`, c.SessionID, seq, c.OrgID, profile, payload)
	if err != nil {
		return "", err
	}
	if participant != nil {
		_, err = tx.Exec(ctx, `INSERT INTO judge_actions(organization_id,session_id,participant_id,action,priority,reason,question_text,source,question_bank_item_id,outcome,latency_ms) VALUES($1,$2,$3,'ask_question',$4,$5,$6,$7,$8,'approved',$9)`, c.OrgID, c.SessionID, participant, bid.Priority, bid.SpeechKind+"/"+bid.Reason+": "+bid.Quote, question, source, bankID, time.Since(started).Milliseconds())
		if err != nil {
			return "", err
		}
	}
	return question, tx.Commit(ctx)
}

// The monitor classifies first and decides second. Small models anchor on
// whatever case issue is in front of them; making them name what kind of
// speech they heard before choosing a reason is what stops a complaint about
// the screen being answered as a legal contradiction.
const liveMonitorPrompt = `You are the presiding judge of a moot court, listening to counsel's oral submissions. Decide what a real judge would do now.
Step 1. Classify latest_speech as speech_kind:
- argument: a submission about the case, its facts, law or authorities, even a weak one, or an analogy or background that leads back to the case.
- answer: a reply to the last bench question.
- procedural: talk about the hearing itself, such as the app, screen, audio, time, a request to repeat or explain the question, or a request for help.
- off_topic: talk unrelated to the case, such as personal stories or other subjects.
Step 2. Compare latest_speech with what counsel said earlier in transcript and conversation_memory. If latest_speech denies or reverses an earlier statement of fact or law by counsel, that is a contradiction. Then decide.
- argument or answer: action continue, unless there is a material problem: a clear contradiction of an earlier claim (reason contradiction, and copy the earlier words into conflict_quote), a material claim with no support (unsupported_claim), a material point that is unclear (clarification), or the last bench question is still unanswered (follow_up). Do not repeat a question counsel has answered. Let a satisfactory answer return to the argument.
- procedural: action interrupt, reason procedural, so the bench can acknowledge it briefly and return counsel to the case.
- off_topic: action interrupt, reason off_topic, as soon as counsel is clearly talking about something unrelated to the case. One unusual word, or a brief analogy that leads back to the case, is not drift.
Never call procedural or off_topic speech a contradiction or an unsupported claim.
Priority: 0.9 for procedural, sustained off_topic or a clear contradiction; 0.7 for other material problems; 0.2 for continue.
Judging style: procedural and off_topic speech are interrupted at once, even mid-sentence. For other problems, balanced waits for a pause except for a clear contradiction; strict challenges material weaknesses sooner; patient always waits for a pause.
Return only JSON {"speech_kind":"argument|answer|procedural|off_topic","action":"interrupt|continue|note","priority":0.0,"reason":"off_topic|procedural|contradiction|unsupported_claim|clarification|follow_up|none","seed":"the specific case issue the bench should raise, named from case_materials","quote":"exact quote of at most 12 words copied from latest_speech","conflict_quote":"exact earlier words, at most 12, for a contradiction, otherwise empty","memory":"rolling summary of claims, authorities, concessions and unanswered bench questions"}.
Update the previous memory using only the supplied conversation. Keep memory under 600 characters: the reply must fit in 256 tokens. No scores.`

const liveQuestionPrompt = `
Speak as the presiding judge. Give one short spoken turn of at most 30 words that ends with exactly one question mark. Address counsel as "Counsel".
- procedural: ask one short question that returns counsel to a named case issue. Form: "<question on the issue>?"
- off_topic: tell counsel the bench needs submissions on the case, then ask about a named issue from case_materials. Form: "Counsel, the bench needs your submissions on <issue>. <question on the issue>?"
- contradiction: put both statements to counsel and ask which one they maintain.
- unsupported_claim: ask what in the record or which authority supports the quoted claim.
- clarification: ask counsel to clarify the quoted point.
- follow_up: ask only the part of the earlier bench question that is still unanswered.
You may use or adapt one of prepared_questions if it fits the approved intervention exactly. If counsel has not answered a question in bench_questions, ask it again. Never invent facts, authorities or scores. Replace each <...> with words about the actual issue from case_materials.
Return only JSON {"text":"one short turn ending in a question?"}.`

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
