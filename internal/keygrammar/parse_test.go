package keygrammar

import "testing"

func TestParseVerbs(t *testing.T) {
	tests := []struct {
		argv []string
		want Command
	}{
		{[]string{"key", "type", "hello"}, Command{Kind: Type, Text: "hello"}},
		{[]string{"key", "type", "hello", "world"}, Command{Kind: Type, Text: "hello world"}},
		{[]string{"key", "tap", "ctrl+w"}, Command{Kind: Tap, Specs: []string{"ctrl+w"}}},
		{[]string{"key", "tap", "enter", "esc"}, Command{Kind: Tap, Specs: []string{"enter", "esc"}}},
		{[]string{"key", "down", "shift"}, Command{Kind: Down, Specs: []string{"shift"}}},
		{[]string{"key", "up"}, Command{Kind: Up}},
	}
	for _, c := range tests {
		got, err := Parse(c.argv)
		if err != nil {
			t.Fatalf("%v: %v", c.argv, err)
		}
		if got.Kind != c.want.Kind || got.Text != c.want.Text || len(got.Specs) != len(c.want.Specs) {
			t.Fatalf("%v: got %+v; want %+v", c.argv, got, c.want)
		}
		for i := range c.want.Specs {
			if got.Specs[i] != c.want.Specs[i] {
				t.Fatalf("%v: spec %d = %q; want %q", c.argv, i, got.Specs[i], c.want.Specs[i])
			}
		}
	}
}

func TestParseRefusals(t *testing.T) {
	bad := [][]string{
		{"key"},
		{"key", "type"},
		{"key", "tap"},
		{"key", "down"},
		{"key", "down", "a", "b"},
		{"key", "up", "extra"},
		{"key", "wiggle", "x"},
		{"mouse", "type", "x"},
	}
	for _, argv := range bad {
		if _, err := Parse(argv); err == nil {
			t.Fatalf("%v: parsed without error", argv)
		}
	}
}
