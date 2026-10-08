package sessions

import (
	"github.com/intelimek/megamoot/apps/api/internal/spec"
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
