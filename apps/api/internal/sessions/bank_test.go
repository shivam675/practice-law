package sessions

import (
	"testing"
	"time"
)

func TestBankRetryDelayIsExponentialAndCapped(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{{1, time.Minute}, {2, 2 * time.Minute}, {7, time.Hour}, {100, time.Hour}}
	for _, tc := range cases {
		if got := bankRetryDelay(tc.attempt); got != tc.want {
			t.Errorf("bankRetryDelay(%d) = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}
