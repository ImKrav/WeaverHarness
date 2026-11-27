package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/config"
	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/ImKrav/weaverharness/internal/verify"
)

func setupPlanApprovedEngine(t *testing.T, cfg config.Config, prov provider.Provider, notify func(any)) (*Engine, gitx.Repo, run.Store) {
	t.Helper()
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}
	app := &fakeApprover{approved: true}

	eng, err := New(ctx, Options{
		Cfg:       cfg,
		Repo:      r,
		Store:     st,
		Providers: map[string]provider.Provider{"codex": prov},
		Approver:  app,
		Notify:    notify,
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
		t.Fatalf("spec approval failed: %v %v", ok, err)
	}
	plan := &run.Plan{
		Tasks: []*run.Task{
			{ID: "T1", Title: "Primera", Role: "implementer", SpecRefs: []string{"REQ-1"}, Checks: []string{"echo ok"}},
			{ID: "T2", Title: "Segunda", Role: "implementer", SpecRefs: []string{"REQ-1"}, Checks: []string{"echo ok"}},
		},
		GlobalChecks: []string{"echo ok"},
	}
	if ok, _, err := eng.RequestApproval(ctx, ApprovalRequest{Kind: "plan", Path: "docs/plan.md", Summary: "p", Plan: plan}); err != nil || !ok {
		t.Fatalf("plan approval failed: %v %v", ok, err)
	}
	return eng, r, st
}

func TestSpawnBeforePlanRejected(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}

	eng, err := New(ctx, Options{
		Cfg:       testConfig(),
		Repo:      r,
		Store:     st,
		Providers: map[string]provider.Provider{"codex": &fakeProvider{name: "codex"}},
		Approver:  &fakeApprover{approved: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	if _, err := eng.StartRun(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = eng.Spawn(ctx, "T1", "", "", "", "haz algo")
	if !errors.Is(err, run.ErrPlanNotApproved) {
		t.Fatalf("Spawn() error = %v, want ErrPlanNotApproved", err)
	}
}

func TestSpawnHappyPath(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			if err := os.WriteFile(filepath.Join(req.Dir, "work.txt"), []byte("done\n"), 0o644); err != nil {
				return nil, err
			}
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "add work.txt"); err != nil {
				return nil, err
			}
			sha, err := wt.RevParse(ctx, "HEAD")
			if err != nil {
				return nil, err
			}
			text := receiptBlock("T1", "done", "work.txt", []string{sha}, "echo ok")
			return newFakeRun("sid-1", []string{`{"raw":"line1"}`}, provider.Final{Text: text, SessionID: "sid-1"}), nil
		},
	}

	eng, _, st := setupPlanApprovedEngine(t, testConfig(), prov, nil)

	id, err := eng.Spawn(ctx, "T1", "", "", "", "crea work.txt")
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	results := eng.Wait(ctx, []string{id}, 10*time.Second)
	if len(results) != 1 || results[0].Status != string(run.AgentFinished) {
		t.Fatalf("Wait() = %+v", results)
	}
	if results[0].Verification == nil || !results[0].Verification.Passed {
		t.Fatalf("Verification = %+v, want Passed=true", results[0].Verification)
	}
	if eng.State().Task("T1").Status != run.TaskReceiptOK {
		t.Fatalf("T1 status = %s, want receipt_ok", eng.State().Task("T1").Status)
	}

	verBytes, err := os.ReadFile(filepath.Join(st.AgentDir(eng.State().RunID, id), "verification.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ver verify.Verification
	if err := json.Unmarshal(verBytes, &ver); err != nil || !ver.Passed {
		t.Fatalf("persisted verification.json = %s, err=%v", string(verBytes), err)
	}
}

func TestAutoCommit(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			// Leave uncommitted file.
			if err := os.WriteFile(filepath.Join(req.Dir, "uncommitted.txt"), []byte("dirty\n"), 0o644); err != nil {
				return nil, err
			}
			text := receiptBlock("T1", "done", "uncommitted.txt", nil, "echo ok")
			return newFakeRun("sid-1", []string{`{"ok":1}`}, provider.Final{Text: text, SessionID: "sid-1"}), nil
		},
	}

	eng, r, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "crea uncommitted.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{id}, 10*time.Second)

	ag := eng.State().Agent(id)
	msg, err := r.Run(ctx, "log", "-1", "--pretty=%s", ag.Branch)
	if err != nil {
		t.Fatal(err)
	}
	wantMsg := "weaver: cambios sin commit de " + id
	if msg != wantMsg {
		t.Fatalf("last commit message = %q, want %q", msg, wantMsg)
	}
}

