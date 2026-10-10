package submissions

import (
	"github.com/slmlabs/megamoot/apps/api/internal/spec"
	"strings"
	"testing"
)

func TestResourceValidation(t *testing.T) {
	participation := spec.Participation{Sides: []string{"candidate", "defence"}}
	for _, kind := range []string{"problem", "authority", "statute", "evidence", "guidance", "other"} {
		for _, visibility := range []string{"all", "staff", "candidate", "defence"} {
			if err := validateResource(strings.Repeat("é", 300), kind, visibility, participation); err != nil {
				t.Fatalf("rejected %s/%s: %v", kind, visibility, err)
			}
		}
	}
	for _, input := range []struct{ title, kind, visibility string }{
		{"", "problem", "all"}, {"   ", "problem", "all"}, {strings.Repeat("x", 301), "problem", "all"},
		{"Case", "unknown", "all"}, {"Case", "problem", "respondent"}, {"Case", "problem", ""},
	} {
		if validateResource(input.title, input.kind, input.visibility, participation) == nil {
			t.Fatalf("accepted invalid material: %+v", input)
		}
	}
}
