package aiprofiles

import "testing"

func TestInterruptionPolicyValidation(t *testing.T) {
	for _, test := range []struct {
		policy map[string]any
		valid  bool
	}{
		{nil, true}, {map[string]any{"cooldown_s": 45, "max_per_stage": 8, "min_priority": .65}, true},
		{map[string]any{"cooldown_s": "45"}, false}, {map[string]any{"max_per_stage": 1.5}, false},
		{map[string]any{"min_priority": 2}, false}, {map[string]any{"cooldown_s": 0}, false},
	} {
		in := Input{Key: "judge", Name: "Judge", Role: "judge", ModelTier: "judge", InterruptionPolicy: test.policy}
		if (in.validate() == nil) != test.valid {
			t.Fatalf("policy %#v validity should be %v", test.policy, test.valid)
		}
	}
}