func TestReceiptRetry(t *testing.T) {
	ctx := context.Background()

	t.Run("succeeds on retry without incrementing rounds", func(t *testing.T) {
		prov := &fakeProvider{
			name: "codex",
			handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
				if call == 1 {
					return newFakeRun("sid-retry", []string{`{"step":1}`}, provider.Final{Text: "Olvidé el recibo", SessionID: "sid-retry"}), nil
				}
				if req.ResumeID != "sid-retry" {
					t.Fatalf("retry ResumeID = %q, want sid-retry", req.ResumeID)
				}
				return newFakeRun("sid-retry", []string{`{"step":2}`}, provider.Final{
					Text:      receiptBlock("T1", "done", "README.md", nil, "echo ok"),
					SessionID: "sid-retry",
				}), nil
			},
		}
		eng, _, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
		id, err := eng.Spawn(ctx, "T1", "", "", "", "haz algo")
		if err != nil {
			t.Fatal(err)
		}
		res := eng.Wait(ctx, []string{id}, 10*time.Second)
		if res[0].Status != string(run.AgentFinished) {
			t.Fatalf("status = %s, err = %s, want finished", res[0].Status, res[0].Err)
		}
		if eng.State().Task("T1").Status != run.TaskReceiptOK {
			t.Fatalf("T1 status = %s, want receipt_ok", eng.State().Task("T1").Status)
		}
		if eng.State().Task("T1").Rounds != 0 {
			t.Fatalf("T1 rounds = %d, want 0", eng.State().Task("T1").Rounds)
		}
	})

	t.Run("fails with no_receipt after two misses", func(t *testing.T) {
		prov := &fakeProvider{
			name: "codex",
			handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
				return newFakeRun("sid-miss", []string{`{"step":1}`}, provider.Final{Text: "Sin recibo otra vez", SessionID: "sid-miss"}), nil
			},
		}
		eng, _, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
		id, err := eng.Spawn(ctx, "T1", "", "", "", "haz algo")
		if err != nil {
			t.Fatal(err)
		}
		res := eng.Wait(ctx, []string{id}, 10*time.Second)
		if res[0].Status != string(run.AgentFailed) || res[0].Err != "no_receipt" {
			t.Fatalf("result = %+v, want failed / no_receipt", res[0])
		}
		if eng.State().Task("T1").Status != run.TaskReceiptFailed {
			t.Fatalf("T1 status = %s, want receipt_failed", eng.State().Task("T1").Status)
		}
	})
}

func TestRefViolationFailsVerification(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(req.Dir, "x.txt"), []byte("x\n"), 0o644); err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "commit"); err != nil {
				return nil, err
			}
			sha, _ := wt.RevParse(ctx, "HEAD")
			// Rogue mutation: move main to this branch's new commit!
			if _, err := wt.Run(ctx, "update-ref", "refs/heads/main", sha); err != nil {
				return nil, err
			}
			return newFakeRun("sid-rogue", []string{`{"ok":1}`}, provider.Final{
				Text:      receiptBlock("T1", "done", "x.txt", []string{sha}, "echo ok"),
				SessionID: "sid-rogue",
			}), nil
		},
	}

	eng, r, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	origMain, err := r.RevParse(ctx, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}

	id, err := eng.Spawn(ctx, "T1", "", "", "", "mueve main")
	if err != nil {
		t.Fatal(err)
	}
	res := eng.Wait(ctx, []string{id}, 10*time.Second)

	afterMain, err := r.RevParse(ctx, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if afterMain != origMain {
		t.Fatalf("main was not restored: got %s, want %s", afterMain, origMain)
	}
	if res[0].Verification == nil || res[0].Verification.Passed {
		t.Fatalf("expected Verification.Passed == false on ref violation: %+v", res[0].Verification)
	}
	if len(res[0].Verification.RefViolations) == 0 {
		t.Fatalf("expected RefViolations recorded")
	}
}

