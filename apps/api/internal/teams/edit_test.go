package teams

import (
	"github.com/google/uuid"
	"testing"
)

func TestMembershipComparison(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	one, two := 1, 2
	a := []MemberInput{{first, RoleSpeaker, &one}, {second, RoleSpeaker, &two}}
	if !sameMembers(a, []MemberInput{a[1], a[0]}) {
		t.Fatal("row ordering changed membership")
	}
	for _, b := range [][]MemberInput{{a[0]}, {{first, RoleSpeaker, &two}, {second, RoleSpeaker, &one}}, {{first, RoleResearcher, nil}, a[1]}, {{uuid.New(), RoleSpeaker, &one}, a[1]}} {
		if sameMembers(a, b) {
			t.Fatal("missed membership or speaking order change")
		}
	}
}
