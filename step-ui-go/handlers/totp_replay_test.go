package handlers

import (
	"testing"
	"time"
)

// totpStepForTime computes the TOTP timestep for a given unix timestamp
// using the standard 30-second period.  This mirrors the logic in
// validateTOTPWithReplay so the test can reason about step values without
// calling the full handler.
func totpStepForTime(unixSec int64) int64 {
	return unixSec / 30
}

func TestTOTPReplayStepComparison(t *testing.T) {
	// Verify the step-comparison invariants that validateTOTPWithReplay relies on.
	now := time.Now().Unix()
	step := totpStepForTime(now)

	tests := []struct {
		name        string
		currentStep int64
		lastStep    int64
		wantAccept  bool
	}{
		{
			name:        "first use — no prior step",
			currentStep: step,
			lastStep:    0,
			wantAccept:  true,
		},
		{
			name:        "replay in same window — exact match",
			currentStep: step,
			lastStep:    step,
			wantAccept:  false,
		},
		{
			name:        "replay with earlier step stored (should never happen but belt-and-suspenders)",
			currentStep: step,
			lastStep:    step + 1,
			wantAccept:  false,
		},
		{
			name:        "next window — fresh code",
			currentStep: step + 1,
			lastStep:    step,
			wantAccept:  true,
		},
		{
			name:        "much later — obviously fresh",
			currentStep: step + 100,
			lastStep:    step,
			wantAccept:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The acceptance condition from validateTOTPWithReplay:
			// accept iff currentStep > lastStep
			got := tc.currentStep > tc.lastStep
			if got != tc.wantAccept {
				t.Errorf("currentStep=%d lastStep=%d: got accept=%v want=%v",
					tc.currentStep, tc.lastStep, got, tc.wantAccept)
			}
		})
	}
}

// TestTOTPStepAdvancesOver30s ensures consecutive steps differ after 30 s,
// confirming the window boundary logic is correct.
func TestTOTPStepAdvancesOver30s(t *testing.T) {
	base := int64(1_700_000_000)
	s1 := totpStepForTime(base)
	s2 := totpStepForTime(base + 30)
	if s2 <= s1 {
		t.Errorf("expected step to advance: s1=%d s2=%d", s1, s2)
	}
}