func TestMaxParallelQueues(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	pc := cfg.Providers["codex"]
	pc.MaxParallel = 1
	cfg.Providers["codex"] = pc

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var mu sync.Mutex
	var maxConcurrent, active int

	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			mu.Lock()
			active++
			if active > maxConcurrent {
				maxConcurrent = active
			}
			mu.Unlock()

			if call == 1 {
				close(firstStarted)
				<-releaseFirst
			}

			mu.Lock()
			active--
			mu.Unlock()

			taskID := "T1"
			if call == 2 {
				taskID = "T2"
			}
			return newFakeRun("sid", []string{`{"ok":1}`}, provider.Final{
				Text: receiptBlock(taskID, "done", "README.md", nil, "echo ok"),
			}), nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, cfg, prov, nil)

	id1, err := eng.Spawn(ctx, "T1", "", "", "", "t1")
	if err != nil {
		t.Fatal(err)
	}
	<-firstStarted

	id2, err := eng.Spawn(ctx, "T2", "", "", "", "t2")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	st2 := eng.Status(id2)
	if len(st2) != 1 || st2[0].Status != string(run.AgentQueued) {
		t.Fatalf("second agent status = %+v, want queued while first runs", st2)
	}

	close(releaseFirst)
	_ = eng.Wait(ctx, []string{id1, id2}, 10*time.Second)

	if maxConcurrent != 1 {
		t.Fatalf("maxConcurrent = %d, want 1", maxConcurrent)
	}
}

func TestFollowUpRounds(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			return newFakeRun("sid-fu", []string{`{"ok":1}`}, provider.Final{
				Text:      receiptBlock("T1", "done", "README.md", nil, "echo ok"),
				SessionID: "sid-fu",
			}), nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "t1")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{id}, 10*time.Second)

	for round := 1; round <= 2; round++ {
		if err := eng.FollowUp(ctx, id, "corrige"); err != nil {
			t.Fatalf("FollowUp round %d error: %v", round, err)
		}
		_ = eng.Wait(ctx, []string{id}, 10*time.Second)
	}

	err = eng.FollowUp(ctx, id, "tercera")
	if !errors.Is(err, run.ErrRoundsExhausted) {
		t.Fatalf("3rd FollowUp error = %v, want ErrRoundsExhausted", err)
	}
}

func TestAcceptRequiresVerified(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			return newFakeRun("sid-bad", []string{`{"ok":1}`}, provider.Final{
				// Check "exit 1" fails verification.
				Text: receiptBlock("T1", "done", "README.md", nil, "exit 1"),
			}), nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "t1")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{id}, 10*time.Second)

	if err := eng.AcceptTask(ctx, "T1", id); err == nil {
		t.Fatal("AcceptTask() succeeded when verification failed, want error")
	}
}

func TestCleanupRequiresMerged(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			if err := os.WriteFile(filepath.Join(req.Dir, "c.txt"), []byte("c\n"), 0o644); err != nil {
				return nil, err
			}
			wt, _ := gitx.Open(req.Dir)
			_, _ = wt.CommitAll(ctx, "commit c")
			sha, _ := wt.RevParse(ctx, "HEAD")
			return newFakeRun("sid-c", []string{`{"ok":1}`}, provider.Final{
				Text: receiptBlock("T1", "done", "c.txt", []string{sha}, "echo ok"),
			}), nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "t1")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{id}, 10*time.Second)

	if err := eng.Cleanup(ctx, id); err == nil {
		t.Fatal("Cleanup() succeeded before branch merged into integration branch, want error")
	}

	// Merge agent branch into session worktree, then Cleanup should succeed.
	sessRepo, err := gitx.Open(eng.State().SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.MergeFFOnly(ctx, eng.State().Agent(id).Branch); err != nil {
		t.Fatal(err)
	}
	if err := eng.Cleanup(ctx, id); err != nil {
		t.Fatalf("Cleanup() after merge error: %v", err)
	}
}

func TestTaskTimeout(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	cfg.TaskTimeout = config.Duration{Duration: 200 * time.Millisecond}

	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, runCtx context.Context, req provider.RunRequest) (provider.Run, error) {
			ch := make(chan provider.Event)
			done := make(chan struct{})
			go func() {
				<-runCtx.Done()
				close(ch)
				close(done)
			}()
			return &fakeRun{
				events: ch,
				done:   done,
				final:  provider.Final{Err: &provider.ProviderError{Class: provider.Timeout, Msg: "timeout"}},
			}, nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, cfg, prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "duerme")
	if err != nil {
		t.Fatal(err)
	}
	res := eng.Wait(ctx, []string{id}, 5*time.Second)
	if len(res) != 1 || res[0].Status != string(run.AgentTimeout) {
		t.Fatalf("Wait() = %+v, want timeout", res)
	}
}

