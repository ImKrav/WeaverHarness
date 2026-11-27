package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/run"
)

func seedCleanFixture(t *testing.T) (repo gitx.Repo, store run.Store, sharedSession, openAgentWT, openBranch, closedBranch, orphanBranch string) {
	t.Helper()
	repo = newAppTestRepo(t)
	ctx := context.Background()
	store = run.Store{Root: filepath.Join(repo.Dir, ".weaver", "runs")}

	weaverDir := gitx.WeaverDir(repo.Dir)
	sharedSession = filepath.Join(weaverDir, "session-run1")
	openAgentWT = filepath.Join(weaverDir, "run2-a1")
	closedBranch = "weaver/run-1"
	openBranch = "weaver/run-2"
	openAgentBranch := "weaver/run-2-a1"
	orphanBranch = "weaver/huerfana"

	if err := repo.AddWorktree(ctx, sharedSession, closedBranch, "main"); err != nil {
		t.Fatalf("AddWorktree(sharedSession): %v", err)
	}
	// Run 2 shares the same SessionDir and has its own integration branch + agent worktree.
	if _, err := repo.Run(ctx, "branch", openBranch, "main"); err != nil {
		t.Fatalf("branch(openBranch): %v", err)
	}
	if err := repo.AddWorktree(ctx, openAgentWT, openAgentBranch, "main"); err != nil {
		t.Fatalf("AddWorktree(openAgentWT): %v", err)
	}
	if _, err := repo.Run(ctx, "branch", orphanBranch, "main"); err != nil {
		t.Fatalf("branch(orphanBranch): %v", err)
	}

	if err := store.Save(&run.State{
		RunID:             "run-1",
		BaseBranch:        "main",
		IntegrationBranch: closedBranch,
		SessionDir:        sharedSession,
		Phase:             run.PhaseIntegrated,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&run.State{
		RunID:             "run-2",
		BaseBranch:        "main",
		IntegrationBranch: openBranch,
		SessionDir:        sharedSession,
		Phase:             run.PhasePlanApproved,
		Agents: []*run.Agent{
			{
				ID:       "a1",
				TaskID:   "T1",
				Branch:   openAgentBranch,
				Worktree: openAgentWT,
				Status:   run.AgentRunning,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return repo, store, sharedSession, openAgentWT, openBranch, closedBranch, orphanBranch
}

func TestCleanDefaultPreservesOpenRunsAndSharedSessionDir(t *testing.T) {
	repo, store, sharedSession, openAgentWT, openBranch, closedBranch, orphanBranch := seedCleanFixture(t)
	ctx := context.Background()

	var out bytes.Buffer
	if err := Clean(ctx, CleanOptions{
		Dir: repo.Dir,
		Out: &out,
	}); err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	// Shared SessionDir and open agent worktree must be preserved.
	if _, err := os.Stat(sharedSession); err != nil {
		t.Fatalf("shared SessionDir was deleted: %v", err)
	}
	if _, err := os.Stat(openAgentWT); err != nil {
		t.Fatalf("open agent worktree was deleted: %v", err)
	}
	// Run 1 (integrated) deleted; Run 2 (plan_approved) preserved.
	if _, err := store.Load("run-1"); err == nil {
		t.Fatalf("integrated run-1 state was not deleted")
	}
	if _, err := store.Load("run-2"); err != nil {
		t.Fatalf("open run-2 state was unexpectedly deleted: %v", err)
	}

	branches, err := repo.ListWeaverBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(branches, ",")
	if strings.Contains(joined, closedBranch) {
		t.Fatalf("closed branch %s remained: %v", closedBranch, branches)
	}
	if strings.Contains(joined, orphanBranch) {
		t.Fatalf("orphan branch %s remained: %v", orphanBranch, branches)
	}
	if !strings.Contains(joined, openBranch) {
		t.Fatalf("open branch %s was deleted: %v", openBranch, branches)
	}
}

func TestCleanDryRunChangesNothing(t *testing.T) {
	repo, store, sharedSession, openAgentWT, _, closedBranch, orphanBranch := seedCleanFixture(t)
	ctx := context.Background()

	var out bytes.Buffer
	if err := Clean(ctx, CleanOptions{
		Dir:    repo.Dir,
		DryRun: true,
		Out:    &out,
	}); err != nil {
		t.Fatalf("Clean(DryRun) error: %v", err)
	}
	if _, err := os.Stat(sharedSession); err != nil {
		t.Fatalf("DryRun modified sharedSession: %v", err)
	}
	if _, err := os.Stat(openAgentWT); err != nil {
		t.Fatalf("DryRun modified openAgentWT: %v", err)
	}
	if _, err := store.Load("run-1"); err != nil {
		t.Fatalf("DryRun deleted run-1: %v", err)
	}
	branches, err := repo.ListWeaverBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(branches, ",")
	if !strings.Contains(joined, closedBranch) || !strings.Contains(joined, orphanBranch) {
		t.Fatalf("DryRun deleted branches: %v", branches)
	}
	if !strings.Contains(out.String(), orphanBranch) {
		t.Fatalf("DryRun output did not mention %s:\n%s", orphanBranch, out.String())
	}
}

func TestCleanAllDeclinedAndForced(t *testing.T) {
	repo, store, sharedSession, openAgentWT, _, _, _ := seedCleanFixture(t)
	ctx := context.Background()

	// Declined -> nothing open is deleted.
	var declineOut bytes.Buffer
	if err := Clean(ctx, CleanOptions{
		Dir: repo.Dir,
		All: true,
		In:  strings.NewReader("n\n"),
		Out: &declineOut,
	}); err != nil {
		t.Fatalf("Clean(All, declined) error: %v", err)
	}
	if !strings.Contains(declineOut.String(), "[s/N]") {
		t.Fatalf("expected [s/N] confirmation prompt, got:\n%s", declineOut.String())
	}
	if _, err := store.Load("run-2"); err != nil {
		t.Fatalf("declined Clean(All) deleted open run-2: %v", err)
	}

	// Forced -> removes all weaver worktrees, branches, runs, and empty WeaverDir.
	var forceOut bytes.Buffer
	if err := Clean(ctx, CleanOptions{
		Dir:   repo.Dir,
		All:   true,
		Force: true,
		Out:   &forceOut,
	}); err != nil {
		t.Fatalf("Clean(All, Force) error: %v", err)
	}
	if _, err := os.Stat(sharedSession); !os.IsNotExist(err) {
		t.Fatalf("sharedSession remained after Clean(All, Force): %v", err)
	}
	if _, err := os.Stat(openAgentWT); !os.IsNotExist(err) {
		t.Fatalf("openAgentWT remained after Clean(All, Force): %v", err)
	}
	branches, err := repo.ListWeaverBranches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 0 {
		t.Fatalf("weaver branches remained after Clean(All, Force): %v", branches)
	}
	runs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs remained after Clean(All, Force): %d", len(runs))
	}
	if _, err := os.Stat(gitx.WeaverDir(repo.Dir)); !os.IsNotExist(err) {
		t.Fatalf("empty WeaverDir remained after Clean(All, Force)")
	}
}

func TestCleanRefusesRunningInsideWeaverWorktree(t *testing.T) {
	_, _, sharedSession, _, _, _, _ := seedCleanFixture(t)
	ctx := context.Background()

	var out bytes.Buffer
	err := Clean(ctx, CleanOptions{
		Dir: sharedSession,
		Out: &out,
	})
	if err == nil {
		t.Fatal("Clean inside weaver worktree succeeded, want error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "worktree") {
		t.Fatalf("expected worktree error message, got: %v", err)
	}
}
