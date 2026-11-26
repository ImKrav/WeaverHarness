package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/provider"
)

func newAppTestRepo(t *testing.T) gitx.Repo {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Weaver Test")
	runGit("config", "user.email", "weaver@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")

	r, err := gitx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(gitx.WeaverDir(r.Dir))
	})
	return r
}

func buildFakeCLI(t *testing.T) string {
	t.Helper()
	name := "fakecli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	outBin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", outBin, "../provider/testdata/fakecli")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakecli: %v\n%s", err, string(out))
	}
	return outBin
}

func TestNotGitRepo(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	err := RunSession(context.Background(), SessionOptions{
		Dir: dir,
		In:  strings.NewReader(""),
		Out: &out,
		Headless: func(send func(string) error, events <-chan provider.Event) error {
			return nil
		},
	})
	if !errors.Is(err, gitx.ErrNotGit) {
		t.Fatalf("RunSession() err = %v, want ErrNotGit", err)
	}
}

func TestDirtyPromptDecline(t *testing.T) {
	r := newAppTestRepo(t)
	if err := os.WriteFile(filepath.Join(r.Dir, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	headlessCalled := false
	err := RunSession(context.Background(), SessionOptions{
		Dir: r.Dir,
		In:  strings.NewReader("n\n"),
		Out: &out,
		Headless: func(send func(string) error, events <-chan provider.Event) error {
			headlessCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSession() unexpected error: %v", err)
	}
	if headlessCalled {
		t.Fatal("Headless was called after declining dirty prompt")
	}
	if !strings.Contains(out.String(), "Hay cambios sin commit; no entrarán en la ejecución. ¿Continuar? [s/N]") {
		t.Fatalf("prompt output = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(r.Dir, ".weaver", "runs")); !os.IsNotExist(err) {
		t.Fatalf("runs directory should not exist when user declined")
	}
}

func TestExcludeAddedOnce(t *testing.T) {
	r := newAppTestRepo(t)
	if err := ensureWeaverExcluded(context.Background(), r); err != nil {
		t.Fatalf("first ensureWeaverExcluded error: %v", err)
	}
	if err := ensureWeaverExcluded(context.Background(), r); err != nil {
		t.Fatalf("second ensureWeaverExcluded error: %v", err)
	}

	commonDir, err := r.CommonDir(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(commonDir, "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == ".weaver/" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf(".weaver/ count = %d in exclude file:\n%s", count, string(data))
	}
}

func TestOrchestratorLaunchArgs(t *testing.T) {
	r := newAppTestRepo(t)
	fakeBin := buildFakeCLI(t)
	argsFile := filepath.Join(t.TempDir(), "orch-args.json")

	// Point project config to fakecli as claude binary.
	cfgDir := filepath.Join(r.Dir, ".weaver")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgTOML := "[providers.claude]\nbin = '" + fakeBin + "'\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	// Isolate global config path so machine config doesn't interfere.
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAKECLI_ECHO", "1")
	t.Setenv("FAKECLI_ARGS", argsFile)

	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var gotText string
	err := RunSession(ctx, SessionOptions{
		Dir: r.Dir,
		In:  strings.NewReader("s\n"),
		Out: &out,
		Headless: func(send func(string) error, events <-chan provider.Event) error {
			if err := send("hola orquestador"); err != nil {
				return err
			}
			for ev := range events {
				if ev.Kind == provider.EventText {
					gotText = ev.Text
				}
				if ev.Kind == provider.EventTurnEnd {
					return nil
				}
			}
			return errors.New("events closed without turn_end")
		},
	})
	if err != nil {
		t.Fatalf("RunSession() error: %v", err)
	}
	if gotText != "eco: hola orquestador" {
		t.Fatalf("gotText = %q, want %q", gotText, "eco: hola orquestador")
	}

	rawArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		t.Fatal(err)
	}

	var hasMCPConfig, hasPermissionMode, hasSessionID bool
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) && strings.HasSuffix(filepath.ToSlash(args[i+1]), "/mcp.json") {
			hasMCPConfig = true
		}
		if a == "--permission-mode" && i+1 < len(args) && args[i+1] == "dontAsk" {
			hasPermissionMode = true
		}
		if a == "--session-id" && i+1 < len(args) && args[i+1] != "" {
			hasSessionID = true
		}
	}
	if !hasMCPConfig || !hasPermissionMode || !hasSessionID {
		t.Fatalf("missing flags in args %v (mcp=%v perm=%v sid=%v)", args, hasMCPConfig, hasPermissionMode, hasSessionID)
	}
	for _, tool := range OrchestratorTools {
		if !slices.Contains(args, tool) {
			t.Fatalf("missing tool %q in args %v", tool, args)
		}
	}
}

func TestDoctorMissingOrchestrator(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".weaver")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgTOML := "[providers.claude]\nbin = 'weaver-missing-claude-bin-xyz'\n"
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
	if !strings.Contains(out.String(), "soporte de términos no confirmado") {
		t.Fatalf("Doctor() missing antigravity terms note:\n%s", out.String())
	}
}