func TestWaitCancel(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	defer close(release)

	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, runCtx context.Context, req provider.RunRequest) (provider.Run, error) {
			ch := make(chan provider.Event)
			done := make(chan struct{})
			go func() {
				select {
				case <-release:
				case <-runCtx.Done():
				}
				close(ch)
				close(done)
			}()
			return &fakeRun{
				events: ch,
				done:   done,
				final:  provider.Final{Err: &provider.ProviderError{Class: provider.Crashed, Msg: "cancelled"}},
			}, nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "espera")
	if err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	res := eng.Wait(waitCtx, []string{id}, 10*time.Second)
	elapsed := time.Since(start)
	if elapsed >= time.Second {
		t.Fatalf("Wait took %v on cancelled ctx, want < 1s", elapsed)
	}
	if len(res) != 1 || res[0].ID != id {
		t.Fatalf("Wait() = %+v", res)
	}
}

func TestSpawnDepAfterCleanup(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}
	app := &fakeApprover{approved: true}

	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			name := "step1.txt"
			taskID := "T1"
			if call > 1 {
				name = "step2.txt"
				taskID = "T2"
			}
			if err := os.WriteFile(filepath.Join(req.Dir, name), []byte("ok\n"), 0o644); err != nil {
				return nil, err
			}
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "add "+name); err != nil {
				return nil, err
			}
			sha, err := wt.RevParse(ctx, "HEAD")
			if err != nil {
				return nil, err
			}
			return newFakeRun("sid", []string{`{"ok":1}`}, provider.Final{
				Text:      receiptBlock(taskID, "done", name, []string{sha}, "echo ok"),
				SessionID: "sid",
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
		t.Fatal(err)
	}
	plan := &run.Plan{
		Tasks: []*run.Task{
			{ID: "T1", Title: "Base", Role: "implementer", SpecRefs: []string{"REQ-1"}, Checks: []string{"echo ok"}},
			{ID: "T2", Title: "Dependent", Role: "implementer", SpecRefs: []string{"REQ-1"}, DependsOn: []string{"T1"}, Checks: []string{"echo ok"}},
		},
		GlobalChecks: []string{"echo ok"},
	}
	if ok, _, err := eng.RequestApproval(ctx, ApprovalRequest{Kind: "plan", Path: "docs/plan.md", Summary: "p", Plan: plan}); err != nil || !ok {
		t.Fatal(err)
	}

	a1, err := eng.Spawn(ctx, "T1", "", "", "", "step1")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{a1}, 10*time.Second)
	if err := eng.AcceptTask(ctx, "T1", a1); err != nil {
		t.Fatalf("AcceptTask(T1): %v", err)
	}

	sessRepo, err := gitx.Open(eng.State().SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.MergeFFOnly(ctx, eng.State().Agent(a1).Branch); err != nil {
		t.Fatal(err)
	}
	if err := eng.Cleanup(ctx, a1); err != nil {
		t.Fatalf("Cleanup(a1): %v", err)
	}

	a2, err := eng.Spawn(ctx, "T2", "", "", "", "step2")
	if err != nil {
		t.Fatalf("Spawn(T2) after Cleanup(a1) failed: %v", err)
	}
	res := eng.Wait(ctx, []string{a2}, 10*time.Second)
	if len(res) != 1 || res[0].Status != string(run.AgentFinished) {
		t.Fatalf("T2 Wait() = %+v", res)
	}
}

