package cli

import "testing"

// The span `rdown drag x y drag x1 y1 rup` is one right-button drag: rdown
// sets the shared latch, drag is legal inside it, rup closes it. This is the
// gesture XMind pans its canvas with, so it must stay expressible as ONE
// process and ONE span.
func TestRDownDragDragRUpIsOneSpan(t *testing.T) {
	cases := []struct {
		argv []string
		want []Kind
	}{
		{
			[]string{"mouse", "rel", "rdown", "drag", "15", "10", "drag", "15", "10", "rup"},
			[]Kind{RDown, Drag, Drag, RUp},
		},
		{
			[]string{"mouse", "rel", "rdown", "drag", "40", "0", "rup"},
			[]Kind{RDown, Drag, RUp},
		},
		{
			[]string{"mouse", "rel", "rdown", "wait", "150", "drag", "1", "0", "rup"},
			[]Kind{RDown, Wait, Drag, RUp},
		},
	}
	for _, c := range cases {
		seq, err := Parse(c.argv, Config{ScreenW: 1920, ScreenH: 1080})
		if err != nil {
			t.Fatalf("%v: %v", c.argv, err)
		}
		if len(seq.Verbs) != len(c.want) {
			t.Fatalf("%v: %d verbs; want %d", c.argv, len(seq.Verbs), len(c.want))
		}
		for i, k := range c.want {
			if seq.Verbs[i].Kind != k {
				t.Fatalf("%v: verb %d = %s; want %s", c.argv, i, seq.Verbs[i].Kind, k)
			}
		}
	}
}

// A right drag left open is completed with an appended release, like a left
// one — Parse step 7 is button-agnostic.
func TestRDownDragWithoutReleaseGetsAppendedUp(t *testing.T) {
	seq, err := Parse([]string{"mouse", "rel", "rdown", "drag", "15", "10"}, Config{ScreenW: 1920, ScreenH: 1080})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if last := seq.Verbs[len(seq.Verbs)-1]; last.Kind != Up {
		t.Fatalf("last verb = %s; want an appended up", last.Kind)
	}
}
