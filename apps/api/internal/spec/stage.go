// Package spec defines the configuration language for assessment templates.
//
// The workflow engine understands exactly five stage kinds. A moot court, a
// medical viva and a job interview are different stage lists over the same
// five kinds, which is where the platform's genericity comes from. Adding a
// sixth kind is a deliberate platform decision, not a configuration change.
package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Kind string

const (
	// KindWait is time-gated. Resources become visible; nothing is collected.
	KindWait Kind = "wait"
	// KindArtifactSubmission collects a document before a deadline.
	KindArtifactSubmission Kind = "artifact_submission"
	// KindLiveTurn is a real-time spoken exchange with AI or human actors.
	KindLiveTurn Kind = "live_turn"
	// KindAutomatedEvaluation scores a rubric subset offline.
	KindAutomatedEvaluation Kind = "automated_evaluation"
	// KindHumanReview requires a person to sign off before results publish.
	KindHumanReview Kind = "human_review"
)

func (k Kind) Valid() bool {
	switch k {
	case KindWait, KindArtifactSubmission, KindLiveTurn,
		KindAutomatedEvaluation, KindHumanReview:
		return true
	}
	return false
}

func AllKinds() []Kind {
	return []Kind{KindWait, KindArtifactSubmission, KindLiveTurn,
		KindAutomatedEvaluation, KindHumanReview}
}

// Stage is one entry in a template version's stage list.
//
// Offsets are relative to the assessment's opening time rather than absolute,
// so one template serves every cohort. The workflow engine resolves them into
// concrete timestamps when an assignment is created.
type Stage struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	Label string `json:"label"`

	OpensAfterS int `json:"opens_after_s,omitempty"`
	DueAfterS   int `json:"due_after_s,omitempty"`
	GraceS      int `json:"grace_s,omitempty"`

	// Config is validated against Kind. Keeping it raw here means adding a
	// field to one kind's config does not touch the other four.
	Config json.RawMessage `json:"config,omitempty"`
}

type WaitConfig struct {
	// Knowledge source kinds revealed while this stage is active.
	VisibleResources []string `json:"visible_resources,omitempty"`
	Instructions     string   `json:"instructions,omitempty"`
}

type ArtifactSubmissionConfig struct {
	Formats  []string `json:"formats"`
	MaxBytes int64    `json:"max_bytes"`
	// Once submitted, further versions are refused. Enforced by a conditional
	// UPDATE, not by an application-level check.
	LockOnSubmit bool `json:"lock_on_submit"`
	// Identifier of a deterministic structure checker, e.g. moot_memorial_v1.
	FormatRules  string `json:"format_rules,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

type InterruptionPolicy string

const (
	InterruptionsEnabled  InterruptionPolicy = "enabled"
	InterruptionsLimited  InterruptionPolicy = "limited"
	InterruptionsDisabled InterruptionPolicy = "disabled"
)

type LiveTurnConfig struct {
	DurationS int `json:"duration_s"`
	// Which speaker in the team holds the floor; 0 means any member.
	SpeakerOrder  int                `json:"speaker_order,omitempty"`
	Interruptions InterruptionPolicy `json:"interruptions"`
	// Warn the speaker this many seconds before time expires.
	WarnAtS []int `json:"warn_at_s,omitempty"`

	// ExtensionS is how long a granted extension runs. Zero disables
	// extensions entirely, which is what a rebuttal wants.
	//
	// A moot judge does not stop a speaker at the second; counsel asks for a
	// moment to conclude and the bench grants it or does not. Modelling that
	// is the difference between a timer and a courtroom.
	ExtensionS int `json:"extension_s,omitempty"`
	// MaxExtensions caps how many a speaker may be granted in this stage.
	// The cap lives here, in code the student cannot talk to, rather than in
	// a prompt that can be argued with.
	MaxExtensions int `json:"max_extensions,omitempty"`

	// AI profile keys seated for this stage. Empty means every profile the
	// assessment configures.
	AIProfiles []string `json:"ai_profiles,omitempty"`
}

// Extensions reports whether a speaker may ask for more time in this stage,
// and how much in total.
func (c LiveTurnConfig) Extensions() (perGrant, max int, allowed bool) {
	if c.ExtensionS <= 0 || c.MaxExtensions <= 0 {
		return 0, 0, false
	}
	return c.ExtensionS, c.MaxExtensions, true
}

type AutomatedEvaluationConfig struct {
	// Rubric criterion keys this stage produces scores for.
	RubricScope []string `json:"rubric_scope"`
	// Stage ids whose output is evaluated. Empty means every preceding stage.
	Sources []string `json:"sources,omitempty"`
}

type HumanReviewConfig struct {
	Required         bool `json:"required"`
	OverridesAllowed bool `json:"overrides_allowed"`
}

var stageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,48}$`)

