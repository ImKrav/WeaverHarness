package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGuardRestoresUserBranch(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	if err := os.WriteFile(filepath.Join(r.Dir, "two.txt"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitAll(ctx, "second commit"); err != nil {
		t.Fatal(err)
	}
	origMain, err := r.RevParse(ctx, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	firstCommit, err := r.RevParse(ctx, "main~1")
	if err != nil {
		t.Fatal(err)
	}

	// Detach HEAD so `git branch -f main` can move main.
	if _, err := r.Run(ctx, "switch", "--detach"); err != nil {
		t.Fatal(err)
	}

	g, err := NewGuard(ctx, r)
	if err != nil {
		t.Fatalf("NewGuard() error: %v", err)
	}

	if _, err := r.Run(ctx, "branch", "-f", "main", firstCommit); err != nil {
		t.Fatal(err)
	}

	violations, err := g.Check(ctx)
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("len(violations) = %d, want 1: %+v", len(violations), violations)
	}
	v := violations[0]
	if v.Kind != "moved" || v.Ref != "refs/heads/main" || !v.Restored || v.Before != origMain || v.After != firstCommit {
		t.Fatalf("unexpected violation: %+v", v)
	}

	afterMain, err := r.RevParse(ctx, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if afterMain != origMain {
		t.Fatalf("main = %s, want restored %s", afterMain, origMain)
	}

	violations2, err := g.Check(ctx)
	if err != nil {
		t.Fatalf("second Check() error: %v", err)
	}
	if len(violations2) != 0 {
		t.Fatalf("second Check() = %+v, want empty", violations2)
	}
}

func TestGuardIgnoresWeaverRefs(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	g, err := NewGuard(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	wtPath := filepath.Join(WeaverDir(r.Dir), "r1", "a1")
	t.Cleanup(func() {
		_ = r.RemoveWorktree(ctx, wtPath)
		_ = os.RemoveAll(WeaverDir(r.Dir))
	})

	if err := r.AddWorktree(ctx, wtPath, "weaver/r1/a1", "main"); err != nil {
		t.Fatal(err)
	}
	wtRepo, err := Open(wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "w.txt"), []byte("w\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wtRepo.CommitAll(ctx, "weaver work"); err != nil {
		t.Fatal(err)
	}

	violations, err := g.Check(ctx)
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("Check() = %+v, want empty for weaver refs/worktrees", violations)
	}
}

func TestGuardReportsCreatedAndRemote(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	headSHA, err := r.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "update-ref", "refs/remotes/origin/main", headSHA); err != nil {
		t.Fatal(err)
	}

	g, err := NewGuard(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	// Create a new local branch outside weaver/.
	if _, err := r.Run(ctx, "branch", "foo", headSHA); err != nil {
		t.Fatal(err)
	}
	// Create an unreachable commit object and move the remote ref without touching main.
	treeSHA, err := r.Run(ctx, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	altSHA, err := r.Run(ctx, "commit-tree", treeSHA, "-p", headSHA, "-m", "alt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "update-ref", "refs/remotes/origin/main", altSHA); err != nil {
		t.Fatal(err)
	}

	violations, err := g.Check(ctx)
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}

	var sawCreated, sawRemote bool
	for _, v := range violations {
		if v.Kind == "created" && v.Ref == "refs/heads/foo" && !v.Restored {
			sawCreated = true
		}
		if v.Kind == "remote_changed" && v.Ref == "refs/remotes/origin/main" && !v.Restored {
			sawRemote = true
		}
	}
	if !sawCreated {
		t.Fatalf("expected created violation for refs/heads/foo in %+v", violations)
	}
	if !sawRemote {
		t.Fatalf("expected remote_changed violation for refs/remotes/origin/main in %+v", violations)
	}

	// Verify foo was not deleted.
	if _, err := r.RevParse(ctx, "refs/heads/foo"); err != nil {
		t.Fatalf("branch foo should not be deleted: %v", err)
	}
}

func TestGuardCheckExceptProtectsOtherWeaverBranches(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)

	headSHA, err := r.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "branch", "weaver/run1", headSHA); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "branch", "weaver/run1-a1", headSHA); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "branch", "weaver/run1-a2", headSHA); err != nil {
		t.Fatal(err)
	}

	g, err := NewGuard(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	treeSHA, err := r.Run(ctx, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	altSHA, err := r.Run(ctx, "commit-tree", treeSHA, "-p", headSHA, "-m", "alt")
	if err != nil {
		t.Fatal(err)
	}

	// Mutate weaver/run1, weaver/run1-a1, and allowed weaver/run1-a2.
	if _, err := r.Run(ctx, "update-ref", "refs/heads/weaver/run1", altSHA); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "update-ref", "refs/heads/weaver/run1-a1", altSHA); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "update-ref", "refs/heads/weaver/run1-a2", altSHA); err != nil {
		t.Fatal(err)
	}

	violations, err := g.CheckExcept(ctx, "refs/heads/weaver/run1-a2")
	if err != nil {
		t.Fatalf("CheckExcept() error: %v", err)
	}
	if len(violations) != 2 {
		t.Fatalf("len(violations) = %d, want 2 (run1 and run1-a1): %+v", len(violations), violations)
	}

	gotRun1, _ := r.RevParse(ctx, "refs/heads/weaver/run1")
	gotA1, _ := r.RevParse(ctx, "refs/heads/weaver/run1-a1")
	gotA2, _ := r.RevParse(ctx, "refs/heads/weaver/run1-a2")
	if gotRun1 != headSHA || gotA1 != headSHA {
		t.Fatalf("protected weaver branches not restored: run1=%s a1=%s want=%s", gotRun1, gotA1, headSHA)
	}
	if gotA2 != altSHA {
		t.Fatalf("allowed weaver/run1-a2 = %s, want %s", gotA2, altSHA)
	}
}

