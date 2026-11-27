package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigInitAndShow(t *testing.T) {
	r := newAppTestRepo(t)
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var initOut bytes.Buffer
	if err := ConfigInit(ConfigInitOptions{
		Dir:    r.Dir,
		Preset: "claude-only",
		Out:    &initOut,
	}); err != nil {
		t.Fatalf("ConfigInit() error: %v", err)
	}

	cfgPath := filepath.Join(r.Dir, ".weaver", "config.toml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading initialized config: %v", err)
	}
	if !strings.Contains(string(raw), `model = "sonnet"`) {
		t.Fatalf("initialized config missing claude-only sonnet role:\n%s", string(raw))
	}

	// Rejects overwriting without Force: true.
	if err := ConfigInit(ConfigInitOptions{
		Dir:    r.Dir,
		Preset: "claude-codex",
		Out:    &initOut,
	}); err == nil {
		t.Fatal("ConfigInit() without Force succeeded on existing file, want error")
	}

	var showOut bytes.Buffer
	if err := ConfigShow(ConfigShowOptions{
		Dir: r.Dir,
		Out: &showOut,
	}); err != nil {
		t.Fatalf("ConfigShow() error: %v", err)
	}
	if !strings.Contains(showOut.String(), `model = "sonnet"`) {
		t.Fatalf("ConfigShow() missing merged config values:\n%s", showOut.String())
	}

	// Preset override on ConfigShow does not mutate file on disk.
	var showOverrideOut bytes.Buffer
	if err := ConfigShow(ConfigShowOptions{
		Dir:    r.Dir,
		Preset: "claude-opencode",
		Out:    &showOverrideOut,
	}); err != nil {
		t.Fatalf("ConfigShow(Preset) error: %v", err)
	}
	if !strings.Contains(showOverrideOut.String(), "opencode/big-pickle") {
		t.Fatalf("ConfigShow(Preset) missing opencode model:\n%s", showOverrideOut.String())
	}
}
