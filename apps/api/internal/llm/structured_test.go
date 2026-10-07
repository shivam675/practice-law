package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

type score struct {
	CriterionID string `json:"criterion_id"`
	Score       int    `json:"score"`
	Max         int    `json:"max_score"`
}

func (s score) Validate() error {
	if s.Score < 0 || s.Score > s.Max {
		return errors.New("score is outside 0..max_score")
	}
	return nil
}

func TestExtractJSONSurvivesWhatModelsActuallyEmit(t *testing.T) {
	want := `{"criterion_id":"legal_reasoning","score":16,"max_score":20}`

	cases := map[string]string{
		"bare":            want,
		"fenced":          "```json\n" + want + "\n```",
		"unlabelledFence": "```\n" + want + "\n```",
		"preamble":        "Here is the result:\n\n" + want,
		"trailingProse":   want + "\n\nLet me know if you want a different weighting.",
		"thinkBlock":      "<think>the quote is on page 4</think>\n" + want,
		"bracesInProse":   want + "\n\nNote: use {} for an empty object.",
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			var got score
			if err := decodeInto(in, &got); err != nil {
				t.Fatalf("decodeInto: %v", err)
			}
			if got.CriterionID != "legal_reasoning" || got.Score != 16 || got.Max != 20 {
				t.Fatalf("decoded %+v", got)
			}
		})
	}
}

// A model that writes prose containing a closing brace before the object ends
// is the case LastIndex gets wrong, so the matcher counts instead.
func TestExtractJSONStopsAtTheFirstCompleteValue(t *testing.T) {
	in := `{"reasoning":"counsel wrote \"} end\" in the memorial","score":3,"max_score":5,"criterion_id":"x"}` +
		"\n\nI hope that helps."

	var got score
	if err := decodeInto(in, &got); err != nil {
		t.Fatalf("decodeInto: %v", err)
	}
	if got.Score != 3 {
		t.Fatalf("score = %d, want 3", got.Score)
	}
}

func TestExtractJSONHandlesArrays(t *testing.T) {
	var got []score
	if err := decodeInto("```json\n[{\"criterion_id\":\"a\",\"score\":1,\"max_score\":2}]\n```", &got); err != nil {
		t.Fatalf("decodeInto: %v", err)
	}
	if len(got) != 1 || got[0].CriterionID != "a" {
		t.Fatalf("decoded %+v", got)
	}
}

// Well-formed JSON that is nonetheless wrong must fail here, so the repair
// round gets a chance at it rather than the caller storing an impossible mark.
func TestValidatorRejectsAnOutOfBoundsScore(t *testing.T) {
	var got score
	err := decodeInto(`{"criterion_id":"legal_reasoning","score":25,"max_score":20}`, &got)
	if err == nil {
		t.Fatal("a score above max_score must not decode")
	}
}

func TestDecodeRefusesWhenThereIsNoObject(t *testing.T) {
	var got score
	if err := decodeInto("I am unable to grade this submission.", &got); err == nil {
		t.Fatal("prose with no object must be refused")
	}
}

// Unknown fields are a style disagreement. Paying for a second call over one
// would be the expensive kind of strictness.
func TestUnknownFieldsAreTolerated(t *testing.T) {
	var got score
	in := `{"criterion_id":"a","score":1,"max_score":2,"confidence":0.9}`
	if err := decodeInto(in, &got); err != nil {
		t.Fatalf("decodeInto: %v", err)
	}
	if got.Score != 1 {
		t.Fatalf("decoded %+v", got)
	}
}

func TestVectorRendersPgvectorLiteral(t *testing.T) {
	if got := Vector([]float32{0.5, -0.25, 0}); got != "[0.5,-0.25,0]" {
		t.Fatalf("Vector = %q", got)
	}
	if got := Vector(nil); got != "[]" {
		t.Fatalf("Vector(nil) = %q", got)
	}
}

// A batching server may answer out of order. Trusting the array's order would
// transpose two vectors and silently return the wrong chunk at retrieval time,
// which nothing downstream could detect.
func TestEmbedPlacesVectorsByIndexNotByArrayOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("called %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"model":"bge-m3","data":[
			{"index":1,"embedding":[9,9]},
			{"index":0,"embedding":[1,1]}],
			"usage":{"prompt_tokens":4}}`)
	}))
	defer srv.Close()

	got, err := New(Provider{BaseURL: srv.URL}).Embed(context.Background(),
		EmbedRequest{Model: "bge-m3", Input: []string{"first", "second"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if got.Vectors[0][0] != 1 || got.Vectors[1][0] != 9 {
		t.Fatalf("vectors not in input order: %v", got.Vectors)
	}
	if got.InputTokens != 4 {
		t.Fatalf("InputTokens = %d, want 4", got.InputTokens)
	}
}

// Fewer vectors than inputs means a silent misalignment for every chunk after
// the gap, so it is refused rather than zipped up.
func TestEmbedRefusesAShortResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,1]}]}`)
	}))
	defer srv.Close()

	_, err := New(Provider{BaseURL: srv.URL}).Embed(context.Background(),
		EmbedRequest{Model: "bge-m3", Input: []string{"a", "b"}})
	if err == nil {
		t.Fatal("a short embeddings response must be refused")
	}
}

func TestStructuredRepairsOnceThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Messages []Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		reply := "I cannot produce JSON for this."
		if calls == 2 {
			// The repair round must carry the bad answer and the complaint.
			if len(body.Messages) < 2 {
				t.Errorf("repair round sent %d messages", len(body.Messages))
			}
			reply = `{"criterion_id":"a","score":1,"max_score":2}`
		}
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"role":"assistant","content":`+
			strconv.Quote(reply)+`}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}))
	defer srv.Close()

	var got score
	resp, err := New(Provider{BaseURL: srv.URL}).Structured(context.Background(),
		ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "grade"}}}, &got)
	if err != nil {
		t.Fatalf("Structured: %v", err)
	}
	if calls != 2 {
		t.Fatalf("made %d calls, want exactly 2", calls)
	}
	if !resp.Repaired {
		t.Fatal("Repaired must be set so the ledger and the logs say so")
	}
	if resp.InputTokens != 20 || resp.OutputTokens != 10 {
		t.Fatalf("usage %d/%d: both attempts must be billed", resp.InputTokens, resp.OutputTokens)
	}
	if got.Score != 1 {
		t.Fatalf("decoded %+v", got)
	}
}

// Two failures is the end of it. A live session cannot afford a retry loop,
// and the caller degrades on ErrUnstructured rather than trying again.
func TestStructuredGivesUpAfterOneRepair(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":`+
			`{"role":"assistant","content":"still no."}}],"usage":{}}`)
	}))
	defer srv.Close()

	var got score
	_, err := New(Provider{BaseURL: srv.URL}).Structured(context.Background(),
		ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "grade"}}}, &got)
	if !errors.Is(err, ErrUnstructured) {
		t.Fatalf("err = %v, want ErrUnstructured", err)
	}
	if calls != 2 {
		t.Fatalf("made %d calls, want exactly 2", calls)
	}
}
