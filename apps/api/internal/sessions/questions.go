package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

type monitorBid struct {
	Action       string  `json:"action"`
	Priority     float64 `json:"priority"`
	Seed         string  `json:"seed"`
	Reason       string  `json:"reason"`
	Quote        string  `json:"quote"`
	Memory       string  `json:"memory"`
	BankRelevant bool    `json:"bank_relevant"`
}

func (b *monitorBid) Validate() error {
	if !slices.Contains([]string{"interrupt", "continue", "note"}, b.Action) || b.Priority < 0 || b.Priority > 1 || len(b.Seed) > 1000 || len(b.Quote) > 1000 || len(b.Memory) > 2000 || !slices.Contains([]string{"off_topic", "contradiction", "unsupported_claim", "clarification", "follow_up", "none"}, b.Reason) {
		return fmt.Errorf("invalid monitor bid")
	}
	return nil
}

func (b monitorBid) shouldInterrupt(style string, speaking bool, latest string) bool {
	if b.Action != "interrupt" || b.Reason == "none" || strings.TrimSpace(b.Seed) == "" || strings.TrimSpace(b.Quote) == "" || !strings.Contains(latest, b.Quote) {
		return false
	}
	if !speaking {
		return true
	}
	if style == "patient" || b.Priority < .85 {
		return false
	}
	return style == "strict" || b.Reason == "off_topic" || b.Reason == "contradiction"
}

type liveQuestion struct {
	Text  string `json:"text"`
	Quote string `json:"quote"`
}

func (q *liveQuestion) Validate() error {
	if strings.TrimSpace(q.Text) == "" || len(q.Text) > 600 || len(strings.Fields(q.Text)) > 30 || strings.Count(q.Text, "?") != 1 || !strings.HasSuffix(strings.TrimSpace(q.Text), "?") || strings.TrimSpace(q.Quote) == "" || len(q.Quote) > 1000 {
		return fmt.Errorf("invalid live question")
	}
	return nil
}

