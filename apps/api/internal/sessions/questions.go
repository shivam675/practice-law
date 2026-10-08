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
	"github.com/intelimek/megamoot/apps/api/internal/harness"
	"github.com/intelimek/megamoot/apps/api/internal/spec"
	"github.com/jackc/pgx/v5"
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
	Action   string  `json:"action"`
	Priority float64 `json:"priority"`
	Seed     string  `json:"seed"`
}

func (b *monitorBid) Validate() error {
	if !slices.Contains([]string{"interrupt", "continue", "note"}, b.Action) || b.Priority < 0 || b.Priority > 1 || len(b.Seed) > 1000 {
		return fmt.Errorf("invalid monitor bid")
	}
	return nil
}

func (h *Handlers) question(ctx context.Context, c *Claims, assignment uuid.UUID, stage string) (string, error) {
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
	err = tx.QueryRow(ctx, `SELECT sp.id,p.id,sp.display_name,p.model_tier,p.system_prompt,p.voice,p.temperature,p.interruption_policy,p.capabilities,p.focus FROM session_participants sp JOIN ai_profiles p ON p.id=sp.ai_profile_id AND p.organization_id=sp.organization_id WHERE sp.session_id=$1 AND sp.organization_id=$2 AND sp.kind='ai' AND p.capabilities @> ARRAY['ask_question','interrupt']::text[] AND (SELECT count(*) FROM session_events e WHERE e.session_id=sp.session_id AND e.organization_id=sp.organization_id AND e.type='judge_question' AND e.payload->>'profile_id'=p.id::text)<coalesce((p.interruption_policy->>'max_per_stage')::int,8) AND (cardinality($3::text[])=0 OR p.key=ANY($3)) ORDER BY (SELECT count(*) FROM session_events e WHERE e.session_id=sp.session_id AND e.organization_id=sp.organization_id AND e.type='judge_question' AND e.payload->>'profile_id'=p.id::text),sp.is_presiding DESC,sp.id LIMIT 1`, c.SessionID, c.OrgID, nonNil(cfg.AIProfiles)).Scan(&participant, &profile, &name, &tier, &prompt, &voice, &temperature, &raw, &caps, &focus)
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
	var record string
	err = tx.QueryRow(ctx, `SELECT coalesce(string_agg(text,E'\n' ORDER BY seq),'') FROM (SELECT seq,coalesce(payload->>'speaker','Participant')||': '||(payload->>'text') text FROM session_events WHERE session_id=$1 AND organization_id=$2 AND type IN ('transcript_final','judge_question') ORDER BY seq DESC LIMIT 6) t`, c.SessionID, c.OrgID).Scan(&record)
	if err != nil || strings.TrimSpace(record) == "" {
		return "", err
	}
	started := time.Now()
	// ponytail: a timed-out monitor uses a bounded pause question; use a dedicated fast monitor when available.
	bid := monitorBid{Action: "interrupt", Priority: .75, Seed: record}
	var proposed monitorBid
	err = h.harness.Structured(ctx, harness.Call{Tier: "monitor", Purpose: "live_monitor", PromptVersion: "monitor.v1", Timeout: 700 * time.Millisecond, MaxTokens: 160, System: "Decide whether a short question would clarify the candidate's reasoning. Return JSON {\"action\":\"interrupt|continue|note\",\"priority\":0.0,\"seed\":\"topic to clarify\"}. No scores. Focus: " + strings.Join(focus, ", "), Untrusted: []harness.Untrusted{{Label: "transcript", Text: record}}, OrganizationID: &c.OrgID, AssignmentID: &assignment, SessionID: &c.SessionID}, &proposed)
	if err == nil {
		bid = proposed
	}
	if bid.Action != "interrupt" || !policy.allowed(cfg.Interruptions, count, last, time.Now(), bid.Priority) {
		return "", nil
	}
	var question string
	var bank *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT q.id,q.text FROM question_bank_items q WHERE q.organization_id=$1 AND q.assessment_id=$2 AND (q.assignment_id IS NULL OR q.assignment_id=$3) AND (q.side IS NULL OR q.side=$4) AND NOT EXISTS(SELECT 1 FROM session_events e WHERE e.session_id=$5 AND e.organization_id=$1 AND e.type='judge_question' AND e.payload->>'text'=q.text) ORDER BY ts_rank(to_tsvector('english',q.text),plainto_tsquery('english',$6)) DESC,q.assignment_id NULLS LAST,q.id LIMIT 1`, c.OrgID, assessment, assignment, side, c.SessionID, bid.Seed).Scan(&bank, &question)
	source := "bank"
	if errors.Is(err, pgx.ErrNoRows) {
		source = "generated"
		question, err = h.harness.Text(ctx, harness.Call{Tier: tier, Purpose: "live_question", PromptVersion: "live.v2", Timeout: 1800 * time.Millisecond, MaxTokens: 100, Temperature: &temperature, System: prompt + " Ask exactly one short question, at most 30 words. Focus: " + strings.Join(focus, ", "), Untrusted: []harness.Untrusted{{Label: "transcript", Text: record}}, User: "Ask the next question.", OrganizationID: &c.OrgID, AssignmentID: &assignment, SessionID: &c.SessionID})
		if err != nil || strings.TrimSpace(question) == "" || len(question) > 600 {
			question = fallbackQuestions[count%len(fallbackQuestions)]
			source = "tier0"
		}
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
	payload, _ := json.Marshal(map[string]any{"speaker": name, "text": strings.TrimSpace(question), "profile_id": profile, "voice": voice, "source": source})
	_, err = tx.Exec(ctx, `INSERT INTO session_events(session_id,seq,organization_id,type,actor_kind,actor_id,payload) VALUES($1,$2,$3,'judge_question','ai',$4,$5)`, c.SessionID, seq, c.OrgID, profile, payload)
	if err != nil {
		return "", err
	}
	if participant != nil {
		_, err = tx.Exec(ctx, `INSERT INTO judge_actions(organization_id,session_id,participant_id,action,priority,reason,question_text,source,question_bank_item_id,outcome,latency_ms) VALUES($1,$2,$3,'ask_question',$4,$5,$6,$7,$8,'approved',$9)`, c.OrgID, c.SessionID, participant, bid.Priority, "pause after candidate turn", question, source, bank, time.Since(started).Milliseconds())
		if err != nil {
			return "", err
		}
	}
	return question, tx.Commit(ctx)
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
