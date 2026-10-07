package platformcfg

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validBinding() BindingInput {
	return BindingInput{
		Tier:        "judge",
		ProviderID:  uuid.New(),
		Model:       "qwen3:8b",
		Temperature: 0.4,
		TopP:        0.95,
		MaxTokens:   512,
		TimeoutMS:   3000,
	}
}

func TestValidateAcceptsAWorkingBinding(t *testing.T) {
	if problems := validBinding().Validate(); len(problems) != 0 {
		t.Fatalf("a valid binding was refused: %v", problems)
	}
}

// The bounds exist in two places on purpose: the CHECK constraint is the
// second line, and these messages are the first. If one drifts the operator
// gets a constraint name instead of a sentence.
func TestValidateRejectsOutOfRangeParameters(t *testing.T) {
	cases := map[string]func(*BindingInput){
		"temperature": func(b *BindingInput) { b.Temperature = 2.1 },
		"top_p":       func(b *BindingInput) { b.TopP = 0 },
		"max_tokens":  func(b *BindingInput) { b.MaxTokens = 0 },
		"timeout_ms":  func(b *BindingInput) { b.TimeoutMS = 100 },
		"model":       func(b *BindingInput) { b.Model = "   " },
		"provider_id": func(b *BindingInput) { b.ProviderID = uuid.Nil },
	}

	for field, break_ := range cases {
		b := validBinding()
		break_(&b)

		problems := b.Validate()
		if len(problems) == 0 {
			t.Errorf("%s: an invalid value was accepted", field)
			continue
		}
		if !strings.Contains(strings.Join(problems, " "), field) {
			t.Errorf("%s: the problem does not name the field: %v", field, problems)
		}
	}
}

func TestValidateRejectsAnUnknownTier(t *testing.T) {
	b := validBinding()
	b.Tier = "reasoner"

	problems := b.Validate()
	if len(problems) != 1 {
		t.Fatalf("expected one problem for an unknown tier, got %v", problems)
	}
	// The whole vocabulary, because the next question is always "then what?".
	for _, tier := range Tiers {
		if !strings.Contains(problems[0], tier) {
			t.Errorf("the message does not offer %q: %s", tier, problems[0])
		}
	}
}

func TestBaseURLMustCarryAScheme(t *testing.T) {
	if problems := baseURLProblems("ollama.example.com"); len(problems) == 0 {
		t.Fatal("a schemeless base URL was accepted; it would be parsed as a relative path")
	}
	if problems := baseURLProblems(" https://ollama.example.com "); len(problems) != 0 {
		t.Fatalf("surrounding whitespace was treated as invalid: %v", problems)
	}
}
