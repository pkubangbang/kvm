package mouse

import (
	"testing"
)

// TestStallGuardCancelsWhenWitnessFrozen pins the STALL PREVENTION rule:
// a movement must be CANCELLED — ErrStalled — once the pointer has failed to
// advance for maxStallReports consecutive reports, instead of the emitter
// continuing to stream no-op reports with the button still down.
//
// This is the failure that matters in practice and it is not hypothetical: at
// a screen edge the requested displacement physically cannot be delivered, so
// an open-loop emitter burns its whole byte budget pressing against an edge
// that will not move. The Linux reference implementation aborts after N
// reports; the count and the reset rule below are the whole contract.
func TestStallGuardCancelsWhenWitnessFrozen(t *testing.T) {
	r := &Rel{}

	// A frozen witness: every sample is the same point.
	frozen := func() ([2]int, bool) { return [2]int{100, 100}, true }
	r.witness = frozen

	for i := 1; i <= maxStallReports+1; i++ {
		err := r.stallGuard()
		if i <= maxStallReports {
			// The FIRST call takes the baseline; the ones after it compare.
			// maxStallReports silent COMPARISONS must not cancel.
			if err != nil {
				t.Fatalf("guard cancelled after %d reports, want it to hold until %d", i, maxStallReports+1)
			}
			continue
		}
		if err != ErrStalled {
			t.Fatalf("after %d reports with a frozen pointer got %v, want ErrStalled", i, err)
		}
	}
}

// TestStallGuardResetsOnAdvance is the other half of the rule: the counter is
// about CONSECUTIVE silence. A slow but real stream must never be cancelled,
// which is exactly what a per-report absolute comparison would do — the
// observer publishes at ~16 ms while reports leave every ~2 ms, so a report
// emitted now is legitimately invisible yet.
func TestStallGuardResetsOnAdvance(t *testing.T) {
	r := &Rel{}
	x := 100
	r.witness = func() ([2]int, bool) { return [2]int{x, 100}, true }

	// Sit one report short of the limit, then advance: never cancelled.
	for cycle := 0; cycle < 5; cycle++ {
		for i := 0; i < maxStallReports-1; i++ {
			if err := r.stallGuard(); err != nil {
				t.Fatalf("cycle %d silent report %d: %v, want nil", cycle, i, err)
			}
		}
		x++
		if err := r.stallGuard(); err != nil {
			t.Fatalf("cycle %d: progress must reset the counter, got %v", cycle, err)
		}
	}
}

// TestStallGuardWithoutWitnessNeverCancels pins the third rule: absent
// evidence is not a stall. A platform with no cursor feed cannot prove that
// nothing moved, and cancelling on that would abort every stroke there. The
// byte budget and LetterBudget remain the bounds in that case.
func TestStallGuardWithoutWitnessNeverCancels(t *testing.T) {
	r := &Rel{}
	r.witness = func() ([2]int, bool) { return [2]int{}, false }
	for i := 0; i < 4*maxStallReports; i++ {
		if err := r.stallGuard(); err != nil {
			t.Fatalf("report %d without a witness: %v, want nil", i, err)
		}
	}
}