func TestWaitAgentsIncludesDiffStatAndNoTranscript(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			if err := os.WriteFile(filepath.Join(req.Dir, "hola.txt"), []byte("hola mundo\n"), 0o644); err != nil {
				return nil, err
			}
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "add hola"); err != nil {
				return nil, err
			}
			sha, _ := wt.RevParse(ctx, "HEAD")
			return newFakeRun("sid-ds", []string{`{"secret":"TRANSCRIPT-ONLY"}`}, provider.Final{
				Text:      receiptBlock("T1", "done", "hola.txt", []string{sha}, "echo ok"),
				SessionID: "sid-ds",
			}), nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "crea hola.txt")
	if err != nil {
		t.Fatal(err)
	}
	res := eng.Wait(ctx, []string{id}, 10*time.Second)
	if len(res) != 1 || !strings.Contains(res[0].DiffStat, "hola.txt") {
		t.Fatalf("Wait() DiffStat = %q, want hola.txt", res[0].DiffStat)
	}
	if strings.Contains(res[0].DiffStat, "TRANSCRIPT-ONLY") {
		t.Fatalf("DiffStat leaked transcript: %q", res[0].DiffStat)
	}

	sessRepo, err := gitx.Open(eng.State().SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.MergeFFOnly(ctx, eng.State().Agent(id).Branch); err != nil {
		t.Fatal(err)
	}
	if err := eng.Cleanup(ctx, id); err != nil {
		t.Fatal(err)
	}

	afterCleanup := eng.Wait(ctx, []string{id}, 5*time.Second)
	if len(afterCleanup) != 1 || !strings.Contains(afterCleanup[0].DiffStat, "hola.txt") {
		t.Fatalf("Wait() after Cleanup DiffStat = %q, want persisted hola.txt", afterCleanup[0].DiffStat)
	}
}

func TestAgentDiffAfterCleanupUsesMergedCommit(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			if err := os.WriteFile(filepath.Join(req.Dir, "uno.txt"), []byte("uno\n"), 0o644); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(req.Dir, "dos.txt"), []byte("dos\n"), 0o644); err != nil {
				return nil, err
			}
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "add dos archivos"); err != nil {
				return nil, err
			}
			sha, _ := wt.RevParse(ctx, "HEAD")
			return newFakeRun("sid-diff", []string{`{"secret":"TRANSCRIPT-ONLY"}`}, provider.Final{
				Text:      receiptBlock("T1", "done", "uno.txt", []string{sha}, "echo ok"),
				SessionID: "sid-diff",
			}), nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "dos archivos")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{id}, 10*time.Second)

	sessRepo, err := gitx.Open(eng.State().SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.MergeFFOnly(ctx, eng.State().Agent(id).Branch); err != nil {
		t.Fatal(err)
	}
	if err := eng.Cleanup(ctx, id); err != nil {
		t.Fatal(err)
	}

	diff, truncated, err := eng.AgentDiff(ctx, id, "uno.txt", 0)
	if err != nil {
		t.Fatalf("AgentDiff() after Cleanup error: %v", err)
	}
	if truncated {
		t.Fatalf("truncated = true, want false")
	}
	if !strings.Contains(diff, "uno.txt") || strings.Contains(diff, "dos.txt") {
		t.Fatalf("filtered AgentDiff() = %q, want only uno.txt", diff)
	}
}

func TestAgentCannotMoveIntegrationBranch(t *testing.T) {
	ctx := context.Background()
	var integBranch string

	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(req.Dir, "rogue.txt"), []byte("r\n"), 0o644); err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "rogue commit"); err != nil {
				return nil, err
			}
			sha, _ := wt.RevParse(ctx, "HEAD")
			if _, err := wt.Run(ctx, "update-ref", "refs/heads/"+integBranch, sha); err != nil {
				return nil, err
			}
			return newFakeRun("sid-int-rogue", []string{`{"ok":1}`}, provider.Final{
				Text:      receiptBlock("T1", "done", "rogue.txt", []string{sha}, "echo ok"),
				SessionID: "sid-int-rogue",
			}), nil
		},
	}

	eng, r, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	integBranch = eng.State().IntegrationBranch
	origInteg, err := r.RevParse(ctx, "refs/heads/"+integBranch)
	if err != nil {
		t.Fatal(err)
	}

	id, err := eng.Spawn(ctx, "T1", "", "", "", "mueve rama de integración")
	if err != nil {
		t.Fatal(err)
	}
	res := eng.Wait(ctx, []string{id}, 10*time.Second)

	afterInteg, err := r.RevParse(ctx, "refs/heads/"+integBranch)
	if err != nil {
		t.Fatal(err)
	}
	if afterInteg != origInteg {
		t.Fatalf("integration branch was not restored: got %s, want %s", afterInteg, origInteg)
	}
	if res[0].Verification == nil || res[0].Verification.Passed {
		t.Fatalf("expected Verification.Passed == false, got %+v", res[0].Verification)
	}
	if len(res[0].Verification.RefViolations) == 0 {
		t.Fatalf("expected non-empty RefViolations")
	}
}