func TestGuardHandlesSymlinkedRepoPath(t *testing.T) {
	base := t.TempDir()
	realParent := filepath.Join(base, "real")
	linkParent := filepath.Join(base, "link")
	if err := os.MkdirAll(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Skipf("symlinks no disponibles en este entorno: %v", err)
	}

	realRepoDir := filepath.Join(realParent, "repo")
	if err := os.MkdirAll(realRepoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, realRepoDir, "init", "-b", "main")
	runGit(t, realRepoDir, "config", "user.name", "Weaver Test")
	runGit(t, realRepoDir, "config", "user.email", "weaver@example.com")
	if err := os.WriteFile(filepath.Join(realRepoDir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, realRepoDir, "add", "README.md")
	runGit(t, realRepoDir, "commit", "-m", "initial")

	linkRepo := Repo{Dir: filepath.Join(linkParent, "repo")}
	wtReal := filepath.Join(realParent, "repo-weaver", "r1", "a1")
	wtLink := filepath.Join(linkParent, "repo-weaver", "r1", "a1")

	if !isUnderDir(wtReal, WeaverDir(linkRepo.Dir)) {
		t.Fatalf("isUnderDir(%q, %q) = false, want true across symlinked parent", wtReal, WeaverDir(linkRepo.Dir))
	}

	ctx := context.Background()
	t.Cleanup(func() {
		_ = linkRepo.RemoveWorktree(ctx, wtLink)
		_ = os.RemoveAll(WeaverDir(realRepoDir))
	})
	if err := linkRepo.AddWorktree(ctx, wtLink, "weaver/r1/a1", "main"); err != nil {
		t.Fatal(err)
	}

	g, err := NewGuard(ctx, linkRepo)
	if err != nil {
		t.Fatal(err)
	}
	wtRepo, err := Open(wtReal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtReal, "sym.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wtRepo.CommitAll(ctx, "commit via real path"); err != nil {
		t.Fatal(err)
	}

	violations, err := g.Check(ctx)
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("Check() = %+v, want no violations for symlinked weaver worktree", violations)
	}
}
