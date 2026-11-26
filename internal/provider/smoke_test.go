//go:build smoke

package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/config"
	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/prompts"
	"github.com/ImKrav/weaverharness/internal/receipt"
	"github.com/ImKrav/weaverharness/internal/verify"
)

func TestSmoke(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("WEAVER_SMOKE_PROVIDERS"))
	if raw == "" {
		t.Skip("WEAVER_SMOKE_PROVIDERS no definido")
	}

	cfg := config.Default()
	defaultModels := map[string]string{
		"claude":      cfg.Roles["architect"].Model,
		"codex":       cfg.Roles["reviewer"].Model,
		"antigravity": cfg.Roles["implementer"].Model,
		"opencode":    "opencode/big-pickle",
	}

	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			p, err := New(name, cfg.Providers[name].Bin)
			if err != nil {
				t.Fatalf("New(%q) error: %v", name, err)
			}
			model := defaultModels[name]
			if override := os.Getenv("WEAVER_SMOKE_MODEL_" + strings.ToUpper(name)); override != "" {
				model = override
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			repoDir := filepath.Join(t.TempDir(), "smoke repo")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatal(err)
			}
			runGit := func(dir string, args ...string) {
				cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, string(out))
				}
			}
			runGit(repoDir, "init", "-b", "main")
			runGit(repoDir, "config", "user.name", "Weaver Smoke")
			runGit(repoDir, "config", "user.email", "smoke@example.com")
			if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("smoke\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(repoDir, "add", "README.md")
			runGit(repoDir, "commit", "-m", "initial")

			repo, err := gitx.Open(repoDir)
			if err != nil {
				t.Fatal(err)
			}
			wtDir := filepath.Join(gitx.WeaverDir(repo.Dir), "smoke", name)
			t.Cleanup(func() {
				_ = repo.RemoveWorktree(context.Background(), wtDir)
				_ = os.RemoveAll(gitx.WeaverDir(repo.Dir))
			})
			branch := "weaver/smoke-" + name
			if err := repo.AddWorktree(ctx, wtDir, branch, "main"); err != nil {
				t.Fatal(err)
			}
			wt, err := gitx.Open(wtDir)
			if err != nil {
				t.Fatal(err)
			}

			workerPrompt, err := prompts.Worker(repo.Dir, prompts.WorkerData{
				TaskID:       "T1",
				Title:        "Crear hola.txt",
				SpecPath:     "docs/spec.md",
				Branch:       branch,
				Instructions: `Crea el archivo hola.txt con exactamente el texto "hola" y haz commit en tu rama actual.`,
				SpecRefs:     []string{"REQ-1"},
				Checks:       []string{"git log -1"},
			})
			if err != nil {
				t.Fatal(err)
			}

			req := RunRequest{
				Mode:  Worker,
				Dir:   wt.Dir,
				Model: model,
			}
			if name == "claude" {
				sysFile := filepath.Join(t.TempDir(), "worker.md")
				if err := os.WriteFile(sysFile, []byte(workerPrompt), 0o600); err != nil {
					t.Fatal(err)
				}
				req.SystemPromptFile = sysFile
				req.Prompt = "Ejecuta la tarea T1 asignada."
			} else {
				req.Prompt = workerPrompt
			}

			started := time.Now()
			run, err := p.Start(ctx, req)
			if err != nil {
				t.Fatalf("Start() error: %v", err)
			}
			for range run.Events() {
			}
			final := run.Wait()
			elapsed := time.Since(started)

			t.Logf("provider=%s duration=%s session_id=%q", name, elapsed.Round(time.Millisecond), final.SessionID)
			if final.Err != nil {
				t.Fatalf("Final.Err = %+v\nText:\n%s", final.Err, final.Text)
			}

			rec, err := receipt.Extract(final.Text)
			if err != nil {
				t.Fatalf("receipt.Extract() error: %v\nText:\n%s", err, final.Text)
			}
			if err := rec.Validate("T1"); err != nil {
				t.Fatalf("receipt.Validate() error: %v\nReceipt: %+v", err, rec)
			}
			if rec.Status != "done" {
				t.Fatalf("rec.Status = %q, want \"done\"\nReceipt: %+v\nText:\n%s", rec.Status, rec, final.Text)
			}
			if len(rec.Commits) == 0 {
				t.Fatalf("rec.Commits vacío; el worker debe hacer commit\nReceipt: %+v", rec)
			}

			ver := verify.Verify(ctx, verify.Input{
				Worktree:   wt,
				Base:       "main",
				Branch:     branch,
				Receipt:    rec,
				TaskChecks: []string{"git log -1"},
				Timeout:    30 * time.Second,
			})
			if !ver.Passed {
				t.Fatalf("verify.Passed = false: %+v", ver)
			}
		})
	}
}
