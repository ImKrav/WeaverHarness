package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/config"
	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/run"
)

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func newTestRepo(t *testing.T) gitx.Repo {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "engine repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "init", "-b", "main")
	runGitTest(t, dir, "config", "user.name", "Weaver Test")
	runGitTest(t, dir, "config", "user.email", "weaver@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", "README.md")
	runGitTest(t, dir, "commit", "-m", "initial")

	r, err := gitx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(gitx.WeaverDir(r.Dir))
	})
	return r
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.CheckTimeout = config.Duration{Duration: 5 * time.Second}
	cfg.TaskTimeout = config.Duration{Duration: 5 * time.Second}
	cfg.Roles["implementer"] = config.Role{Provider: "codex", Model: "m1"}
	return cfg
}

type fakeApprover struct {
	mu       sync.Mutex
	calls    int
	lastReq  ApprovalRequest
	approved bool
	comment  string
	err      error
	blockCh  chan struct{}
}

func (f *fakeApprover) Ask(ctx context.Context, r ApprovalRequest) (bool, string, error) {
	f.mu.Lock()
	f.calls++
	f.lastReq = r
	block := f.blockCh
	approved := f.approved
	comment := f.comment
	err := f.err
	f.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return false, "", ctx.Err()
		}
	}
	return approved, comment, err
}

func (f *fakeApprover) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeRun struct {
	events    chan provider.Event
	final     provider.Final
	done      chan struct{}
	sessionID string
	killFn    func()
}

func newFakeRun(sid string, rawLines []string, final provider.Final) *fakeRun {
	ch := make(chan provider.Event, len(rawLines)+1)
	for _, l := range rawLines {
		ch <- provider.Event{Kind: provider.EventRaw, Raw: []byte(l)}
	}
	close(ch)
	done := make(chan struct{})
	close(done)
	if final.SessionID == "" {
		final.SessionID = sid
	}
	return &fakeRun{
		events:    ch,
		final:     final,
		done:      done,
		sessionID: sid,
	}
}

func (r *fakeRun) Events() <-chan provider.Event { return r.events }
func (r *fakeRun) Send(string) error             { return nil }
func (r *fakeRun) SessionID() string             { return r.sessionID }
func (r *fakeRun) Wait() provider.Final {
	<-r.done
	return r.final
}
func (r *fakeRun) Kill() error {
	if r.killFn != nil {
		r.killFn()
	}
	return nil
}

type fakeProvider struct {
	name    string
	mu      sync.Mutex
	calls   int
	reqs    []provider.RunRequest
	handler func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error)
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) Check(context.Context) provider.Health {
	return provider.Health{Installed: true, LoggedIn: true}
}
func (p *fakeProvider) Start(ctx context.Context, req provider.RunRequest) (provider.Run, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.reqs = append(p.reqs, req)
	h := p.handler
	p.mu.Unlock()
	return h(n, ctx, req)
}

func receiptBlock(taskID, status, file string, commitSHAs []string, checkCmd string) string {
	payload := map[string]any{
		"task_id":      taskID,
		"status":       status,
		"summary":      "implementado",
		"spec_refs":    []string{"REQ-1"},
		"changes":      []map[string]string{{"path": file, "kind": "added"}},
		"commits":      commitSHAs,
		"verification": []map[string]any{{"cmd": checkCmd, "exit_code": 0, "output_tail": "ok"}},
		"open_issues":  []string{},
	}
	b, _ := json.Marshal(payload)
	return "Listo.\n```weaver-receipt\n" + string(b) + "\n```"
}

