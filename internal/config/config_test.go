package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.Orchestrator != "architect" {
		t.Fatalf("Orchestrator = %q, want %q", cfg.Orchestrator, "architect")
	}
	if cfg.ReviewRounds != 2 {
		t.Fatalf("ReviewRounds = %d, want 2", cfg.ReviewRounds)
	}
	if cfg.CheckTimeout.Duration != 10*time.Minute {
		t.Fatalf("CheckTimeout = %v, want 10m", cfg.CheckTimeout.Duration)
	}
	if cfg.TaskTimeout.Duration != 30*time.Minute {
		t.Fatalf("TaskTimeout = %v, want 30m", cfg.TaskTimeout.Duration)
	}

	wantRoles := map[string]Role{
		"architect":   {Provider: "claude", Model: "opus"},
		"implementer": {Provider: "antigravity", Model: "gemini-4-hydrogen"},
		"reviewer":    {Provider: "codex", Model: "gpt-5.6-sol"},
	}
	for name, want := range wantRoles {
		got, ok := cfg.Roles[name]
		if !ok {
			t.Fatalf("missing default role %q", name)
		}
		if got != want {
			t.Fatalf("Roles[%q] = %+v, want %+v", name, got, want)
		}
	}

	for _, p := range ProviderNames {
		pc, ok := cfg.Providers[p]
		if !ok {
			t.Fatalf("missing default provider %q", p)
		}
		if pc.MaxParallel != 3 {
			t.Fatalf("Providers[%q].MaxParallel = %d, want 3", p, pc.MaxParallel)
		}
	}
}

func TestLoadMerge(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.toml")
	projectPath := filepath.Join(dir, "project.toml")

	globalTOML := `
[roles.implementer]
provider = "codex"
model = "x"
`
	projectTOML := `
review_rounds = 5
`
	if err := os.WriteFile(globalPath, []byte(globalTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(projectTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(globalPath, projectPath)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got := cfg.Roles["implementer"]; got != (Role{Provider: "codex", Model: "x"}) {
		t.Fatalf("implementer = %+v, want {codex x}", got)
	}
	if cfg.ReviewRounds != 5 {
		t.Fatalf("ReviewRounds = %d, want 5", cfg.ReviewRounds)
	}
	if got := cfg.Roles["architect"]; got != (Role{Provider: "claude", Model: "opus"}) {
		t.Fatalf("architect = %+v, want {claude opus}", got)
	}
}

func TestLoadMissingFiles(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(
		filepath.Join(dir, "missing-global.toml"),
		filepath.Join(dir, "missing-project.toml"),
	)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	want := Default()
	if cfg.Orchestrator != want.Orchestrator || cfg.ReviewRounds != want.ReviewRounds {
		t.Fatalf("cfg = %+v, want %+v", cfg, want)
	}
	if len(cfg.Roles) != len(want.Roles) {
		t.Fatalf("len(Roles) = %d, want %d", len(cfg.Roles), len(want.Roles))
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name: "unknown provider in role",
			toml: `
[roles.helper]
provider = "bedrock"
model = "x"
`,
			wantErr: "helper",
		},
		{
			name: "orchestrator points to missing role",
			toml: `
orchestrator = "ghost"
`,
			wantErr: "ghost",
		},
		{
			name: "orchestrator role uses non-claude provider",
			toml: `
[roles.architect]
provider = "codex"
model = "gpt-5.6-sol"
`,
			wantErr: "v1: solo claude puede ser orquestador",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(p, []byte(tt.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load("", p)
			if err == nil {
				t.Fatalf("Load() succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestGlobalPath(t *testing.T) {
	p, err := GlobalPath()
	if err != nil {
		t.Fatalf("GlobalPath() error: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(p), "/weaver/config.toml") {
		t.Fatalf("GlobalPath() = %q, want suffix /weaver/config.toml", p)
	}
}

func TestApplyPresetAndRenderRoundTrip(t *testing.T) {
	presets := Presets()
	if len(presets) != 4 {
		t.Fatalf("len(Presets()) = %d, want 4", len(presets))
	}

	base := Default()
	pc := base.Providers["claude"]
	pc.Bin = "/custom/claude"
	pc.MaxParallel = 5
	base.Providers["claude"] = pc

	applied, err := ApplyPreset(base, "claude-codex")
	if err != nil {
		t.Fatalf("ApplyPreset(claude-codex) error: %v", err)
	}
	if applied.Providers["claude"].Bin != "/custom/claude" || applied.Providers["claude"].MaxParallel != 5 {
		t.Fatalf("Providers[claude] was not preserved: %+v", applied.Providers["claude"])
	}
	if got := applied.Roles["implementer"]; got != (Role{Provider: "codex", Model: "gpt-5.6-sol"}) {
		t.Fatalf("implementer = %+v, want {codex gpt-5.6-sol}", got)
	}

	_, err = ApplyPreset(base, "no-existe")
	if err == nil {
		t.Fatal("ApplyPreset(no-existe) succeeded, want error")
	}
	for _, wantName := range []string{"default", "claude-codex", "claude-opencode", "claude-only"} {
		if !strings.Contains(err.Error(), wantName) {
			t.Fatalf("unknown preset error %q missing %q", err.Error(), wantName)
		}
	}

	rendered, err := RenderTOML(applied)
	if err != nil {
		t.Fatalf("RenderTOML() error: %v", err)
	}
	if !strings.Contains(rendered, "10m0s") || !strings.Contains(rendered, "30m0s") {
		t.Fatalf("RenderTOML() missing serialized durations:\n%s", rendered)
	}

	var decoded Config
	if _, err := toml.Decode(rendered, &decoded); err != nil {
		t.Fatalf("toml.Decode(RenderTOML) error: %v\n%s", err, rendered)
	}
	if decoded.CheckTimeout.Duration != applied.CheckTimeout.Duration ||
		decoded.TaskTimeout.Duration != applied.TaskTimeout.Duration ||
		decoded.Roles["implementer"] != applied.Roles["implementer"] ||
		decoded.Providers["claude"] != applied.Providers["claude"] {
		t.Fatalf("round-trip mismatch:\ngot:  %+v\nwant: %+v", decoded, applied)
	}
}
