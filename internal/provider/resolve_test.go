package provider

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCandidatePathsByOS(t *testing.T) {
	envDarwin := map[string]string{}
	darwin := candidatePaths("darwin", "/Users/alice", func(k string) string { return envDarwin[k] }, "antigravity", "agy")
	if !slices.Contains(darwin, "/opt/homebrew/bin/agy") {
		t.Fatalf("darwin missing /opt/homebrew/bin/agy: %v", darwin)
	}
	if !slices.Contains(darwin, "/Applications/Antigravity.app/Contents/Resources/app/bin/agy") {
		t.Fatalf("darwin missing Antigravity.app binary: %v", darwin)
	}
	if !slices.Contains(darwin, "/Users/alice/.local/bin/agy") {
		t.Fatalf("darwin missing ~/.local/bin/agy: %v", darwin)
	}

	envLinux := map[string]string{"XDG_BIN_HOME": "/home/bob/.bin"}
	linux := candidatePaths("linux", "/home/bob", func(k string) string { return envLinux[k] }, "opencode", "opencode")
	for _, want := range []string{
		"/home/bob/.bin/opencode",
		"/home/bob/.local/bin/opencode",
		"/home/bob/.opencode/bin/opencode",
		"/usr/local/bin/opencode",
	} {
		if !slices.Contains(linux, want) {
			t.Fatalf("linux missing %q in %v", want, linux)
		}
	}

	envWin := map[string]string{
		"APPDATA":      `C:\Users\carol\AppData\Roaming`,
		"LOCALAPPDATA": `C:\Users\carol\AppData\Local`,
	}
	win := candidatePaths("windows", `C:\Users\carol`, func(k string) string { return envWin[k] }, "codex", "codex")
	if !slices.Contains(win, `C:\Users\carol\AppData\Roaming\npm\codex.cmd`) {
		t.Fatalf("windows missing npm codex.cmd in %v", win)
	}
	if !slices.Contains(win, `C:\Users\carol\.local\bin\codex.exe`) {
		t.Fatalf("windows missing .local/bin/codex.exe in %v", win)
	}
}

func TestResolveBinaryFallsBackToCandidateAndRejectsMissingExplicitPath(t *testing.T) {
	home := t.TempDir()
	localBinDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBinDir, 0o755); err != nil {
		t.Fatal(err)
	}

	binName := "claude"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	candBin := filepath.Join(localBinDir, binName)
	if err := os.WriteFile(candBin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}

	missingLookPath := func(file string) (string, error) {
		return "", errors.New("not found in PATH")
	}

	got, err := resolveBinaryWith(runtime.GOOS, home, func(string) string { return "" }, missingLookPath, "claude", "")
	if err != nil {
		t.Fatalf("resolveBinaryWith() fallback error: %v", err)
	}
	if got != candBin {
		t.Fatalf("resolveBinaryWith() = %q, want %q", got, candBin)
	}

	explicit := filepath.Join(t.TempDir(), "custom-claude")
	if _, err := resolveBinaryWith(runtime.GOOS, home, func(string) string { return "" }, missingLookPath, "claude", explicit); err == nil {
		t.Fatalf("resolveBinaryWith(explicit missing path) succeeded, want error")
	}

	if hint := InstallHint("darwin", "antigravity"); !strings.Contains(hint, "config.toml") {
		t.Fatalf("InstallHint missing config.toml guidance: %q", hint)
	}
}
