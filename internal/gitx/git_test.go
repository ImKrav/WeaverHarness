package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) Repo {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mi repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Weaver Test")
	runGit(t, dir, "config", "user.email", "weaver@example.com")

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial commit")

	r, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	return r
}

func TestOpenNotGit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "no git aqui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if !errors.Is(err, ErrNotGit) {
		t.Fatalf("Open() error = %v, want ErrNotGit", err)
	}
}

func TestWorktreeCycle(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	wtPath := filepath.Join(WeaverDir(r.Dir), "r1", "a1")
	t.Cleanup(func() {
		_ = r.RemoveWorktree(ctx, wtPath)
		_ = os.RemoveAll(WeaverDir(r.Dir))
	})

	if err := r.AddWorktree(ctx, wtPath, "weaver/r1/a1", "main"); err != nil {
		t.Fatalf("AddWorktree() error: %v", err)
	}

	wtRepo, err := Open(wtPath)
	if err != nil {
		t.Fatalf("Open(wtPath) error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(wtPath, "nuevo.txt"), []byte("hola\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := wtRepo.CommitAll(ctx, "add nuevo.txt")
	if err != nil {
		t.Fatalf("CommitAll() error: %v", err)
	}
	if !committed {
		t.Fatal("CommitAll() = false, want true")
	}

	diff, err := r.DiffNames(ctx, "main", "weaver/r1/a1")
	if err != nil {
		t.Fatalf("DiffNames() error: %v", err)
	}
	if !slices.Equal(diff, []string{"nuevo.txt"}) {
		t.Fatalf("DiffNames() = %v, want [nuevo.txt]", diff)
	}

	anc, err := r.IsAncestor(ctx, "main", "weaver/r1/a1")
	if err != nil {
		t.Fatalf("IsAncestor() error: %v", err)
	}
	if !anc {
		t.Fatal("IsAncestor(main, weaver/r1/a1) = false, want true")
	}

	if err := r.RemoveWorktree(ctx, wtPath); err != nil {
		t.Fatalf("RemoveWorktree() error: %v", err)
	}
	if err := r.DeleteBranch(ctx, "weaver/r1/a1"); err != nil {
		t.Fatalf("DeleteBranch() error: %v", err)
	}

	if _, err := r.RevParse(ctx, "refs/heads/weaver/r1/a1"); err == nil {
		t.Fatal("branch weaver/r1/a1 still exists after DeleteBranch")
	}
}

func TestCommitAllClean(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	before, err := r.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := r.CommitAll(ctx, "should not commit")
	if err != nil {
		t.Fatalf("CommitAll() error: %v", err)
	}
	if committed {
		t.Fatal("CommitAll() = true on clean repo, want false")
	}
	after, err := r.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("HEAD moved from %s to %s on clean CommitAll", before, after)
	}
}

func TestMergeFFOnly(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	if err := r.SwitchNew(ctx, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitAll(ctx, "feat commit"); err != nil {
		t.Fatal(err)
	}
	featSHA, err := r.RevParse(ctx, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.Run(ctx, "switch", "main"); err != nil {
		t.Fatal(err)
	}
	if err := r.MergeFFOnly(ctx, "feat"); err != nil {
		t.Fatalf("MergeFFOnly(feat) error: %v", err)
	}
	mainSHA, err := r.RevParse(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if mainSHA != featSHA {
		t.Fatalf("main = %s, want %s after fast-forward", mainSHA, featSHA)
	}

	// Create divergent branch from initial commit (HEAD~1).
	if err := r.SwitchNew(ctx, "divergent", "HEAD~1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitAll(ctx, "divergent commit"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "switch", "main"); err != nil {
		t.Fatal(err)
	}
	if err := r.MergeFFOnly(ctx, "divergent"); err == nil {
		t.Fatal("MergeFFOnly(divergent) succeeded, want error")
	}
}

func TestRepoDiffAndDiffStat(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	if err := r.SwitchNew(ctx, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	accentName := "nota rápida.txt"
	if err := os.WriteFile(filepath.Join(r.Dir, accentName), []byte("línea 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "otro.txt"), []byte("dos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitAll(ctx, "add two files"); err != nil {
		t.Fatal(err)
	}

	stat, err := r.DiffStat(ctx, "main", "feat")
	if err != nil {
		t.Fatalf("DiffStat() error: %v", err)
	}
	if !strings.Contains(stat, accentName) || !strings.Contains(stat, "otro.txt") {
		t.Fatalf("DiffStat() = %q, want both %q and otro.txt", stat, accentName)
	}

	full, err := r.Diff(ctx, "main", "feat", "")
	if err != nil {
		t.Fatalf("Diff(all) error: %v", err)
	}
	if !strings.Contains(full, accentName) || !strings.Contains(full, "otro.txt") {
		t.Fatalf("Diff(all) = %q, want both files", full)
	}

	filtered, err := r.Diff(ctx, "main", "feat", accentName)
	if err != nil {
		t.Fatalf("Diff(filtered) error: %v", err)
	}
	if !strings.Contains(filtered, accentName) || strings.Contains(filtered, "otro.txt") {
		t.Fatalf("Diff(filtered) = %q, want only %q", filtered, accentName)
	}
}

func TestAttachWorktree(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	wtPath := filepath.Join(WeaverDir(r.Dir), "session-rec")
	t.Cleanup(func() {
		_ = r.RemoveWorktree(ctx, wtPath)
		_ = os.RemoveAll(WeaverDir(r.Dir))
	})

	if err := r.AddWorktree(ctx, wtPath, "weaver/rec", "main"); err != nil {
		t.Fatal(err)
	}
	wtRepo, err := Open(wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "saved.txt"), []byte("saved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wtRepo.CommitAll(ctx, "saved"); err != nil {
		t.Fatal(err)
	}
	wantSHA, err := wtRepo.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate manual/external deletion of the worktree directory while branch remains.
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}

	if err := r.AttachWorktree(ctx, wtPath, "weaver/rec"); err != nil {
		t.Fatalf("AttachWorktree() error: %v", err)
	}
	attached, err := Open(wtPath)
	if err != nil {
		t.Fatalf("Open(attached) error: %v", err)
	}
	gotSHA, err := attached.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if gotSHA != wantSHA {
		t.Fatalf("attached HEAD = %s, want %s", gotSHA, wantSHA)
	}
}

func TestAddWorktreePrunesStaleRegistration(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	wtPath := filepath.Join(WeaverDir(r.Dir), "stale-wt")
	t.Cleanup(func() {
		_ = r.RemoveWorktree(ctx, wtPath)
		_ = os.RemoveAll(WeaverDir(r.Dir))
	})

	if err := r.AddWorktree(ctx, wtPath, "weaver/stale-1", "main"); err != nil {
		t.Fatalf("initial AddWorktree() error: %v", err)
	}
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}

	if err := r.AddWorktree(ctx, wtPath, "weaver/stale-2", "main"); err != nil {
		t.Fatalf("AddWorktree() after stale directory deletion error: %v", err)
	}
	wtRepo, err := Open(wtPath)
	if err != nil {
		t.Fatalf("Open(wtPath) error: %v", err)
	}
	br, err := wtRepo.CurrentBranch(ctx)
	if err != nil || br != "weaver/stale-2" {
		t.Fatalf("CurrentBranch() = (%q, %v), want weaver/stale-2", br, err)
	}
}
