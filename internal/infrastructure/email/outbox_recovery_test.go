package email

import (
	"testing"
	"time"
)

func TestFailureScheduleKeepsNormalLimitAndBoundsManualRecovery(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		attempt    int
		limit      int
		wantStatus string
		wantDelay  time.Duration
	}{
		{"normal job ends at five", 5, 5, "exhausted", 0},
		{"authorized fifth failure retries once", 5, 6, "retrying", 16 * time.Minute},
		{"authorized sixth failure is final", 6, 6, "exhausted", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, next := failureSchedule(&TicketEmailJob{AttemptNumber: tc.attempt, MaxAttempts: tc.limit}, now)
			if status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", status, tc.wantStatus)
			}
			if tc.wantDelay == 0 && next != nil {
				t.Fatal("final failure was scheduled again")
			}
			if tc.wantDelay != 0 && (next == nil || !next.Equal(now.Add(tc.wantDelay))) {
				t.Fatal("authorized recovery did not preserve the bounded retry schedule")
			}
		})
	}
}
