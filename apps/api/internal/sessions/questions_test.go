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
	b := monitorBid{Action: "interrupt", Priority: .7, Seed: "relevance", Reason: "off_topic", Quote: "my holiday"}
	if !b.shouldInterrupt("balanced", true, "I want to discuss my holiday.", "I want to discuss my holiday.") {
		t.Fatal("drift must be interrupted mid-speech whatever its priority")
	}
	b.Priority = .9
	if b.shouldInterrupt("patient", true, "my holiday", "my holiday") {
		t.Fatal("patient bench must wait for a pause")
	}
	b.Reason = "unsupported_claim"
	if b.shouldInterrupt("balanced", true, "my holiday", "my holiday") || !b.shouldInterrupt("strict", true, "my holiday", "my holiday") {
		t.Fatal("ordinary challenges during speech depend on judging style")
	}
	b.Quote = "invented words"
	if b.shouldInterrupt("strict", false, "my holiday", "my holiday") {
		t.Fatal("a fabricated quote must not trigger a question")
	}
	if (monitorBid{}).shouldInterrupt("balanced", false, "my holiday", "my holiday") {
		t.Fatal("monitor failure must remain silent")
	}
}

func TestLiveQuestionRejectsMalformedOutput(t *testing.T) {
	q := liveQuestion{Text: "How does your holiday relate to the case?"}
	if q.Validate() != nil {
		t.Fatal("valid question rejected")
	}
	for _, text := range []string{strings.Repeat("word ", 36) + "?", "What about damages? And costs?", "Tell me about damages."} {
		if (&liveQuestion{Text: text}).Validate() == nil {
			t.Fatalf("malformed question accepted: %q", text)
		}
	}
	if !spoken("Okay, so: the JUDGE is unavailable.", "the judge is unavailable") || spoken("the judge", "") || spoken("ajudge is", "judge is") {
		t.Fatal("quote matching must ignore punctuation and case but respect word boundaries")
	}
}

// The session that prompted these rules: counsel complained about the app and
// the monitor filed it as a legal contradiction with maximum priority.
func TestProceduralSpeechIsNeverALegalChallenge(t *testing.T) {
	latest := "okay so on the screen I see the jazz is unavailable my transcript will be saved that is"
	b := monitorBid{SpeechKind: "procedural", Action: "interrupt", Priority: 1, Reason: "contradiction", Seed: "less restrictive measures", Quote: "the jazz is unavailable"}
	b.normalize()
	if b.Reason != "procedural" {
		t.Fatalf("reason = %q, want procedural", b.Reason)
	}
	if !b.shouldInterrupt("balanced", true, latest, latest) || !b.shouldInterrupt("balanced", false, latest, latest) {
		t.Fatal("the bench should stop talk about the hearing at once")
	}
	if b.shouldInterrupt("patient", true, latest, latest) || !b.shouldInterrupt("patient", false, latest, latest) {
		t.Fatal("a patient bench waits for the pause")
	}
	argument := monitorBid{SpeechKind: "argument", Action: "interrupt", Priority: .9, Reason: "off_topic", Seed: "x", Quote: "y"}
	argument.normalize()
	if argument.Action != "continue" {
		t.Fatal("a legal argument must not be redirected as drift")
	}
}

func TestContradictionNeedsBothClaimsOnRecord(t *testing.T) {
	record := "Counsel: The measure was necessary.\nCounsel: No alternative existed, though a fine was available."
	latest := "No alternative existed, though a fine was available."
	b := monitorBid{SpeechKind: "argument", Action: "interrupt", Priority: .9, Reason: "contradiction", Seed: "necessity", Quote: "No alternative existed"}
	if b.shouldInterrupt("balanced", false, latest, record) {
		t.Fatal("a contradiction with no earlier claim must be dropped")
	}
	b.ConflictQuote = "a fine was never considered"
	if b.shouldInterrupt("balanced", false, latest, record) {
		t.Fatal("an invented earlier claim must be dropped")
	}
	b.ConflictQuote = "a fine was available"
	if !b.shouldInterrupt("balanced", false, latest, record) {
		t.Fatal("a grounded contradiction should be put to counsel")
	}
}

func TestRewordedQuestionCountsAsRepeat(t *testing.T) {
	asked := []string{"What specific evidence demonstrates that a less restrictive measure was genuinely unavailable?"}
	again := "What specific evidence demonstrates that a less restrictive measure was genuinely unavailable to the State in light of the record and the available case law?"
	if len(repeats(again, asked)) != 1 {
		t.Fatal("a longer rewording of the same question was not recognised")
	}
	if repeats("Counsel, the bench needs your submissions. Which authority supports your proportionality argument?", asked) != nil {
		t.Fatal("a different question was treated as a repeat")
	}
	if _, ok := frame(again, "unsupported_claim", asked); ok {
		t.Fatal("the bench re-asked the same challenge")
	}
	pressed, ok := frame(again, "follow_up", asked)
	if !ok || pressed != pressPrefix+asked[0] {
		t.Fatalf("pressing must restate the unanswered question: %q", pressed)
	}
	if _, ok := frame(again, "follow_up", append(asked, pressed)); ok {
		t.Fatal("the bench may press once, never ask a third time")
	}
	if got, _ := frame("Please continue: which authority supports the measure?", "procedural", nil); got != "Noted, Counsel. Please continue: which authority supports the measure?" {
		t.Fatalf("a question about the hearing must be acknowledged first: %q", got)
	}
}
