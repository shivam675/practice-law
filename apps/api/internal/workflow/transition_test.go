package workflow

import (
	"sort"
	"testing"
)

func TestAllowedTransitions(t *testing.T) {
	cases := []struct {
		subject  Subject
		from, to string
		want     bool
	}{
		{SubjectAssignment, AssignmentAssigned, AssignmentInProgress, true},
		{SubjectAssignment, AssignmentInProgress, AssignmentFinalized, false},
		{SubjectAssignment, AssignmentFinalized, AssignmentAbandoned, false},
		{SubjectAssignment, AssignmentAbandoned, AssignmentInProgress, true},

		{SubjectStage, StagePending, StageActive, true},
		{SubjectStage, StagePending, StageCompleted, false},
		{SubjectStage, StageActive, StageGrace, true},
		{SubjectStage, StageGrace, StageCompleted, true},
		{SubjectStage, StageCompleted, StageActive, false},
		{SubjectStage, StageExpired, StageCompleted, true},

		{SubjectSession, SessionScheduled, SessionRunning, false},
		{SubjectSession, SessionDeviceCheck, SessionRunning, true},
		{SubjectSession, SessionRunning, SessionDegraded, true},
		{SubjectSession, SessionDegraded, SessionEnded, true},
		{SubjectSession, SessionEnded, SessionRunning, false},
		{SubjectSession, SessionEvaluated, SessionEvaluating, true},
	}

	for _, c := range cases {
		if got := Allowed(c.subject, c.from, c.to); got != c.want {
			t.Errorf("Allowed(%s, %s -> %s) = %v, want %v",
				c.subject, c.from, c.to, got, c.want)
		}
	}
}

func TestUnknownSubjectIsNeverAllowed(t *testing.T) {
	if Allowed("moot_round", "a", "b") {
		t.Fatal("an unknown subject must not permit transitions")
	}
}

// A state with no way out is a trap: whatever reaches it can never progress,
// and in this system that means a student stuck mid-assessment.
func TestEveryStateIsReachableAndEscapable(t *testing.T) {
	terminal := map[Subject]map[string]bool{
		SubjectAssignment: {AssignmentWithdrawn: true},
		SubjectStage:      {StageCompleted: true, StageSkipped: true},
		SubjectSession:    {SessionEvaluated: true},
	}

	for subject, m := range machines {
		outgoing := map[string]int{}
		incoming := map[string]int{}
		for e := range m {
			outgoing[e.from]++
			incoming[e.to]++
		}

		for _, state := range States(subject) {
			isStart := startState(subject) == state
			if !isStart && incoming[state] == 0 {
				t.Errorf("%s: state %q is unreachable", subject, state)
			}
			if !terminal[subject][state] && outgoing[state] == 0 {
				t.Errorf("%s: state %q is a dead end", subject, state)
			}
		}
	}
}

func startState(s Subject) string {
	switch s {
	case SubjectAssignment:
		return AssignmentAssigned
	case SubjectStage:
		return StagePending
	case SubjectSession:
		return SessionScheduled
	}
	return ""
}

func TestNoSelfTransitions(t *testing.T) {
	for subject, m := range machines {
		for e := range m {
			if e.from == e.to {
				t.Errorf("%s: self transition %q is never valid; Apply returns ErrNoop instead",
					subject, e.from)
			}
		}
	}
}

func TestStatesAreStable(t *testing.T) {
	got := States(SubjectStage)
	sort.Strings(got)
	want := []string{StageActive, StageCompleted, StageExpired, StageFailed,
		StageGrace, StagePending, StageSkipped}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("stage states = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("stage states = %v, want %v", got, want)
		}
	}
}

func TestTimestampColumns(t *testing.T) {
	cases := []struct {
		subject Subject
		to      string
		want    string
	}{
		{SubjectStage, StageActive, "started_at"},
		{SubjectStage, StageCompleted, "completed_at"},
		{SubjectStage, StagePending, ""},
		{SubjectSession, SessionRunning, "started_at"},
		{SubjectSession, SessionEnded, "ended_at"},
		{SubjectAssignment, AssignmentFinalized, "finalized_at"},
		{SubjectAssignment, AssignmentInProgress, ""},
	}
	for _, c := range cases {
		if got := timestampColumn(c.subject, c.to); got != c.want {
			t.Errorf("timestampColumn(%s, %s) = %q, want %q", c.subject, c.to, got, c.want)
		}
	}
}

// Table names reach SQL through string concatenation, so they must come only
// from this fixed map and never from a request.
func TestSubjectTablesCoverEveryMachine(t *testing.T) {
	for subject := range machines {
		if _, ok := subjectTables[subject]; !ok {
			t.Errorf("subject %q has a machine but no table mapping", subject)
		}
	}
	for subject := range subjectTables {
		if _, ok := machines[subject]; !ok {
			t.Errorf("subject %q has a table mapping but no machine", subject)
		}
	}
}
