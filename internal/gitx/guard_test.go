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
