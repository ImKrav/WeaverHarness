package verify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/receipt"
)

func TestMain(m *testing.M) {
	switch os.Getenv("VERIFY_TEST_HELPER") {
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "10kb":
		// Emit 10240 bytes ending with a recognizable 2000-byte suffix.
		prefix := strings.Repeat("A", 8240)
		suffix := strings.Repeat("Z", 2000)
		fmt.Print(prefix + suffix)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func newWorktreeRepo(t *testing.T) (repo gitx.Repo, wt gitx.Repo, commitSHA string) {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "verify repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Weaver Test")
	runGit(t, dir, "config", "user.email", "weaver@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")

	repo, err := gitx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	wtPath := filepath.Join(gitx.WeaverDir(repo.Dir), "r1", "a1")
	t.Cleanup(func() {
		_ = repo.RemoveWorktree(ctx, wtPath)
		_ = os.RemoveAll(gitx.WeaverDir(repo.Dir))
	})
	if err := repo.AddWorktree(ctx, wtPath, "weaver/r1/a1", "main"); err != nil {
		t.Fatal(err)
	}
	wt, err = gitx.Open(wtPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(wtPath, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := wt.CommitAll(ctx, "add hello.txt")
	if err != nil || !committed {
		t.Fatalf("CommitAll = (%v, %v)", committed, err)
	}
	commitSHA, err = wt.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return repo, wt, commitSHA
}

func TestVerifyAllGood(t *testing.T) {
	_, wt, sha := newWorktreeRepo(t)
	v := Verify(context.Background(), Input{
		Worktree: wt,
		Base:     "main",
		Branch:   "weaver/r1/a1",
		Receipt: receipt.Receipt{
			TaskID:  "T1",
			Status:  "done",
			Summary: "ok",
			Changes: []receipt.Change{{Path: "hello.txt", Kind: "added"}},
			Commits: []string{sha},
			Verification: []receipt.Check{
				{Cmd: "echo extra", ExitCode: 0},
			},
		},
		TaskChecks: []string{"echo task"},
		Timeout:    5 * time.Second,
	})

	if !v.Passed {
		t.Fatalf("Passed = false, want true: %+v", v)
	}
	if len(v.CommitsMissing) != 0 || len(v.OnlyInReceipt) != 0 || len(v.OnlyInDiff) != 0 {
		t.Fatalf("unexpected diffs/missing: %+v", v)
	}
	if len(v.Checks) != 2 {
		t.Fatalf("len(Checks) = %d, want 2", len(v.Checks))
	}
}

func TestVerifyMissingCommit(t *testing.T) {
	_, wt, _ := newWorktreeRepo(t)
	v := Verify(context.Background(), Input{
		Worktree: wt,
		Base:     "main",
		Branch:   "weaver/r1/a1",
		Receipt: receipt.Receipt{
			TaskID:  "T1",
			Status:  "done",
			Summary: "ok",
			Changes: []receipt.Change{{Path: "hello.txt", Kind: "added"}},
			Commits: []string{"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
		},
		TaskChecks: []string{"echo ok"},
		Timeout:    5 * time.Second,
	})

	if v.Passed {
		t.Fatalf("Passed = true, want false on missing commit")
	}
	if !slices.Equal(v.CommitsMissing, []string{"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}) {
		t.Fatalf("CommitsMissing = %v", v.CommitsMissing)
	}
}

func TestVerifyChangesMismatchReportedOnly(t *testing.T) {
	_, wt, sha := newWorktreeRepo(t)
	v := Verify(context.Background(), Input{
		Worktree: wt,
		Base:     "main",
		Branch:   "weaver/r1/a1",
		Receipt: receipt.Receipt{
			TaskID:  "T1",
			Status:  "done",
			Summary: "ok",
			Changes: []receipt.Change{{Path: "phantom.txt", Kind: "added"}},
			Commits: []string{sha},
		},
		TaskChecks: []string{"echo ok"},
		Timeout:    5 * time.Second,
	})

	if !v.Passed {
		t.Fatalf("Passed = false, want true (changes mismatch is reported only): %+v", v)
	}
	if !slices.Equal(v.OnlyInReceipt, []string{"phantom.txt"}) {
		t.Fatalf("OnlyInReceipt = %v, want [phantom.txt]", v.OnlyInReceipt)
	}
	if !slices.Equal(v.OnlyInDiff, []string{"hello.txt"}) {
		t.Fatalf("OnlyInDiff = %v, want [hello.txt]", v.OnlyInDiff)
	}
}

func TestVerifyClaimMismatch(t *testing.T) {
	_, wt, sha := newWorktreeRepo(t)
	v := Verify(context.Background(), Input{
		Worktree: wt,
		Base:     "main",
		Branch:   "weaver/r1/a1",
		Receipt: receipt.Receipt{
			TaskID:  "T1",
			Status:  "done",
			Summary: "ok",
			Changes: []receipt.Change{{Path: "hello.txt", Kind: "added"}},
			Commits: []string{sha},
			Verification: []receipt.Check{
				{Cmd: "exit 1", ExitCode: 0},
			},
		},
		Timeout: 5 * time.Second,
	})

	if v.Passed {
		t.Fatal("Passed = true, want false when claimed exit 0 actually exits 1")
	}
	if len(v.Checks) != 1 || v.Checks[0].Passed || v.Checks[0].ExitCode != 1 || v.Checks[0].Source != "receipt" {
		t.Fatalf("unexpected Checks: %+v", v.Checks)
	}
}

func TestVerifyDedup(t *testing.T) {
	_, wt, sha := newWorktreeRepo(t)
	v := Verify(context.Background(), Input{
		Worktree: wt,
		Base:     "main",
		Branch:   "weaver/r1/a1",
		Receipt: receipt.Receipt{
			TaskID:  "T1",
			Status:  "done",
			Summary: "ok",
			Changes: []receipt.Change{{Path: "hello.txt", Kind: "added"}},
			Commits: []string{sha},
			Verification: []receipt.Check{
				{Cmd: "echo same", ExitCode: 0},
			},
		},
		TaskChecks: []string{"echo same"},
		Timeout:    5 * time.Second,
	})

	if !v.Passed {
		t.Fatalf("Passed = false: %+v", v)
	}
	if len(v.Checks) != 1 {
		t.Fatalf("len(Checks) = %d, want 1 (deduplicated)", len(v.Checks))
	}
	if v.Checks[0].Source != "task" {
		t.Fatalf("Checks[0].Source = %q, want %q", v.Checks[0].Source, "task")
	}
}

func TestRunCheckTimeout(t *testing.T) {
	t.Setenv("VERIFY_TEST_HELPER", "sleep")
	cmdStr := `"` + os.Args[0] + `"`

	start := time.Now()
	exit, _, timedOut := RunCheck(context.Background(), t.TempDir(), cmdStr, 200*time.Millisecond)
	elapsed := time.Since(start)

	if !timedOut {
		t.Fatalf("timedOut = false, want true")
	}
	if exit == 0 {
		t.Fatalf("exit = 0, want non-zero on timeout")
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("RunCheck took %v on 200ms timeout, want < 2s", elapsed)
	}
}

func TestRunCheckTail(t *testing.T) {
	t.Setenv("VERIFY_TEST_HELPER", "10kb")
	cmdStr := `"` + os.Args[0] + `"`

	exit, tail, timedOut := RunCheck(context.Background(), t.TempDir(), cmdStr, 5*time.Second)
	if timedOut || exit != 0 {
		t.Fatalf("RunCheck() = (exit=%d, timedOut=%v), want (0, false)", exit, timedOut)
	}
	if len(tail) != 2000 {
		t.Fatalf("len(tail) = %d, want 2000", len(tail))
	}
	if tail != strings.Repeat("Z", 2000) {
		t.Fatalf("tail did not preserve last 2000 bytes accurately")
	}
}

func TestCompareChangesNormalizesBackslashesAndDotPrefix(t *testing.T) {
	_, wt, _ := newWorktreeRepo(t)
	ctx := context.Background()
	subDir := filepath.Join(wt.Dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file.go"), []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := wt.CommitAll(ctx, "add sub/file.go")
	if err != nil || !committed {
		t.Fatalf("CommitAll = (%v, %v)", committed, err)
	}
	sha, err := wt.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	v := Verify(ctx, Input{
		Worktree: wt,
		Base:     "main",
		Branch:   "weaver/r1/a1",
		Receipt: receipt.Receipt{
			TaskID:  "T1",
			Status:  "done",
			Summary: "ok",
			Changes: []receipt.Change{
				{Path: `.\hello.txt`, Kind: "added"},
				{Path: `sub\file.go`, Kind: "added"},
			},
			Commits: []string{sha},
		},
		TaskChecks: []string{"echo ok"},
		Timeout:    5 * time.Second,
	})

	if len(v.OnlyInReceipt) != 0 || len(v.OnlyInDiff) != 0 {
		t.Fatalf("OnlyInReceipt=%v OnlyInDiff=%v, want both empty", v.OnlyInReceipt, v.OnlyInDiff)
	}
}
