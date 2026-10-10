package sessions

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/slmlabs/megamoot/apps/api/internal/db"
	"github.com/slmlabs/megamoot/apps/api/internal/harness"
	"github.com/slmlabs/megamoot/apps/api/internal/llm"
	"github.com/slmlabs/megamoot/apps/api/internal/platformcfg"
	"github.com/slmlabs/megamoot/apps/api/internal/secrets"
	"github.com/slmlabs/megamoot/apps/api/migrations"
)

// Run only against an isolated judge_test database, never an assessment database.
func TestLiveJudgePipeline(t *testing.T) {
	url := os.Getenv("JUDGE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set JUDGE_TEST_DATABASE_URL to an isolated judge_test database")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil || database != "judge_test" {
		t.Fatal("requires an isolated database named judge_test")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := db.Migrate(ctx, pool, migrations.FS, log); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	org, rubric, template, version, assessment, team, assignment, profile, provider := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO organizations(id,slug,name) VALUES($1,$2,'Judge test')`, org, org.String())
	exec(`INSERT INTO rubrics(id,organization_id,key,name) VALUES($1,$2,'test','Test')`, rubric, org)
	exec(`INSERT INTO assessment_templates(id,organization_id,assessment_type_key,key,name) VALUES($1,$2,'moot_court','test','Test')`, template, org)
	stages := `[ {"id":"oral","kind":"live_turn","config":{"duration_s":600,"interruptions":"enabled","judging_style":"balanced"}} ]`
	exec(`INSERT INTO assessment_template_versions(id,template_id,version,rubric_id,stages,participation) VALUES($1,$2,1,$3,$4,'{}')`, version, template, rubric, stages)
	exec(`INSERT INTO assessments(id,organization_id,template_version_id,title) VALUES($1,$2,$3,'Contract moot')`, assessment, org, version)
	exec(`INSERT INTO teams(id,organization_id,name) VALUES($1,$2,'Candidate')`, team, org)
	exec(`INSERT INTO assignments(id,organization_id,assessment_id,team_id,side) VALUES($1,$2,$3,$4,'applicant')`, assignment, org, assessment, team)
	exec(`INSERT INTO ai_profiles(id,organization_id,key,name,role,capabilities,interruption_policy) VALUES($1,$2,'bench','Judge','judge',ARRAY['ask_question','interrupt'],'{"cooldown_s":1,"max_per_stage":8,"min_priority":0.65}')`, profile, org)
	source := uuid.New()
	exec(`INSERT INTO knowledge_sources(id,organization_id,assessment_id,kind,title) VALUES($1,$2,$3,'problem','Contract dispute')`, source, org, assessment)
	exec(`INSERT INTO documents(organization_id,knowledge_source_id,storage_key,filename,content_type,byte_size,sha256,parse_status,extracted_text) VALUES($1,$2,'test','case.txt','text/plain',1,'','parsed','The court must decide whether a signed contract is enforceable. Clause 4 requires written notice before termination.')`, org, source)
	for _, visibility := range []string{"staff", "respondent"} {
		privateSource := uuid.New()
		exec(`INSERT INTO knowledge_sources(id,organization_id,assessment_id,kind,title,visibility) VALUES($1,$2,$3,'guidance','Hidden guidance',$4)`, privateSource, org, assessment, visibility)
		exec(`INSERT INTO documents(organization_id,knowledge_source_id,storage_key,filename,content_type,byte_size,sha256,parse_status,extracted_text) VALUES($1,$2,'hidden','hidden.txt','text/plain',1,'','parsed','PRIVATE MATERIAL MUST NOT REACH THE JUDGE')`, org, privateSource)
	}
	exec(`INSERT INTO question_bank_items(organization_id,assessment_id,text,generated_by) VALUES($1,$2,'What is the proportionality standard?','test')`, org, assessment)
	var mode, captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string        `json:"model"`
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if mode == "failure" {
			w.WriteHeader(503)
			return
		}
		var reply any
		if request.Model == "monitor" {
			for _, m := range request.Messages {
				captured += m.Content + "\n"
			}
			bid := monitorBid{Action: "interrupt", Priority: .95, Seed: "connection to the contract", Reason: "off_topic", Quote: "my holiday", Memory: "Counsel discussed a holiday. Contract enforceability remains the issue."}
			if mode == "continue" {
				bid.Action = "continue"
				bid.Reason = "none"
			}
			if mode == "fabricated" {
				bid.Quote = "words never spoken"
			}
			if mode == "stale" {
				var id uuid.UUID
				if err := pool.QueryRow(ctx, `SELECT id FROM sessions WHERE organization_id=$1 ORDER BY created_at DESC LIMIT 1`, org).Scan(&id); err != nil {
					t.Error(err)
				}
				exec(`UPDATE sessions SET last_seq=2 WHERE id=$1`, id)
				exec(`INSERT INTO session_events(session_id,seq,organization_id,type,payload) VALUES($1,2,$2,'transcript_final','{"text":"I return to clause 4."}')`, id, org)
			}
			reply = bid
		} else {
			reply = liveQuestion{Text: "How does your holiday relate to whether the contract is enforceable?", Quote: "my holiday"}
			if mode == "bad_question" {
				reply = liveQuestion{Text: "What about damages?", Quote: "damages"}
			}
		}
		content, _ := json.Marshal(reply)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	exec(`INSERT INTO model_providers(id,key,name,base_url) VALUES($1,$2,'Test provider',$3)`, provider, provider.String(), server.URL)
	for _, tier := range []string{"monitor", "judge"} {
		exec(`INSERT INTO model_bindings(tier,provider_id,model,max_tokens,timeout_ms,reasoning_effort) VALUES($1,$2,$1,600,10000,'none') ON CONFLICT(tier) DO UPDATE SET provider_id=$2,model=$1,max_tokens=600,timeout_ms=10000,reasoning_effort='none'`, tier, provider)
	}
	box, err := secrets.NewBox(strings.Repeat("00", 32))
	if err != nil {
		t.Fatal(err)
	}
	h := New(pool, nil, harness.New(platformcfg.NewStore(pool, box), llm.NewLedger(pool, log), log), "test", 3)
	for _, tc := range []struct {
		mode, style              string
		speaking, question, fail bool
	}{
		{"redirect", "balanced", true, true, false}, {"continue", "balanced", false, false, false}, {"fabricated", "strict", false, false, false},
		{"redirect", "patient", true, false, false}, {"redirect", "patient", false, true, false}, {"failure", "balanced", false, false, true},
		{"bad_question", "balanced", false, false, true}, {"stale", "balanced", true, false, false},
	} {
		t.Run(tc.mode+tc.style+map[bool]string{true: "speaking", false: "pause"}[tc.speaking], func(t *testing.T) {
			mode, captured = tc.mode, ""
			exec(`UPDATE assessment_template_versions SET stages=jsonb_set(stages,'{0,config,judging_style}',to_jsonb($2::text)) WHERE id=$1`, version, tc.style)
			id := uuid.New()
			exec(`INSERT INTO sessions(id,organization_id,assignment_id,stage_id,status,scheduled_at,last_seq) VALUES($1,$2,$3,'oral','running',now(),1)`, id, org, assignment)
			defer pool.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
			exec(`INSERT INTO session_participants(organization_id,session_id,kind,ai_profile_id,role,display_name,is_presiding) VALUES($1,$2,'ai',$3,'judge','Judge',true)`, org, id, profile)
			exec(`INSERT INTO session_events(session_id,seq,organization_id,type,payload) VALUES($1,1,$2,'transcript_final','{"speaker":"Counsel","text":"Let me tell you about my holiday. I spent the week swimming and eating ice cream. Then we toured museums and went shopping."}')`, id, org)
			got, err := h.question(ctx, &Claims{SessionID: id, OrgID: org}, assignment, "oral", tc.speaking)
			if (err != nil) != tc.fail || (got != "") != tc.question {
				t.Fatalf("question=%q err=%v", got, err)
			}
			if !tc.fail && (!strings.Contains(captured, "signed contract") || strings.Contains(captured, "What is the proportionality standard?") || strings.Contains(captured, "PRIVATE MATERIAL")) {
				t.Fatalf("case grounding missing or zero-relevance bank candidate included: %s", captured)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM session_events WHERE session_id=$1 AND type='judge_question'`, id).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count > 0) != tc.question {
				t.Fatal("question persistence disagrees with response")
			}
			if tc.mode == "continue" {
				var memory string
				if err := pool.QueryRow(ctx, `SELECT payload->>'memory' FROM session_events WHERE session_id=$1 AND type='judge_memory'`, id).Scan(&memory); err != nil || memory == "" {
					t.Fatal("conversation memory was not saved")
				}
				captured = ""
				if _, err := h.question(ctx, &Claims{SessionID: id, OrgID: org}, assignment, "oral", false); err != nil || captured != "" {
					t.Fatal("same speech triggered another model call")
				}
				exec(`UPDATE sessions SET last_seq=last_seq+1 WHERE id=$1`, id)
				exec(`INSERT INTO session_events(session_id,seq,organization_id,type,payload) SELECT id,last_seq,organization_id,'transcript_final','{"text":"Now I return to the contract."}' FROM sessions WHERE id=$1`, id)
				if _, err := h.question(ctx, &Claims{SessionID: id, OrgID: org}, assignment, "oral", false); err != nil || !strings.Contains(captured, "Contract enforceability remains the issue.") {
					t.Fatal("earlier memory did not reach the next decision")
				}
			}
		})
	}
	if modelURL := os.Getenv("JUDGE_TEST_MODEL_URL"); modelURL != "" {
		model := os.Getenv("JUDGE_TEST_MODEL")
		if model == "" {
			t.Fatal("set JUDGE_TEST_MODEL with JUDGE_TEST_MODEL_URL")
		}
		exec(`UPDATE model_providers SET base_url=$2 WHERE id=$1`, provider, modelURL)
		exec(`UPDATE model_bindings SET model=$1,temperature=0,timeout_ms=10000 WHERE provider_id=$2`, model, provider)
		exec(`UPDATE ai_profiles SET temperature=0 WHERE id=$1`, profile)
		for _, tc := range []struct {
			name, style, speech string
			speaking, question  bool
		}{
			{"relevant", "balanced", "Clause 4 requires written notice before termination. We submit that the signed contract remains enforceable because no written notice was given. I will now address the effect of that notice requirement.", false, false},
			{"off_topic", "balanced", "Let me tell you about my holiday. I spent the week swimming and eating ice cream. Then we toured museums and went shopping. The hotel breakfast was lovely and I bought souvenirs for my family.", true, true},
			{"patient_speaking", "patient", "Let me tell you about my holiday. I spent the week swimming and eating ice cream. Then we toured museums and went shopping. The hotel breakfast was lovely and I bought souvenirs for my family.", true, false},
			{"patient_pause", "patient", "Let me tell you about my holiday. I spent the week swimming and eating ice cream. Then we toured museums and went shopping. The hotel breakfast was lovely and I bought souvenirs for my family.", false, true},
			{"answered", "balanced", "Clause 4 requires written notice before termination. The applicant received no written notice. That is why we say the purported termination was ineffective. I will now address the remedy.", false, false},
		} {
			t.Run("local_"+tc.name, func(t *testing.T) {
				exec(`UPDATE assessment_template_versions SET stages=jsonb_set(stages,'{0,config,judging_style}',to_jsonb($2::text)) WHERE id=$1`, version, tc.style)
				id := uuid.New()
				exec(`INSERT INTO sessions(id,organization_id,assignment_id,stage_id,status,scheduled_at,last_seq) VALUES($1,$2,$3,'oral','running',now(),1)`, id, org, assignment)
				defer pool.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
				exec(`INSERT INTO session_participants(organization_id,session_id,kind,ai_profile_id,role,display_name,is_presiding) VALUES($1,$2,'ai',$3,'judge','Judge',true)`, org, id, profile)
				if tc.name == "answered" {
					exec(`INSERT INTO session_events(session_id,seq,organization_id,type,payload) VALUES($1,0,$2,'judge_question','{"speaker":"Judge","text":"What does clause 4 require, and was written notice given?"}')`, id, org)
					// The earlier bench question must not suppress this monitor call through cooldown.
					exec(`UPDATE session_events SET created_at=now()-interval '1 minute' WHERE session_id=$1 AND seq=0`, id)
				}
				payload, _ := json.Marshal(map[string]string{"speaker": "Counsel", "text": tc.speech})
				exec(`INSERT INTO session_events(session_id,seq,organization_id,type,payload) VALUES($1,1,$2,'transcript_final',$3)`, id, org, payload)
				started := time.Now()
				got, err := h.question(ctx, &Claims{SessionID: id, OrgID: org}, assignment, "oral", tc.speaking)
				t.Logf("model=%s elapsed=%s question=%q", model, time.Since(started).Round(time.Millisecond), got)
				if err != nil || (got != "") != tc.question {
					t.Fatalf("want question=%t, got=%q err=%v", tc.question, got, err)
				}
				if got != "" && !strings.Contains(strings.ToLower(got), "contract") && !strings.Contains(strings.ToLower(got), "clause") {
					t.Fatalf("redirect does not identify a case issue: %q", got)
				}
			})
		}
	}
}
