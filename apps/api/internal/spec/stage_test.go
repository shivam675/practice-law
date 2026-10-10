package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

func stage(id string, kind Kind, cfg any, mutate ...func(*Stage)) Stage {
	raw, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	s := Stage{ID: id, Kind: kind, Label: strings.ToUpper(id), Config: raw}
	for _, m := range mutate {
		m(&s)
	}
	return s
}

func validMootStages() []Stage {
	return []Stage{
		stage("preparation", KindWait, WaitConfig{VisibleResources: []string{"problem"}}),
		stage("memorial", KindArtifactSubmission, ArtifactSubmissionConfig{
			Formats: []string{"pdf", "docx"}, MaxBytes: 25 << 20, LockOnSubmit: true,
		}, func(s *Stage) { s.DueAfterS = 604800 }),
		stage("memorial_eval", KindAutomatedEvaluation, AutomatedEvaluationConfig{
			RubricScope: []string{"legal_reasoning", "authorities"},
		}),
		stage("oral", KindLiveTurn, LiveTurnConfig{
			DurationS: 720, SpeakerOrder: 1, Interruptions: InterruptionsEnabled,
			WarnAtS: []int{120, 60},
		}),
		stage("oral_eval", KindAutomatedEvaluation, AutomatedEvaluationConfig{
			RubricScope: []string{"advocacy"},
		}),
		stage("moderation", KindHumanReview, HumanReviewConfig{Required: true}),
	}
}

var mootCriteria = []string{"legal_reasoning", "authorities", "advocacy"}

func TestValidateStagesAcceptsAWellFormedMoot(t *testing.T) {
	if err := ValidateStages(validMootStages(), mootCriteria); err != nil {
		t.Fatalf("expected valid stage list, got: %v", err)
	}
}

func TestValidateStagesRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func([]Stage) []Stage
		wantSub string
	}{
		{
			name: "duplicate stage id",
			mutate: func(s []Stage) []Stage {
				s[2].ID = "memorial"
				return s
			},
			wantSub: "duplicate stage id",
		},
		{
			name: "unknown kind",
			mutate: func(s []Stage) []Stage {
				s[0].Kind = "moot_rebuttal"
				return s
			},
			wantSub: "unknown kind",
		},
		{
			name: "criterion scored twice",
			mutate: func(s []Stage) []Stage {
				s[4] = stage("oral_eval", KindAutomatedEvaluation, AutomatedEvaluationConfig{
					RubricScope: []string{"advocacy", "authorities"},
				})
				return s
			},
			wantSub: "already scored by an earlier stage",
		},
		{
			name: "criterion never scored",
			mutate: func(s []Stage) []Stage {
				s[4] = stage("oral_eval", KindAutomatedEvaluation, AutomatedEvaluationConfig{
					RubricScope: []string{"legal_reasoning"},
				})
				s[2] = stage("memorial_eval", KindAutomatedEvaluation, AutomatedEvaluationConfig{
					RubricScope: []string{"authorities"},
				})
				return s
			},
			wantSub: `"advocacy" is not scored`,
		},
		{
			name: "unknown criterion",
			mutate: func(s []Stage) []Stage {
				s[2] = stage("memorial_eval", KindAutomatedEvaluation, AutomatedEvaluationConfig{
					RubricScope: []string{"legal_reasoning", "authorities", "vibes"},
				})
				return s
			},
			wantSub: "unknown criterion",
		},
		{
			name: "submission without a deadline",
			mutate: func(s []Stage) []Stage {
				s[1].DueAfterS = 0
				return s
			},
			wantSub: "needs due_after_s",
		},
		{
			name: "unsupported upload format",
			mutate: func(s []Stage) []Stage {
				s[1] = stage("memorial", KindArtifactSubmission, ArtifactSubmissionConfig{
					Formats: []string{"exe"}, MaxBytes: 1024,
				}, func(st *Stage) { st.DueAfterS = 600 })
				return s
			},
			wantSub: `unsupported format "exe"`,
		},
		{
			name: "live stage longer than the cap",
			mutate: func(s []Stage) []Stage {
				s[3] = stage("oral", KindLiveTurn, LiveTurnConfig{
					DurationS: 99999, Interruptions: InterruptionsEnabled,
				})
				return s
			},
			wantSub: "duration_s must be between",
		},
		{
			name: "missing interruption policy",
			mutate: func(s []Stage) []Stage {
				s[3] = stage("oral", KindLiveTurn, LiveTurnConfig{DurationS: 600})
				return s
			},
			wantSub: "interruptions policy is required",
		},
		{
			name: "warning outside the stage duration",
			mutate: func(s []Stage) []Stage {
				s[3] = stage("oral", KindLiveTurn, LiveTurnConfig{
					DurationS: 60, Interruptions: InterruptionsEnabled, WarnAtS: []int{600},
				})
				return s
			},
			wantSub: "must fall inside the stage duration",
		},
		{
			name: "unknown config field",
			mutate: func(s []Stage) []Stage {
				s[3].Config = json.RawMessage(`{"duration_s":600,"interruptions":"enabled","judge_question":true}`)
				return s
			},
			wantSub: "invalid config",
		},
		{
			name: "due before opening",
			mutate: func(s []Stage) []Stage {
				s[1].OpensAfterS = 900000
				return s
			},
			wantSub: "must be later than opens_after_s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStages(tc.mutate(validMootStages()), mootCriteria)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantSub, err)
			}
		})
	}
}

