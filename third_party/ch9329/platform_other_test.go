//go:build !windows && !linux && !darwin

package ch9329

// Platform-layer tests for non-Windows builds.
//
// These run on any GOOS the stubs cover (linux, darwin, ...) and they exist to
// pin down one property: an unimplemented witness must FAIL LOUDLY. A stub that
// returned (0, 0, nil) would look like a pointer parked in the top-left corner,
// and every motion primitive would then "succeed" against a fiction — which is
// the exact bug class this package was built to avoid.

import (
	"errors"
	"testing"
)

func TestCursorPointIsUnsupported(t *testing.T) {
	x, y, err := CursorPoint()
	if err == nil {
		t.Fatalf("CursorPoint returned (%d,%d) with nil error; an unimplemented "+
			"witness must not pretend the pointer is at a real position", x, y)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("CursorPoint error = %v, want it to wrap ErrUnsupported", err)
	}
}

func TestButtonStateIsUnsupported(t *testing.T) {
	if _, _, err := ButtonState(0x01); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ButtonState error = %v, want ErrUnsupported", err)
	}
}

func TestFocusMustNotClaimSuccess(t *testing.T) {
	// Focus is the one window helper where a wrong answer is worse than no
	// answer: a controller that believes it focused a window will inject into
	// whatever is actually in front of the user.
	if _, ok := Focus(12345); ok {
		t.Fatal("Focus reported success on a platform that cannot focus windows")
	}
}

func TestScreenSizeIsNominal(t *testing.T) {
	w, h := ScreenSize()
	if w <= 0 || h <= 0 {
		t.Fatalf("ScreenSize = %dx%d, want positive nominal bounds", w, h)
	}
}

func TestTitleContainsIsPortable(t *testing.T) {
	cases := []struct {
		hay, needle string
		want        bool
	}{
		{"Slider Captcha Demo - Chrome", "captcha", true},
		{"Slider Captcha Demo - Chrome", "CAPTCHA", true},
		{"Slider Captcha Demo - Chrome", "vivaldi", false},
		{"anything", "", true},
	}
	for _, c := range cases {
		if got := TitleContains(c.hay, c.needle); got != c.want {
			t.Errorf("TitleContains(%q, %q) = %v, want %v", c.hay, c.needle, got, c.want)
		}
	}
}
