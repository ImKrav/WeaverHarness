package engine

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ImKrav/weaverharness/internal/run"
)

func TestEngineCloseIdempotent(t *testing.T) {
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
	if ok, _, err := eng.RequestApproval(ctx, ApprovalRequest{
		Kind:    "spec",
		Path:    "docs/spec.md",
		Summary: "spec",
	}); err != nil || !ok {
		t.Fatalf("RequestApproval(spec) = (%v, %v), want (true, nil)", ok, err)
	}

	if err := eng.Close(ctx); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := eng.Close(ctx); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}

	persisted, err := st.Load(s.RunID)
	if err != nil {
		t.Fatalf("run with approvals should be preserved across double Close(): %v", err)
	}
	if len(persisted.Approvals) != 1 {
		t.Fatalf("len(Approvals) = %d, want 1", len(persisted.Approvals))
	}
}

func TestSemaphoreForSharesChannelForUnknownProvider(t *testing.T) {
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
	t.Cleanup(func() {
		_ = eng.Close(context.Background())
	})

	const workers = 8
	chs := make([]chan struct{}, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			chs[idx] = eng.semaphoreFor("custom")
		}(i)
	}
	wg.Wait()

	if chs[0] == nil {
		t.Fatal("semaphoreFor(\"custom\") returned nil")
	}
	for i := 1; i < workers; i++ {
		if chs[i] != chs[0] {
			t.Fatalf("concurrent semaphoreFor(\"custom\") returned different channels: ch[%d]=%p != ch[0]=%p", i, chs[i], chs[0])
		}
	}
}
