package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/run"
)

func setupAcceptedTaskInEngine(t *testing.T, globalChecks []string, mergeIntoSession bool) (*Engine, gitx.Repo, *fakeApprover, string) {
	t.Helper()
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}
	app := &fakeApprover{approved: true}

	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			if err := os.WriteFile(filepath.Join(req.Dir, "feature.txt"), []byte("feat\n"), 0o644); err != nil {
				return nil, err
			}
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "add feature.txt"); err != nil {
				return nil, err
			}
			sha, err := wt.RevParse(ctx, "HEAD")
			if err != nil {
				return nil, err
			}
			return newFakeRun("sid-int", []string{`{"ok":1}`}, provider.Final{
				Text:      receiptBlock("T1", "done", "feature.txt", []string{sha}, "echo ok"),
				SessionID: "sid-int",
			}), nil
		},
	}

	eng, err := New(ctx, Options{
		Cfg:       testConfig(),
		Repo:      r,
		Store:     st,
		Providers: map[string]provider.Provider{"codex": prov},
		Approver:  app,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = eng.Close(context.Background())
	})

	if _, err := eng.StartRun(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := eng.RequestApproval(ctx, ApprovalRequest{Kind: "spec", Path: "docs/spec.md", Summary: "s"}); err != nil || !ok {
		t.Fatalf("spec failed: %v %v", ok, err)
	}
	plan := &run.Plan{
		Tasks: []*run.Task{
			{ID: "T1", Title: "Feature", Role: "implementer", SpecRefs: []string{"REQ-1"}, Checks: []string{"echo ok"}},
		},
		GlobalChecks: globalChecks,
	}
	if ok, _, err := eng.RequestApproval(ctx, ApprovalRequest{Kind: "plan", Path: "docs/plan.md", Summary: "p", Plan: plan}); err != nil || !ok {
		t.Fatalf("plan failed: %v %v", ok, err)
	}

	id, err := eng.Spawn(ctx, "T1", "", "", "", "implementa feature")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{id}, 10*time.Second)

	if err := eng.AcceptTask(ctx, "T1", id); err != nil {
		t.Fatalf("AcceptTask() error: %v", err)
	}

	agBranch := eng.State().Agent(id).Branch
	if mergeIntoSession {
		sessRepo, err := gitx.Open(eng.State().SessionDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessRepo.MergeFFOnly(ctx, agBranch); err != nil {
			t.Fatalf("MergeFFOnly into session error: %v", err)
		}
	}
	return eng, r, app, agBranch
}

func TestIntegrationBlockedByUnmergedTask(t *testing.T) {
	ctx := context.Background()
	eng, _, app, _ := setupAcceptedTaskInEngine(t, []string{"echo ok"}, false)
	beforeCalls := app.Calls()

	_, _, err := eng.RequestApproval(ctx, ApprovalRequest{
		Kind:    "integration",
		Path:    "docs/plan.md",
		Summary: "merge",
	})
	if err == nil {
		t.Fatal("RequestApproval(integration) succeeded with unmerged accepted task, want error")
	}
	if !strings.Contains(err.Error(), "T1") {
		t.Fatalf("error = %q, want task name T1", err.Error())
	}
	if app.Calls() != beforeCalls {
		t.Fatalf("Approver should not be called when task is unmerged")
	}
}

func TestIntegrationGlobalCheckFails(t *testing.T) {
	ctx := context.Background()
	eng, _, app, _ := setupAcceptedTaskInEngine(t, []string{"exit 1"}, true)
	beforeCalls := app.Calls()

	_, _, err := eng.RequestApproval(ctx, ApprovalRequest{
		Kind:    "integration",
		Path:    "docs/plan.md",
		Summary: "merge",
	})
	if err == nil {
		t.Fatal("RequestApproval(integration) succeeded when global check failed, want error")
	}
	if app.Calls() != beforeCalls {
		t.Fatalf("Approver should not be called when global check fails")
	}
}

