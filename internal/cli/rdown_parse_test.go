package cli

import "testing"

// The latch is shared by both buttons, so one span cannot change buttons
// half-way: `down rdown …` is refused outright at the second press rather than
// silently producing a left drag that the caller believed was a right click.
func TestMixedButtonSequenceIsRefused(t *testing.T) {
	_, err := Parse([]string{"mouse", "rel", "down", "rdown", "drag", "10", "0", "up", "rup"}, Config{ScreenW: 1920, ScreenH: 1080})
	if err == nil {
		t.Fatal("`down rdown ...` must be refused: the right span never opens")
	}
	if want := `at token 3: "rdown" is illegal while the button is DOWN (allowed: drag, up, wait)`; err.Error() != want {
		t.Fatalf("error = %q; want %q", err, want)
	}
}

// rdown is legal only while the latch is up, and rup is legal with either
// latch — the two rules the verb table states.
func TestRDownRequiresLatchUpRUpAcceptsEither(t *testing.T) {
	if _, err := Parse([]string{"mouse", "rel", "rdown"}, Config{ScreenW: 1920, ScreenH: 1080}); err != nil {
		t.Fatalf("rdown alone must parse (a right-click): %v", err)
	}
	if _, err := Parse([]string{"mouse", "rel", "rup"}, Config{ScreenW: 1920, ScreenH: 1080}); err != nil {
		t.Fatalf("rup with the latch up must parse (release where the pointer is): %v", err)
	}
	if _, err := Parse([]string{"mouse", "rel", "down", "rdown"}, Config{ScreenW: 1920, ScreenH: 1080}); err == nil {
		t.Fatal("rdown inside a held span must be refused")
	}
	if _, err := Parse([]string{"mouse", "rel", "rdown", "drag", "5", "5"}, Config{ScreenW: 1920, ScreenH: 1080}); err != nil {
		t.Fatalf("rdown drag 5 5 must parse (a right drag): %v", err)
	}
}

// A line left open gets its release appended, and for a right span that is a
// plain `up`, which the executor treats as the span terminator too.
func TestRightSpanLeftOpenGetsAppendedRelease(t *testing.T) {
	seq, err := Parse([]string{"mouse", "rel", "rdown", "drag", "4", "0"}, Config{ScreenW: 1920, ScreenH: 1080})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	last := seq.Verbs[len(seq.Verbs)-1]
	if last.Kind != Up {
		t.Fatalf("last verb = %s; want up appended", last.Kind)
	}
}
