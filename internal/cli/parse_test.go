package cli

import (
	"strings"
	"testing"
)

func cfg() Config { return Config{ScreenW: 1920, ScreenH: 1080} }

// drag is legal ONLY inside a down..up span. Both sides of the rule matter:
// before the press, and after the release.
func TestDragMustBeInsideSpan(t *testing.T) {
	bad := [][]string{
		{"mouse", "rel", "drag", "10", "10"},                          // no press
		{"mouse", "rel", "down", "up", "drag", "10", "10"},            // after release
	}
	for _, argv := range bad {
		_, err := Parse(argv, cfg())
		if err == nil {
			t.Errorf("%v: drag outside a down..up span must be an error", argv)
			continue
		}
		if !strings.Contains(err.Error(), "down..up span") {
			t.Errorf("%v: error should name the span, got %v", argv, err)
		}
	}
	good := [][]string{
		{"mouse", "rel", "down", "drag", "10", "10", "up"},
		{"mouse", "rel", "down", "drag", "10", "10", "wait", "50", "drag", "0", "10", "up"},
	}
	for _, argv := range good {
		if _, err := Parse(argv, cfg()); err != nil {
			t.Errorf("%v: should parse, got %v", argv, err)
		}
	}
}

// A sequence never ends pressed (up is appended), and abs has no button verbs.
func TestAbsNeedsGotoAndMustEndUp(t *testing.T) {
	if _, err := Parse([]string{"mouse", "abs", "down", "up"}, cfg()); err == nil {
		t.Error("abs down must be rejected: abs is placement-only")
	}
	seq, err := Parse([]string{"mouse", "rel", "down"}, cfg())
	if err != nil {
		t.Fatalf("a sequence ending DOWN is normalized, not rejected: %v", err)
	}
	if n := len(seq.Verbs); n == 0 || seq.Verbs[n-1].Kind != Up {
		t.Errorf("normalized sequence must end with up, got %v", seq.Verbs)
	}
	if _, err := Parse([]string{"mouse", "rel", "down", "up"}, cfg()); err != nil {
		t.Errorf("an already-terminated sequence must still parse: %v", err)
	}
}

// The absolute namespace is placement-only: a missing abs drag is better than a
// fake one, so the verb must not exist there at all.
func TestAbsHasNoDrag(t *testing.T) {
	if _, err := Parse([]string{"mouse", "abs", "move", "900", "600", "down", "up"}, cfg()); err == nil {
		t.Fatal("abs drag must be rejected: it cannot hold the button on this host")
	} else if !strings.Contains(err.Error(), "unknown verb") {
		t.Fatalf("want \"unknown verb\", got %v", err)
	}
}