func TestIntegrationFastForward(t *testing.T) {
	ctx := context.Background()
	eng, r, _, _ := setupAcceptedTaskInEngine(t, []string{"echo ok"}, true)

	integSHA, err := r.RevParse(ctx, eng.State().IntegrationBranch)
	if err != nil {
		t.Fatal(err)
	}

	approved, _, err := eng.RequestApproval(ctx, ApprovalRequest{
		Kind:    "integration",
		Path:    "docs/plan.md",
		Summary: "merge",
	})
	if err != nil || !approved {
		t.Fatalf("RequestApproval(integration) = (%v, %v), want (true, nil)", approved, err)
	}

	mainSHA, err := r.RevParse(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if mainSHA != integSHA {
		t.Fatalf("main = %s, want %s after fast-forward", mainSHA, integSHA)
	}
	if eng.State().Phase != run.PhaseIntegrated {
		t.Fatalf("Phase = %s, want integrated", eng.State().Phase)
	}

	// Guard must have rebased, so CheckRefs reports no violation.
	if violations := eng.CheckRefs(ctx); len(violations) != 0 {
		t.Fatalf("CheckRefs() after integration = %+v, want empty", violations)
	}
}

func TestIntegrationNonFF(t *testing.T) {
	ctx := context.Background()
	eng, r, _, _ := setupAcceptedTaskInEngine(t, []string{"echo ok"}, true)
	integBranch := eng.State().IntegrationBranch

	// Advance main independently so fast-forward fails.
	if err := os.WriteFile(filepath.Join(r.Dir, "diverge.txt"), []byte("div\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitAll(ctx, "diverge main"); err != nil {
		t.Fatal(err)
	}
	mainBefore, err := r.RevParse(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}

	approved, msg, err := eng.RequestApproval(ctx, ApprovalRequest{
		Kind:    "integration",
		Path:    "docs/plan.md",
		Summary: "merge",
	})
	if err != nil || !approved {
		t.Fatalf("RequestApproval(integration) = (%v, %q, %v)", approved, msg, err)
	}

	mainAfter, err := r.RevParse(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if mainAfter != mainBefore {
		t.Fatalf("main mutated on non-FF integration: %s -> %s", mainBefore, mainAfter)
	}
	if !strings.Contains(msg, integBranch) {
		t.Fatalf("integration message = %q, want mention of %q", msg, integBranch)
	}
}

func TestNextRunAfterIntegration(t *testing.T) {
	ctx := context.Background()
	eng, r, _, _ := setupAcceptedTaskInEngine(t, []string{"echo ok"}, true)
	firstRunID := eng.State().RunID
	sessionDir := eng.State().SessionDir

	if ok, _, err := eng.RequestApproval(ctx, ApprovalRequest{Kind: "integration", Path: "docs/plan.md", Summary: "merge"}); err != nil || !ok {
		t.Fatalf("integration failed: %v", err)
	}

	mainSHA, err := r.RevParse(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}

	nextState, err := eng.StartRun(ctx)
	if err != nil {
		t.Fatalf("second StartRun() error: %v", err)
	}
	if nextState.RunID == firstRunID {
		t.Fatalf("second RunID %q collided with first RunID", nextState.RunID)
	}
	if nextState.Phase != run.PhaseOpen {
		t.Fatalf("second Phase = %s, want open", nextState.Phase)
	}
	if nextState.SessionDir != sessionDir {
		t.Fatalf("SessionDir = %q, want reused %q", nextState.SessionDir, sessionDir)
	}
	nextHead, err := r.RevParse(ctx, nextState.IntegrationBranch)
	if err != nil {
		t.Fatal(err)
	}
	if nextHead != mainSHA {
		t.Fatalf("next IntegrationBranch HEAD = %s, want updated main %s", nextHead, mainSHA)
	}
}

func TestIntegrationAfterCleanup(t *testing.T) {
	ctx := context.Background()
	eng, r, _, _ := setupAcceptedTaskInEngine(t, []string{"echo ok"}, true)

	acceptedAgent := eng.State().Task("T1").AcceptedAgent
	if err := eng.Cleanup(ctx, acceptedAgent); err != nil {
		t.Fatalf("Cleanup() error: %v", err)
	}

	integSHA, err := r.RevParse(ctx, eng.State().IntegrationBranch)
	if err != nil {
		t.Fatal(err)
	}

	approved, _, err := eng.RequestApproval(ctx, ApprovalRequest{
		Kind:    "integration",
		Path:    "docs/plan.md",
		Summary: "merge",
	})
	if err != nil || !approved {
		t.Fatalf("RequestApproval(integration) after Cleanup = (%v, %v), want (true, nil)", approved, err)
	}

	mainSHA, err := r.RevParse(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if mainSHA != integSHA {
		t.Fatalf("main = %s, want %s after fast-forward", mainSHA, integSHA)
	}
}