// maxStageDurationS caps a single live stage at four hours. Anything longer is
// a configuration mistake, and an uncapped value would hold a GPU slot open
// indefinitely.
const maxStageDurationS = 4 * 60 * 60

// ValidateStages checks a full stage list against the rubric it will be scored
// with. Every problem is reported at once, because fixing template
// configuration one error per save is miserable.
func ValidateStages(stages []Stage, criterionKeys []string) error {
	var problems []string

	if len(stages) == 0 {
		return &ValidationError{Problems: []string{"a template needs at least one stage"}}
	}

	seen := make(map[string]struct{}, len(stages))
	scored := make(map[string]struct{}, len(criterionKeys))
	known := make(map[string]struct{}, len(criterionKeys))
	for _, key := range criterionKeys {
		known[key] = struct{}{}
	}

	stageIDs := make(map[string]struct{}, len(stages))
	for _, s := range stages {
		stageIDs[s.ID] = struct{}{}
	}

	for i, s := range stages {
		where := fmt.Sprintf("stage %d (%q)", i+1, s.ID)

		if !stageIDPattern.MatchString(s.ID) {
			problems = append(problems, fmt.Sprintf(
				"%s: id must be lowercase letters, digits and underscores, 2 to 49 characters", where))
		}
		if _, dup := seen[s.ID]; dup {
			problems = append(problems, fmt.Sprintf("%s: duplicate stage id", where))
		}
		seen[s.ID] = struct{}{}

		if strings.TrimSpace(s.Label) == "" {
			problems = append(problems, fmt.Sprintf("%s: label is required", where))
		}
		if !s.Kind.Valid() {
			problems = append(problems, fmt.Sprintf(
				"%s: unknown kind %q, expected one of %v", where, s.Kind, AllKinds()))
			continue
		}

		if s.OpensAfterS < 0 || s.DueAfterS < 0 || s.GraceS < 0 {
			problems = append(problems, fmt.Sprintf("%s: time offsets cannot be negative", where))
		}
		if s.DueAfterS > 0 && s.DueAfterS <= s.OpensAfterS {
			problems = append(problems, fmt.Sprintf(
				"%s: due_after_s must be later than opens_after_s", where))
		}

		problems = append(problems, validateConfig(where, s, known, stageIDs, scored)...)
	}

	// Every criterion must be produced by some evaluation stage, or a student
	// receives a report with a silently missing score.
	for _, key := range criterionKeys {
		if _, ok := scored[key]; !ok {
			problems = append(problems, fmt.Sprintf(
				"rubric criterion %q is not scored by any automated_evaluation stage", key))
		}
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func validateConfig(where string, s Stage, knownCriteria, stageIDs map[string]struct{},
	scored map[string]struct{}) []string {

	var problems []string
	raw := s.Config
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}

	decode := func(dst any) bool {
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(dst); err != nil {
			problems = append(problems, fmt.Sprintf("%s: invalid config: %v", where, err))
			return false
		}
		return true
	}

	switch s.Kind {
	case KindWait:
		var c WaitConfig
		decode(&c)

	case KindArtifactSubmission:
		var c ArtifactSubmissionConfig
		if !decode(&c) {
			break
		}
		if len(c.Formats) == 0 {
			problems = append(problems, fmt.Sprintf("%s: at least one accepted format is required", where))
		}
		for _, f := range c.Formats {
			if !allowedUploadFormats[f] {
				problems = append(problems, fmt.Sprintf("%s: unsupported format %q", where, f))
			}
		}
		if c.MaxBytes <= 0 || c.MaxBytes > maxUploadBytes {
			problems = append(problems, fmt.Sprintf(
				"%s: max_bytes must be between 1 and %d", where, maxUploadBytes))
		}
		if s.DueAfterS == 0 {
			problems = append(problems, fmt.Sprintf("%s: a submission stage needs due_after_s", where))
		}

	case KindLiveTurn:
		var c LiveTurnConfig
		if !decode(&c) {
			break
		}
		if c.DurationS <= 0 || c.DurationS > maxStageDurationS {
			problems = append(problems, fmt.Sprintf(
				"%s: duration_s must be between 1 and %d", where, maxStageDurationS))
		}
		switch c.Interruptions {
		case InterruptionsEnabled, InterruptionsLimited, InterruptionsDisabled:
		case "":
			problems = append(problems, fmt.Sprintf("%s: interruptions policy is required", where))
		default:
			problems = append(problems, fmt.Sprintf(
				"%s: unknown interruptions policy %q", where, c.Interruptions))
		}
		if c.SpeakerOrder < 0 {
			problems = append(problems, fmt.Sprintf("%s: speaker_order cannot be negative", where))
		}
		for _, warn := range c.WarnAtS {
			if warn <= 0 || warn >= c.DurationS {
				problems = append(problems, fmt.Sprintf(
					"%s: warn_at_s %d must fall inside the stage duration", where, warn))
			}
		}

		if c.ExtensionS < 0 || c.MaxExtensions < 0 {
			problems = append(problems, fmt.Sprintf(
				"%s: extension_s and max_extensions cannot be negative", where))
		}
		if (c.ExtensionS > 0) != (c.MaxExtensions > 0) {
			problems = append(problems, fmt.Sprintf(
				"%s: extension_s and max_extensions must be set together, or neither", where))
		}
		// The cap is what makes an extension a concession rather than an
		// unbounded clock, so the total granted time stays inside the GPU
		// slot the stage reserved.
		if total := c.DurationS + c.ExtensionS*c.MaxExtensions; total > maxStageDurationS {
			problems = append(problems, fmt.Sprintf(
				"%s: duration_s plus every extension is %ds, above the %ds ceiling",
				where, total, maxStageDurationS))
		}

	case KindAutomatedEvaluation:
		var c AutomatedEvaluationConfig
		if !decode(&c) {
			break
		}
		if len(c.RubricScope) == 0 {
			problems = append(problems, fmt.Sprintf("%s: rubric_scope cannot be empty", where))
		}
		for _, key := range c.RubricScope {
			if _, ok := knownCriteria[key]; !ok {
				problems = append(problems, fmt.Sprintf(
					"%s: rubric_scope references unknown criterion %q", where, key))
				continue
			}
			if _, dup := scored[key]; dup {
				problems = append(problems, fmt.Sprintf(
					"%s: criterion %q is already scored by an earlier stage", where, key))
			}
			scored[key] = struct{}{}
		}
		for _, src := range c.Sources {
			if _, ok := stageIDs[src]; !ok {
				problems = append(problems, fmt.Sprintf(
					"%s: sources references unknown stage %q", where, src))
			}
		}

	case KindHumanReview:
		var c HumanReviewConfig
		decode(&c)
	}

	return problems
}

const maxUploadBytes int64 = 50 << 20 // 50 MiB

var allowedUploadFormats = map[string]bool{
	"pdf":  true,
	"docx": true,
	"txt":  true,
	"md":   true,
}

type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid stage configuration: " + strings.Join(e.Problems, "; ")
}

// DecodeStages parses the stage list stored on a template version.
func DecodeStages(raw []byte) ([]Stage, error) {
	var stages []Stage
	if err := json.Unmarshal(raw, &stages); err != nil {
		return nil, fmt.Errorf("decode stage list: %w", err)
	}
	return stages, nil
}

// Find returns the stage with the given id.
func Find(stages []Stage, id string) (Stage, bool) {
	for _, s := range stages {
		if s.ID == id {
			return s, true
		}
	}
	return Stage{}, false
}

// DecodeConfig unmarshals a stage's config into a kind-specific struct.
func DecodeConfig[T any](s Stage) (T, error) {
	var out T
	raw := s.Config
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode config for stage %q: %w", s.ID, err)
	}
	return out, nil
}
