package engine

import (
	"context"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/run"
)

func TestEngineRecordsAgentMetrics(t *testing.T) {
	ctx := context.Background()
	prov := &fakeProvider{
		name: "codex",
		handler: func(call int, ctx context.Context, req provider.RunRequest) (provider.Run, error) {
			switch call {
			case 1:
				// First call omits receipt -> triggers automatic receipt retry.
				return newFakeRun("sid-met", []string{`{"step":1}`}, provider.Final{
					Text:      "olvidé incluir el recibo",
					SessionID: "sid-met",
				}), nil
			case 2:
				// Receipt retry returns a failing check ("exit 1") alongside task check ("echo ok").
				return newFakeRun("sid-met", []string{`{"step":2}`}, provider.Final{
					Text:      receiptBlock("T1", "done", "README.md", nil, "exit 1"),
					SessionID: "sid-met",
				}), nil
			default:
				// FollowUp fixes the verification check ("echo ok").
				return newFakeRun("sid-met", []string{`{"step":3}`}, provider.Final{
					Text:      receiptBlock("T1", "done", "README.md", nil, "echo ok"),
					SessionID: "sid-met",
				}), nil
			}
		},
	}

	eng, _, st := setupPlanApprovedEngine(t, testConfig(), prov, nil)
	id, err := eng.Spawn(ctx, "T1", "", "", "", "mide métricas")
	if err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	res := eng.Wait(ctx, []string{id}, 10*time.Second)
	if len(res) != 1 || res[0].Status != string(run.AgentFinished) {
		t.Fatalf("Wait() = %+v, want finished", res)
	}

	if err := eng.FollowUp(ctx, id, "corrige el check"); err != nil {
		t.Fatalf("FollowUp() error: %v", err)
	}
	res = eng.Wait(ctx, []string{id}, 10*time.Second)
	if len(res) != 1 || res[0].Status != string(run.AgentFinished) {
		t.Fatalf("Wait() after FollowUp = %+v, want finished", res)
	}

	persisted, err := st.Load(eng.State().RunID)
	if err != nil {
		t.Fatalf("st.Load() error: %v", err)
	}
	ag := persisted.Agent(id)
	if ag == nil {
		t.Fatalf("agent %s not found in persisted state.json", id)
	}
	if ag.ReceiptRetries < 1 {
		t.Fatalf("ReceiptRetries = %d, want >= 1", ag.ReceiptRetries)
	}
	if ag.FollowUps != 1 {
		t.Fatalf("FollowUps = %d, want 1", ag.FollowUps)
	}
	if ag.ActiveDurationMS <= 0 {
		t.Fatalf("ActiveDurationMS = %d, want > 0", ag.ActiveDurationMS)
	}
	if ag.ChecksPassed < 1 {
		t.Fatalf("ChecksPassed = %d, want >= 1", ag.ChecksPassed)
	}
	if ag.ChecksFailed < 1 {
		t.Fatalf("ChecksFailed = %d, want >= 1", ag.ChecksFailed)
	}
}
