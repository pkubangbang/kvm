package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.Baud != 115200 || c.ScreenW != 1920 || c.ScreenH != 1080 || c.StepPx != 4 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

// TestDefaultsPortMatchesPlatform pins the one default that is genuinely
// platform-shaped. A wrong value here does not fail loudly at parse time — it
// opens the wrong device, so an untouched port would make the tool look broken on
// a machine where the chip is fine.
func TestDefaultsPortMatchesPlatform(t *testing.T) {
	c := Defaults()
	want := "/dev/ttyACM0"
	if runtime.GOOS == "windows" {
		want = "COM3"
	}
	if c.Port != want {
		t.Fatalf("Defaults().Port = %q on %s; want %q", c.Port, runtime.GOOS, want)
	}
}

func TestLoadMissingFileFallsBackToDefaults(t *testing.T) {
	t.Setenv("KVM_CONFIG", filepath.Join(t.TempDir(), "nope.json"))
	c := Load()
	if c.Baud != Defaults().Baud {
		t.Fatalf("missing file: Baud = %d; want default %d", c.Baud, Defaults().Baud)
	}
	if c.Source["*"] == "" {
		t.Fatal("missing file must record its source")
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	t.Setenv("KVM_CONFIG", path)
	want := Defaults()
	want.Port = "/dev/ttyUSB9"
	want.Baud = 9600
	want.ScreenW, want.ScreenH = 2560, 1440
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.Port != want.Port || got.Baud != want.Baud || got.ScreenW != 2560 || got.ScreenH != 1440 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.VerifiedAt == "" {
		t.Fatal("Save must stamp VerifiedAt")
	}
}

func TestLoadCorruptFileFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KVM_CONFIG", path)
	c := Load()
	if c.Baud != Defaults().Baud {
		t.Fatalf("corrupt file: Baud = %d; want default", c.Baud)
	}
}
