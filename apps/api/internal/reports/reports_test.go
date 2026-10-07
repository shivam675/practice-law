package reports

import "testing"

func TestTotalsApplyWeightsAndOverrides(t *testing.T) {
	override := 8.0
	total, max := totals([]Score{{Score: 5, Max: 10, Weight: 40}, {Score: 2, Max: 10, Weight: 60, Override: &override}})
	if total != 68 || max != 100 {
		t.Fatalf("got %g/%g, want 68/100", total, max)
	}
}