func TestStartRunCreatesSessionWorktree(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}

	eng, err := New(ctx, Options{
		Cfg:      testConfig(),
		Repo:     r,
		Store:    st,
		Approver: &fakeApprover{approved: true},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer eng.Close(ctx)

	s, err := eng.StartRun(ctx)
	if err != nil {
		t.Fatalf("StartRun() error: %v", err)
	}
	if s.SessionDir == "" || s.IntegrationBranch != "weaver/"+s.RunID || s.BaseBranch != "main" {
		t.Fatalf("unexpected state: %+v", s)
	}
	if _, err := os.Stat(s.SessionDir); err != nil {
		t.Fatalf("SessionDir does not exist: %v", err)
	}
	br, err := r.RevParse(ctx, "refs/heads/"+s.IntegrationBranch)
	if err != nil || br == "" {
		t.Fatalf("integration branch missing: %v", err)
	}
}

func TestApprovalOrder(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}
	app := &fakeApprover{approved: true}

	eng, err := New(ctx, Options{
		Cfg:      testConfig(),
		Repo:     r,
		Store:    st,
		Approver: app,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	if _, err := eng.StartRun(ctx); err != nil {
		t.Fatal(err)
	}

	ok, _, err := eng.RequestApproval(ctx, ApprovalRequest{
		Kind: "plan",
		Path: "docs/plan.md",
		Plan: &run.Plan{
			Tasks:        []*run.Task{{ID: "T1", Title: "t", Role: "implementer", Checks: []string{"echo ok"}}},
			GlobalChecks: []string{"echo ok"},
		},
	})
	if !errors.Is(err, run.ErrSpecNotApproved) || ok {
		t.Fatalf("RequestApproval(plan) = (%v, %v), want ErrSpecNotApproved", ok, err)
	}
	if app.Calls() != 0 {
		t.Fatalf("Approver was called %d times, want 0", app.Calls())
	}
}

func TestApprovalCancel(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}

	app := &fakeApprover{
		approved: true,
		blockCh:  make(chan struct{}),
	}

	eng, err := New(ctx, Options{
		Cfg:      testConfig(),
		Repo:     r,
		Store:    st,
		Approver: app,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	if _, err := eng.StartRun(ctx); err != nil {
		t.Fatal(err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	_, _, err = eng.RequestApproval(reqCtx, ApprovalRequest{
		Kind:    "spec",
		Path:    "docs/spec.md",
		Summary: "s",
	})
	if err == nil {
		t.Fatal("RequestApproval() succeeded after ctx cancel, want error")
	}
}

func TestResumeMarksInterrupted(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}

	s := &run.State{
		RunID:             "20261126-100000",
		BaseBranch:        "main",
		IntegrationBranch: "weaver/20261126-100000",
		Phase:             run.PhasePlanApproved,
		Plan: &run.Plan{
			Tasks:        []*run.Task{{ID: "T1", Role: "implementer", Status: run.TaskRunning, Checks: []string{"echo ok"}}},
			GlobalChecks: []string{"echo ok"},
		},
		Agents: []*run.Agent{
			{ID: "a1", TaskID: "T1", Status: run.AgentRunning},
		},
		NextAgent: 1,
	}
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}

	eng, err := New(ctx, Options{
		Cfg:      testConfig(),
		Repo:     r,
		Store:    st,
		Approver: &fakeApprover{approved: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	resumed, err := eng.Resume(ctx, "")
	if err != nil {
		t.Fatalf("Resume() error: %v", err)
	}
	if resumed.Agent("a1").Status != run.AgentInterrupted {
		t.Fatalf("agent status = %s, want interrupted", resumed.Agent("a1").Status)
	}
	if resumed.Task("T1").Status != run.TaskPending {
		t.Fatalf("task status = %s, want pending", resumed.Task("T1").Status)
	}
}

func TestCloseDeletesEmptyRun(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}

	eng, err := New(ctx, Options{
		Cfg:      testConfig(),
		Repo:     r,
		Store:    st,
		Approver: &fakeApprover{approved: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	s, err := eng.StartRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := s.SessionDir
	runID := s.RunID
	integBranch := s.IntegrationBranch

	if err := eng.Close(ctx); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if _, err := st.Load(runID); err == nil {
		t.Fatalf("empty run %s still exists in store after Close()", runID)
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("SessionDir %s still exists after Close()", sessionDir)
	}
	if _, err := r.RevParse(ctx, "refs/heads/"+integBranch); err == nil {
		t.Fatalf("integration branch %s still exists after Close()", integBranch)
	}
}

func TestRunSnapshotReportsPhaseAndMergedTasks(t *testing.T) {
	ctx := context.Background()
	r := newTestRepo(t)
	st := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}

	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			if err := os.WriteFile(filepath.Join(req.Dir, "t1.txt"), []byte("t1\n"), 0o644); err != nil {
				return nil, err
			}
			wt, err := gitx.Open(req.Dir)
			if err != nil {
				return nil, err
			}
			if _, err := wt.CommitAll(ctx, "add t1.txt"); err != nil {
				return nil, err
			}
			sha, err := wt.RevParse(ctx, "HEAD")
			if err != nil {
				return nil, err
			}
			return newFakeRun("sid-snap", []string{`{"ok":1}`}, provider.Final{
				Text:      receiptBlock("T1", "done", "t1.txt", []string{sha}, "echo ok"),
				SessionID: "sid-snap",
			}), nil
		},
	}

	eng, err := New(ctx, Options{
		Cfg:       testConfig(),
		Repo:      r,
		Store:     st,
		Providers: map[string]provider.Provider{"codex": prov},
		Approver:  &fakeApprover{approved: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(ctx)

	if _, err := eng.StartRun(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.RequestApproval(ctx, ApprovalRequest{Kind: "spec", Path: "docs/specs/demo.md", Summary: "spec"}); err != nil {
		t.Fatal(err)
	}
	plan := &run.Plan{
		Tasks: []*run.Task{
			{ID: "T1", Title: "Primera", Role: "implementer", SpecRefs: []string{"REQ-1"}, Checks: []string{"echo ok"}},
			{ID: "T2", Title: "Segunda", Role: "implementer", DependsOn: []string{"T1"}, SpecRefs: []string{"REQ-1"}, Checks: []string{"echo ok"}},
		},
		GlobalChecks: []string{"echo ok"},
	}
	if _, _, err := eng.RequestApproval(ctx, ApprovalRequest{Kind: "plan", Path: "docs/plans/demo.md", Summary: "plan", Plan: plan}); err != nil {
		t.Fatal(err)
	}

	id, err := eng.Spawn(ctx, "T1", "", "", "", "haz T1")
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Wait(ctx, []string{id}, 10*time.Second)
	if err := eng.AcceptTask(ctx, "T1", id); err != nil {
		t.Fatal(err)
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

	snap, err := eng.RunSnapshot(ctx)
	if err != nil {
		t.Fatalf("RunSnapshot() error: %v", err)
	}
	if snap.Phase != string(run.PhasePlanApproved) {
		t.Fatalf("snap.Phase = %q, want %q", snap.Phase, run.PhasePlanApproved)
	}
	if snap.SpecPath != "docs/specs/demo.md" {
		t.Fatalf("snap.SpecPath = %q, want docs/specs/demo.md", snap.SpecPath)
	}
	if len(snap.Tasks) != 2 {
		t.Fatalf("len(snap.Tasks) = %d, want 2", len(snap.Tasks))
	}
	if snap.Tasks[0].ID != "T1" || snap.Tasks[0].Status != string(run.TaskAccepted) || !snap.Tasks[0].Merged || snap.Tasks[0].AcceptedAgent != id {
		t.Fatalf("unexpected T1 snapshot: %+v", snap.Tasks[0])
	}
	if snap.Tasks[1].ID != "T2" || snap.Tasks[1].Status != string(run.TaskPending) || snap.Tasks[1].Merged {
		t.Fatalf("unexpected T2 snapshot: %+v", snap.Tasks[1])
	}
}