func TestValidateStagesRejectsEmptyList(t *testing.T) {
	if err := ValidateStages(nil, mootCriteria); err == nil {
		t.Fatal("expected an empty stage list to be rejected")
	}
}

func TestValidateStagesReportsEveryProblemAtOnce(t *testing.T) {
	stages := validMootStages()
	stages[0].ID = "Bad ID"
	stages[0].Label = ""
	stages[1].DueAfterS = 0

	err := ValidateStages(stages, mootCriteria)
	if err == nil {
		t.Fatal("expected errors")
	}
	var v *ValidationError
	if !asValidationError(err, &v) {
		t.Fatalf("expected a ValidationError, got %T", err)
	}
	if len(v.Problems) < 3 {
		t.Fatalf("expected at least 3 problems reported together, got %d: %v",
			len(v.Problems), v.Problems)
	}
}

func asValidationError(err error, target **ValidationError) bool {
	v, ok := err.(*ValidationError)
	if ok {
		*target = v
	}
	return ok
}

func TestParticipationValidate(t *testing.T) {
	valid := Participation{
		Sides: []string{"applicant", "respondent"}, Speakers: 2,
		MinTeamSize: 2, MaxTeamSize: 3,
		AIActors: []AIActorSlot{
			{ProfileKey: "presiding_judge", Role: "judge", DisplayName: "Presiding Judge", Presiding: true},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid participation, got: %v", err)
	}

	noPresiding := valid
	noPresiding.AIActors = []AIActorSlot{
		{ProfileKey: "a", Role: "judge", DisplayName: "A"},
		{ProfileKey: "b", Role: "judge", DisplayName: "B"},
	}
	if err := noPresiding.Validate(); err == nil ||
		!strings.Contains(err.Error(), "exactly one AI actor must be presiding") {
		t.Fatalf("expected a presiding-judge error, got: %v", err)
	}

	tooSmall := valid
	tooSmall.MinTeamSize = 1
	if err := tooSmall.Validate(); err == nil ||
		!strings.Contains(err.Error(), "min_team_size") {
		t.Fatalf("expected a team size error, got: %v", err)
	}
}

func TestDecodeConfigRoundTrip(t *testing.T) {
	s := stage("oral", KindLiveTurn, LiveTurnConfig{
		DurationS: 720, SpeakerOrder: 2, Interruptions: InterruptionsLimited,
	})
	cfg, err := DecodeConfig[LiveTurnConfig](s)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.DurationS != 720 || cfg.SpeakerOrder != 2 ||
		cfg.Interruptions != InterruptionsLimited {
		t.Fatalf("round trip lost data: %+v", cfg)
	}
}

/* --------------------------------------------------------------- extensions */

func liveTurnWith(cfg LiveTurnConfig) []Stage {
	stages := validMootStages()
	for i := range stages {
		if stages[i].Kind == KindLiveTurn {
			stages[i] = stage(stages[i].ID, KindLiveTurn, cfg)
		}
	}
	return stages
}

var mootKeys = []string{"legal_reasoning", "authorities", "advocacy"}

func TestJudgingStyle(t *testing.T) {
	for _, style := range []string{"", "balanced", "strict", "patient", "unknown"} {
		err := ValidateStages(liveTurnWith(LiveTurnConfig{DurationS: 600, Interruptions: InterruptionsEnabled, JudgingStyle: style}), mootKeys)
		if (err != nil) != (style == "unknown") {
			t.Errorf("style %q: %v", style, err)
		}
	}
}

func TestExtensionsAreOptional(t *testing.T) {
	err := ValidateStages(liveTurnWith(LiveTurnConfig{
		DurationS: 720, Interruptions: InterruptionsEnabled,
	}), mootKeys)
	if err != nil {
		t.Fatalf("a live turn without extensions must be valid: %v", err)
	}
}

// Half a policy is a bug either way round: a grant length with no cap is an
// unbounded clock, and a cap with no length grants nothing.
func TestExtensionLengthAndCapMustBeSetTogether(t *testing.T) {
	for _, cfg := range []LiveTurnConfig{
		{DurationS: 720, Interruptions: InterruptionsEnabled, ExtensionS: 120},
		{DurationS: 720, Interruptions: InterruptionsEnabled, MaxExtensions: 2},
	} {
		err := ValidateStages(liveTurnWith(cfg), mootKeys)
		if err == nil {
			t.Fatalf("%+v was accepted", cfg)
		}
		if !strings.Contains(err.Error(), "must be set together") {
			t.Fatalf("unexpected problem: %v", err)
		}
	}
}

func TestExtensionsCannotPushAStagePastTheCeiling(t *testing.T) {
	err := ValidateStages(liveTurnWith(LiveTurnConfig{
		DurationS: maxStageDurationS, Interruptions: InterruptionsEnabled,
		ExtensionS: 600, MaxExtensions: 3,
	}), mootKeys)
	if err == nil {
		t.Fatal("extensions were allowed to exceed the stage ceiling")
	}
	if !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("unexpected problem: %v", err)
	}
}

func TestExtensionsAccessor(t *testing.T) {
	if _, _, ok := (LiveTurnConfig{}).Extensions(); ok {
		t.Fatal("an unset policy must not allow extensions")
	}
	if _, _, ok := (LiveTurnConfig{ExtensionS: 120}).Extensions(); ok {
		t.Fatal("a grant length with no cap must not allow extensions")
	}
	per, max, ok := LiveTurnConfig{ExtensionS: 120, MaxExtensions: 2}.Extensions()
	if !ok || per != 120 || max != 2 {
		t.Fatalf("Extensions() = %d, %d, %v", per, max, ok)
	}
}
