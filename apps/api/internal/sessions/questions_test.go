package sessions

import (
	"github.com/slmlabs/megamoot/apps/api/internal/spec"
	"strings"
	"testing"
	"time"
)

func TestInterruptionLimits(t *testing.T) {
	now := time.Now()
	p := questionPolicy{CooldownS: 45, MaxPerStage: 8, MinPriority: .65}
	if p.allowed(spec.InterruptionsDisabled, 0, time.Time{}, now, .9) {
		t.Fatal("disabled interruption")
	}
	if p.allowed(spec.InterruptionsLimited, 2, time.Time{}, now, .9) {
		t.Fatal("limited quota")
	}
	if p.allowed(spec.InterruptionsEnabled, 8, time.Time{}, now, .9) {
		t.Fatal("profile quota")
	}
	if p.allowed(spec.InterruptionsEnabled, 1, now.Add(-10*time.Second), now, .9) {
		t.Fatal("cooldown")
	}
	if p.allowed(spec.InterruptionsEnabled, 0, time.Time{}, now, .2) {
		t.Fatal("priority threshold")
	}
	if !p.allowed(spec.InterruptionsEnabled, 0, time.Time{}, now, .9) {
		t.Fatal("valid bid refused")
	}
}

func TestMonitorRequiresGroundedReasonAndRespectsStyle(t *testing.T) {
	b := monitorBid{Action: "interrupt", Priority: .9, Seed: "relevance", Reason: "off_topic", Quote: "my holiday"}
	if !b.shouldInterrupt("balanced", true, "I want to discuss my holiday.") {
		t.Fatal("sustained drift should allow an urgent interruption")
	}
	if b.shouldInterrupt("patient", true, "my holiday") {
		t.Fatal("patient bench must wait for a pause")
	}
	b.Reason = "unsupported_claim"
	if b.shouldInterrupt("balanced", true, "my holiday") || !b.shouldInterrupt("strict", true, "my holiday") {
		t.Fatal("ordinary challenges during speech depend on judging style")
	}
	b.Quote = "invented words"
	if b.shouldInterrupt("strict", false, "my holiday") {
		t.Fatal("a fabricated quote must not trigger a question")
	}
	if (monitorBid{}).shouldInterrupt("balanced", false, "my holiday") {
		t.Fatal("monitor failure must remain silent")
	}
}

func TestLiveQuestionRejectsUngroundedOrLongOutput(t *testing.T) {
	q := liveQuestion{Text: "How does your holiday relate to the case?", Quote: "my holiday"}
	if !q.grounded("I mentioned my holiday.") {
		t.Fatal("valid question rejected")
	}
	if q.grounded("The statute applies.") {
		t.Fatal("invented grounding accepted")
	}
	q.Text = strings.Repeat("word ", 31) + "?"
	if q.grounded("my holiday") {
		t.Fatal("long question accepted")
	}
}
