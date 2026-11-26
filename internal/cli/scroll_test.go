package cli

import "testing"

// scroll is a RELATIVE verb: the wheel byte exists only on the relative HID
// report (0x05), so `mouse abs scroll` must be refused rather than silently
// becoming a placement with no effect on the page.
func TestScrollIsRelOnly(t *testing.T) {
	seq, err := Parse([]string{"mouse", "rel", "scroll", "-3"}, Config{ScreenW: 1920, ScreenH: 1080})
	if err != nil {
		t.Fatalf("rel scroll -3: %v", err)
	}
	if len(seq.Verbs) != 1 || seq.Verbs[0].Kind != Scroll || seq.Verbs[0].A != -3 {
		t.Fatalf("rel scroll -3 parsed as %+v", seq.Verbs)
	}
	if _, err := Parse([]string{"mouse", "abs", "scroll", "3"}, Config{ScreenW: 1920, ScreenH: 1080}); err == nil {
		t.Fatal("abs scroll must be refused: the abs spec table holds Move only")
	}
}

func TestScrollArityAndRange(t *testing.T) {
	cfg := Config{ScreenW: 1920, ScreenH: 1080}
	if _, err := Parse([]string{"mouse", "rel", "scroll"}, cfg); err == nil {
		t.Fatal("scroll with no count must be refused")
	}
	if _, err := Parse([]string{"mouse", "rel", "scroll", "1", "2"}, cfg); err == nil {
		t.Fatal("scroll takes exactly one argument")
	}
	if _, err := Parse([]string{"mouse", "rel", "scroll", "x"}, cfg); err == nil {
		t.Fatal("scroll with a non-integer must be refused")
	}
	for _, n := range []string{"-100", "0", "100"} {
		if _, err := Parse([]string{"mouse", "rel", "scroll", n}, cfg); err != nil {
			t.Fatalf("scroll %s must be legal: %v", n, err)
		}
	}
	for _, n := range []string{"101", "-101"} {
		if _, err := Parse([]string{"mouse", "rel", "scroll", n}, cfg); err == nil {
			t.Fatalf("scroll %s is out of range [-100,100] and must be refused", n)
		}
	}
}

// Scrolling WHILE the button is held is a real gesture (dragging a canvas and
// scrolling it), so the verb is legal in both latch states and must not end a
// span early.
func TestScrollIsLegalInsideAHeldSpan(t *testing.T) {
	seq, err := Parse([]string{"mouse", "rel", "down", "drag", "10", "0", "scroll", "-2", "up"},
		Config{ScreenW: 1920, ScreenH: 1080})
	if err != nil {
		t.Fatalf("scroll inside a held span: %v", err)
	}
	kinds := []Kind{Down, Drag, Scroll, Up}
	if len(seq.Verbs) != len(kinds) {
		t.Fatalf("got %d verbs, want %d: %+v", len(seq.Verbs), len(kinds), seq.Verbs)
	}
	for i, k := range kinds {
		if seq.Verbs[i].Kind != k {
			t.Fatalf("verb %d = %s; want %s", i, seq.Verbs[i].Kind, k)
		}
	}
}