func (q liveQuestion) grounded(latest string) bool {
	return q.Validate() == nil && strings.Contains(latest, q.Quote)
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
	err = tx.QueryRow(ctx, `SELECT coalesce(right(string_agg(text,E'\n' ORDER BY seq),16000),'') FROM (SELECT seq,coalesce(payload->>'speaker','Participant')||': '||(payload->>'text') text FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type IN ('transcript_final','judge_question') ORDER BY seq DESC LIMIT 24) t`, c.SessionID, c.OrgID).Scan(&record)
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
	var materials string
	err = tx.QueryRow(ctx, `WITH sources AS (
 SELECT d.id,k.title,k.kind,d.extracted_text body FROM knowledge_sources k JOIN documents d ON d.knowledge_source_id=k.id AND d.organization_id=k.organization_id WHERE k.organization_id=$1 AND (k.assessment_id=$2 OR k.assessment_id IS NULL) AND d.parse_status='parsed' AND k.visibility<>'staff' AND (k.visibility='all' OR k.visibility=$4)
 UNION ALL SELECT d.id,'Candidate memorial','memorial',d.extracted_text FROM artifacts ar JOIN documents d ON d.id=ar.document_id AND d.organization_id=ar.organization_id WHERE ar.organization_id=$1 AND ar.assignment_id=$3 AND d.parse_status='parsed'
 ), search AS (SELECT websearch_to_tsquery('english',array_to_string(tsvector_to_array(to_tsvector('english',$5)), ' OR ')) terms), relevant AS (
 SELECT s.title,c.content body,1 section,ts_rank_cd(c.content_tsv,search.terms) relevance FROM sources s JOIN document_chunks c ON c.document_id=s.id AND c.organization_id=$1 CROSS JOIN search WHERE c.content_tsv @@ search.terms ORDER BY relevance DESC,c.id LIMIT 4
 ), excerpts AS (
 SELECT title,left(body,2000) body,CASE WHEN kind='problem' THEN 0 ELSE 2 END section,0::real relevance FROM sources
 UNION ALL SELECT title,body,section,relevance FROM relevant
 ) SELECT coalesce(left(string_agg(title||E'\n'||body,E'\n' ORDER BY section,relevance DESC,title),12000),'') FROM excerpts`, c.OrgID, assessment, assignment, side, latest+" "+memory).Scan(&materials)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(materials) == "" {
		return "", fmt.Errorf("live judge needs parsed case materials")
	}
	started := time.Now()
	var question string
	var bank *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT q.id,q.text FROM question_bank_items q CROSS JOIN (SELECT websearch_to_tsquery('english',array_to_string(tsvector_to_array(to_tsvector('english',$6)), ' OR ')) terms) search WHERE q.organization_id=$1 AND q.assessment_id=$2 AND (q.assignment_id IS NULL OR q.assignment_id=$3) AND (q.side IS NULL OR q.side=$4) AND to_tsvector('english',q.text) @@ search.terms AND NOT EXISTS(SELECT 1 FROM session_events e WHERE e.session_id=$5 AND e.organization_id=$1 AND e.type='judge_question' AND e.payload->>'text'=q.text) ORDER BY ts_rank(to_tsvector('english',q.text),search.terms) DESC,q.assignment_id NULLS LAST,q.id LIMIT 1`, c.OrgID, assessment, assignment, side, c.SessionID, latest).Scan(&bank, &question)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	style := cfg.JudgingStyle
	if style == "" {
		style = "balanced"
	}
	blocks := []harness.Untrusted{{Label: "case_materials", Text: materials}, {Label: "conversation_memory", Text: memory}, {Label: "transcript", Text: record}, {Label: "latest_speech", Text: latest}, {Label: "bank_candidate", Text: question}}
	var bid monitorBid
	err = h.harness.Structured(ctx, harness.Call{Tier: "monitor", Purpose: "live_monitor", PromptVersion: "monitor.v2", MaxTokens: 600, System: liveMonitorPrompt + " Focus: " + strings.Join(focus, ", "), Untrusted: blocks, User: fmt.Sprintf("Candidate side: %s. Judging style: %s. Candidate still speaking: %t.", side, style, speaking), OrganizationID: &c.OrgID, AssignmentID: &assignment, SessionID: &c.SessionID}, &bid)
	if err != nil {
		return "", err
	}
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
	if !bid.shouldInterrupt(style, speaking, latest) || !policy.allowed(cfg.Interruptions, count, last, time.Now(), bid.Priority) {
		if err = saveMemory(); errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		} else if err != nil {
			return "", err
		}
		if participant != nil {
			if _, err = tx.Exec(ctx, `INSERT INTO judge_actions(organization_id,session_id,participant_id,action,priority,reason,outcome,outcome_reason,latency_ms) VALUES($1,$2,$3,$4,$5,$6,'dropped','continue or policy limit',$7)`, c.OrgID, c.SessionID, participant, bid.Action, bid.Priority, bid.Reason, time.Since(started).Milliseconds()); err != nil {
				return "", err
			}
		}
		return "", tx.Commit(ctx)
	}
	source := "bank"
	if bank == nil || !bid.BankRelevant || bid.Reason == "off_topic" || !(liveQuestion{Text: question, Quote: bid.Quote}).grounded(latest) {
		source = "generated"
		bank = nil
		decision, _ := json.Marshal(bid)
		blocks = append(blocks, harness.Untrusted{Label: "monitor_decision", Text: string(decision)})
		var out liveQuestion
		err = h.harness.Structured(ctx, harness.Call{Tier: tier, Purpose: "live_question", PromptVersion: "live.v3", MaxTokens: 240, Temperature: &temperature, System: prompt + liveQuestionPrompt + " Focus: " + strings.Join(focus, ", "), Untrusted: blocks, User: "Respond to the latest speech and the approved intervention. Candidate side: " + side, OrganizationID: &c.OrgID, AssignmentID: &assignment, SessionID: &c.SessionID}, &out)
		if err != nil {
			return "", err
		}
		if !out.grounded(latest) {
			return "", fmt.Errorf("live question did not quote the latest speech")
		}
		question = out.Text
	}
	if err = saveMemory(); errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	var repeated bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type='judge_question' AND lower(trim(payload->>'text'))=lower(trim($3::text)))`, c.SessionID, c.OrgID, question).Scan(&repeated); err != nil {
		return "", err
	}
	if repeated {
		if participant != nil {
			if _, err = tx.Exec(ctx, `INSERT INTO judge_actions(organization_id,session_id,participant_id,action,priority,reason,outcome,outcome_reason) VALUES($1,$2,$3,'continue',$4,$5,'dropped','question already asked')`, c.OrgID, c.SessionID, participant, bid.Priority, bid.Reason); err != nil {
				return "", err
			}
		}
		return "", tx.Commit(ctx)
	}
	var seq int64
	err = tx.QueryRow(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE id=$1 AND organization_id=$2 AND status='running' AND NOT EXISTS(SELECT 1 FROM scheduled_transitions t WHERE t.subject_id=$1 AND t.cause_key IN ('end','drain_end') AND t.run_at-CASE WHEN t.cause_key='drain_end' THEN interval '120 seconds' ELSE interval '0 seconds' END<=now()) RETURNING last_seq`, c.SessionID, c.OrgID).Scan(&seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]any{"speaker": name, "text": strings.TrimSpace(question), "profile_id": profile, "voice": voice, "source": source})
	_, err = tx.Exec(ctx, `INSERT INTO session_events(session_id,seq,organization_id,type,actor_kind,actor_id,payload) VALUES($1,$2,$3,'judge_question','ai',$4,$5)`, c.SessionID, seq, c.OrgID, profile, payload)
	if err != nil {
		return "", err
	}
	if participant != nil {
		_, err = tx.Exec(ctx, `INSERT INTO judge_actions(organization_id,session_id,participant_id,action,priority,reason,question_text,source,question_bank_item_id,outcome,latency_ms) VALUES($1,$2,$3,'ask_question',$4,$5,$6,$7,$8,'approved',$9)`, c.OrgID, c.SessionID, participant, bid.Priority, bid.Reason+": "+bid.Quote, question, source, bank, time.Since(started).Milliseconds())
		if err != nil {
			return "", err
		}
	}
	return question, tx.Commit(ctx)
}

const liveMonitorPrompt = `You are the presiding moot judge. Let counsel develop an argument. Do not ask a question merely because counsel paused.
Use the case materials to identify the issues. Treat a brief analogy or background explanation as potentially relevant.
Intervene for sustained topic drift, a clear contradiction, an unsupported material claim, an unclear material point, or an unanswered bench question.
For off_topic, require sustained unrelated speech, not one unusual word. For contradiction, identify both conflicting claims in the conversation.
Consider the last bench question and the answer. Do not repeat an answered question. Let a satisfactory answer return to the argument.
Balanced: wait for pauses except high-priority sustained drift or contradictions. Strict: challenge material weaknesses sooner. Patient: always wait for a pause.
Return JSON {"action":"interrupt|continue|note","priority":0.0,"reason":"off_topic|contradiction|unsupported_claim|clarification|follow_up|none","seed":"specific issue to address","quote":"exact short quote from latest_speech","memory":"brief rolling summary of claims, authorities, concessions and unanswered questions","bank_relevant":false}.
Update the previous memory using only the supplied conversation. Keep memory under 2000 characters.
Set bank_relevant true only when the bank candidate directly addresses the quoted current claim and has not been answered. No scores.`

const liveQuestionPrompt = `
Act on the approved intervention. Ask exactly one question of at most 30 words.
Tie it to the quoted current claim and supplied case materials. For off_topic, ask counsel to explain the connection to a specific case issue.
For follow_up, address what remains unanswered. Do not repeat an answered question. Never invent facts, authorities or scores.
Return JSON {"text":"one short question?","quote":"exact short quote from latest_speech that this question addresses"}.`

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