func TestParallelAgentsDoNotTriggerFalseRefViolations(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	pc := cfg.Providers["codex"]
	pc.MaxParallel = 2
	cfg.Providers["codex"] = pc

	t2Committed := make(chan struct{})
	releaseT2 := make(chan struct{})

	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			taskID := "T1"
			fileName := "f1.txt"
			if strings.Contains(req.Prompt, "Segunda") {
				taskID = "T2"
				fileName = "f2.txt"
			}
			if err := os.WriteFile(filepath.Join(req.Dir, fileName), []byte(taskID+"\n"), 0o644); err != nil {
				return nil, err
			}
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "commit "+taskID); err != nil {
				return nil, err
			}
			sha, err := wt.RevParse(ctx, "HEAD")
			if err != nil {
				return nil, err
			}

			if taskID == "T2" {
				close(t2Committed)
				ch := make(chan provider.Event)
				done := make(chan struct{})
				go func() {
					<-releaseT2
					close(ch)
					close(done)
				}()
				return &fakeRun{
					events:    ch,
					done:      done,
					sessionID: "sid-T2",
					final: provider.Final{
						Text:      receiptBlock("T2", "done", fileName, []string{sha}, "echo ok"),
						SessionID: "sid-T2",
					},
				}, nil
			}

			// Wait until T2 has already committed on its branch so T1 checks refs while T2 is still running.
			<-t2Committed
			return newFakeRun("sid-T1", []string{`{"ok":1}`}, provider.Final{
				Text:      receiptBlock("T1", "done", fileName, []string{sha}, "echo ok"),
				SessionID: "sid-T1",
			}), nil
		},
	}

	eng, _, _ := setupPlanApprovedEngine(t, cfg, prov, nil)
	id1, err := eng.Spawn(ctx, "T1", "", "", "", "haz T1")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := eng.Spawn(ctx, "T2", "", "", "", "haz T2")
	if err != nil {
		t.Fatal(err)
	}

	res1 := eng.Wait(ctx, []string{id1}, 10*time.Second)
	if len(res1) != 1 || res1[0].Verification == nil || !res1[0].Verification.Passed || len(res1[0].Verification.RefViolations) != 0 {
		close(releaseT2)
		t.Fatalf("T1 failed under parallel execution: %+v (ver=%+v)", res1[0], res1[0].Verification)
	}

	close(releaseT2)
	res2 := eng.Wait(ctx, []string{id2}, 10*time.Second)
	if len(res2) != 1 || res2[0].Verification == nil || !res2[0].Verification.Passed || len(res2[0].Verification.RefViolations) != 0 {
		t.Fatalf("T2 failed under parallel execution: %+v (ver=%+v)", res2[0], res2[0].Verification)
	}
}

func TestCleanupDoesNotTriggerDeletedViolation(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			if err := os.WriteFile(filepath.Join(req.Dir, "clean.txt"), []byte("ok\n"), 0o644); err != nil {
				return nil, err
			}
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "add clean.txt"); err != nil {
				return nil, err
			}
			sha, _ := wt.RevParse(ctx, "HEAD")
			return newFakeRun("sid-cl", []string{`{"ok":1}`}, provider.Final{
				Text:      receiptBlock("T1", "done", "clean.txt", []string{sha}, "echo ok"),
				SessionID: "sid-cl",
			}), nil
		},
	}

	eng, r, _ := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "limpia")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{id}, 10*time.Second)

	branch := eng.State().Agent(id).Branch
	sessRepo, err := gitx.Open(eng.State().SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.MergeFFOnly(ctx, branch); err != nil {
		t.Fatal(err)
	}
	if err := eng.Cleanup(ctx, id); err != nil {
		t.Fatal(err)
	}

	violations := eng.CheckRefs(ctx)
	if len(violations) != 0 {
		t.Fatalf("CheckRefs() after Cleanup = %+v, want empty", violations)
	}
	if _, err := r.RevParse(ctx, "refs/heads/"+branch); err == nil {
		t.Fatalf("Cleanup branch %s was resurrected", branch)
	}
}
