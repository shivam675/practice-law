package spec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Participation describes who is in the room: how many human speakers, which
// sides exist, and which AI actors are seated.
//
// A solo candidate is a team of one, so interviews and vivas use the same
// shape as a two-speaker moot. Student-versus-student later needs no change
// here either: it is two teams on opposing sides of the same session.
type Participation struct {
	// Side keys an assignment can take. One side means no opposing party.
	Sides []string `json:"sides"`
	// Human speakers per team who hold the floor during live stages.
	Speakers int `json:"speakers"`
	// Team size bounds including non-speaking members such as a researcher.
	MinTeamSize int `json:"min_team_size"`
	MaxTeamSize int `json:"max_team_size"`
	// AI actors seated for the whole assessment.
	AIActors []AIActorSlot `json:"ai_actors"`
}

type AIActorSlot struct {
	// Profile key resolved against the organisation's ai_profiles.
	ProfileKey  string `json:"profile_key"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
	// Exactly one actor arbitrates the speaking floor when several can speak.
	Presiding bool `json:"presiding"`
}

var validAIRoles = map[string]bool{
	"judge":     true,
	"examiner":  true,
	"moderator": true,
	"opponent":  true,
	"evaluator": true,
}

func (p Participation) Validate() error {
	var problems []string

	if len(p.Sides) == 0 {
		problems = append(problems, "at least one side is required")
	}
	seenSides := map[string]struct{}{}
	for _, side := range p.Sides {
		if strings.TrimSpace(side) == "" {
			problems = append(problems, "side names cannot be blank")
			continue
		}
		if _, dup := seenSides[side]; dup {
			problems = append(problems, fmt.Sprintf("duplicate side %q", side))
		}
		seenSides[side] = struct{}{}
	}

	if p.Speakers < 1 {
		problems = append(problems, "at least one speaker is required")
	}
	if p.MinTeamSize < p.Speakers {
		problems = append(problems, "min_team_size cannot be smaller than the number of speakers")
	}
	if p.MaxTeamSize < p.MinTeamSize {
		problems = append(problems, "max_team_size cannot be smaller than min_team_size")
	}

	presiding := 0
	seenProfiles := map[string]struct{}{}
	for i, actor := range p.AIActors {
		where := fmt.Sprintf("ai_actors[%d]", i)
		if strings.TrimSpace(actor.ProfileKey) == "" {
			problems = append(problems, where+": profile_key is required")
		}
		if _, dup := seenProfiles[actor.ProfileKey]; dup {
			problems = append(problems, fmt.Sprintf("%s: duplicate profile_key %q", where, actor.ProfileKey))
		}
		seenProfiles[actor.ProfileKey] = struct{}{}

		if !validAIRoles[actor.Role] {
			problems = append(problems, fmt.Sprintf("%s: unknown role %q", where, actor.Role))
		}
		if strings.TrimSpace(actor.DisplayName) == "" {
			problems = append(problems, where+": display_name is required")
		}
		if actor.Presiding {
			presiding++
		}
	}

	// Without exactly one arbiter the speaking floor has no owner and agents
	// talk over each other. See ADR 0008.
	if len(p.AIActors) > 0 && presiding != 1 {
		problems = append(problems, fmt.Sprintf(
			"exactly one AI actor must be presiding, found %d", presiding))
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func DecodeParticipation(raw []byte) (Participation, error) {
	var p Participation
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("decode participation: %w", err)
	}
	return p, nil
}

func (p Participation) HasSide(side string) bool {
	for _, s := range p.Sides {
		if s == side {
			return true
		}
	}
	return false
}
