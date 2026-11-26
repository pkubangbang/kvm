// Package config discovers the machine facts kvm needs and caches them beside the
// executable. The user never sets a baud rate or a port: those are low-level facts
// the tool derives for itself.
//
// The split that keeps this honest:
//   - EQUIPMENT facts (port, baud, screen size) may persist. They stay true when
//     nothing is running.
//   - INTERACTION state (button, position, in-flight drag) never persists.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Config holds the discovered equipment facts.
type Config struct {
	Version    int    `json:"version"`
	Port       string `json:"port"`
	PortAlt    string `json:"port_fallback,omitempty"`
	Baud       int    `json:"baud"`
	ScreenW    int    `json:"screen_w"`
	ScreenH    int    `json:"screen_h"`
	StepPx     int    `json:"step_px"`
	ClickGap   int    `json:"click_gap_ms"`
	VerifiedAt string `json:"verified_at,omitempty"`

	// source records where each value came from, for `kvm config` output.
	Source map[string]string `json:"-"`
}

// Defaults returns the values used when nothing has been discovered yet.
//
// The port is the one value that is genuinely platform-shaped rather than
// discovered: the CH9329 enumerates as a USB CDC-ACM device, which on Linux is
// /dev/ttyACM0 and on Windows is a COM port. Picking the right default here
// means a first run finds the chip without the user passing -port; discovery
// still overrides it when it finds something better.
func Defaults() Config {
	return Config{
		Version:  1,
		Port:     defaultPort(),
		Baud:     115200, // the only rate that answers on this unit
		ScreenW:  1920,
		ScreenH:  1080,
		StepPx:   4, // inside libinput's 1:1 plateau
		ClickGap: 60,
		Source:   map[string]string{},
	}
}

// defaultPort names the device the chip normally enumerates as.
//
// Windows has no stable name for a serial device: COM3 is what this rig's CH343
// board receives, but the number is assigned by enumeration order and can differ
// on another machine. It is a starting point, not a promise — discovery and the
// -port flag both outrank it, and a wrong default fails loudly at open time
// rather than silently driving nothing.
func defaultPort() string {
	if runtime.GOOS == "windows" {
		return "COM3"
	}
	return "/dev/ttyACM0"
}

// Path returns the config file location: beside the executable, so the values
// travel with the build and never pollute the caller's working directory.
func Path() string {
	if p := os.Getenv("KVM_CONFIG"); p != "" {
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return "kvm-config.json"
	}
	return filepath.Join(filepath.Dir(exe), ".kvm-config.json")
}

// Load reads the cached config, falling back to defaults. A missing or corrupt
// file is NOT an error: it degrades to discovery. That is deliberate — a stale
// cache must never be a thing the user has to fix by hand.
func Load() Config {
	cfg := Defaults()
	path := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		cfg.Source["*"] = "defaults (no config file)"
		return cfg
	}
	var onDisk Config
	if err := json.Unmarshal(data, &onDisk); err != nil {
		cfg.Source["*"] = "defaults (config file unreadable, will be rewritten)"
		return cfg
	}
	// only take values that look sane; anything else stays at the default
	if onDisk.Port != "" {
		cfg.Port = onDisk.Port
		cfg.Source["port"] = "file"
	}
	if onDisk.PortAlt != "" {
		cfg.PortAlt = onDisk.PortAlt
	}
	if onDisk.Baud > 0 {
		cfg.Baud = onDisk.Baud
		cfg.Source["baud"] = "file"
	}
	if onDisk.ScreenW > 0 && onDisk.ScreenH > 0 {
		cfg.ScreenW, cfg.ScreenH = onDisk.ScreenW, onDisk.ScreenH
		cfg.Source["screen"] = "file"
	}
	if onDisk.StepPx > 0 {
		cfg.StepPx = onDisk.StepPx
	}
	if onDisk.ClickGap > 0 {
		cfg.ClickGap = onDisk.ClickGap
	}
	cfg.VerifiedAt = onDisk.VerifiedAt
	return cfg
}

// Save writes the config atomically. A read-only directory is a warning, never a
// failure: drawing must not depend on being able to cache.
func Save(cfg Config) error {
	cfg.Version = 1
	cfg.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
	// never persist the source map
	out := struct {
		Config
		Source any `json:"-"`
	}{Config: cfg}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	path := Path()
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".kvm-config-*.tmp")
	if err != nil {
		return fmt.Errorf("config not persisted (%s is not writable): %w", dir, err)
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ErrNotWritable is returned (wrapped) when the cache cannot be written.
var ErrNotWritable = errors.New("config not persisted")
