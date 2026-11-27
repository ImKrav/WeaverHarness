package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorShowsInstallHintWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".weaver")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgTOML := "[providers.claude]\nbin = 'weaver-missing-claude-hint-xyz'\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var out bytes.Buffer
	code := Doctor(context.Background(), dir, &out)
	if code != 1 {
		t.Fatalf("Doctor() exit code = %d, want 1\n%s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "[providers.claude]") {
		t.Fatalf("Doctor() output missing provider config hint:\n%s", got)
	}
}
