package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/receipt"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/ImKrav/weaverharness/internal/verify"
)

func seedInspectTestRun(t *testing.T) (repoDir, runID string) {
	t.Helper()
	r := newAppTestRepo(t)
	runID = "20261127-130000"
	store := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}

	now := time.Now().UTC()
	st := &run.State{
		RunID:             runID,
		BaseBranch:        "main",
		IntegrationBranch: "weaver/" + runID,
		SessionDir:        r.Dir,
		Phase:             run.PhasePlanApproved,
		Approvals: []run.Approval{
			{Kind: "spec", Path: "docs/spec.md", Summary: "spec aprobada", Approved: true, At: now.Add(-2 * time.Minute)},
		},
		Plan: &run.Plan{
			Tasks: []*run.Task{
				{ID: "T1", Title: "Base", Role: "implementer", Status: run.TaskAccepted, Rounds: 1, AcceptedAgent: "a1"},
				{ID: "T2", Title: "UI", Role: "reviewer", Status: run.TaskPending, Rounds: 0},
			},
		},
		Agents: []*run.Agent{
			{
				ID:        "a1",
				TaskID:    "T1",
				Role:      "implementer",
				Provider:  "codex",
				Model:     "gpt-5.6-sol",
				Branch:    "weaver/" + runID + "/a1",
				Base:      "weaver/" + runID,
				Worktree:  filepath.Join(r.Dir, "wt-a1"),
				Status:    run.AgentFinished,
				StartedAt: now.Add(-90 * time.Second),
				EndedAt:   now.Add(-30 * time.Second),
			},
		},
		NextAgent: 2,
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	agDir := store.AgentDir(runID, "a1")
	if err := os.MkdirAll(agDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rcBytes, err := json.MarshalIndent(receipt.Receipt{
		TaskID:  "T1",
		Status:  "done",
		Summary: "todo implementado",
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agDir, "receipt.json"), rcBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	vrBytes, err := json.MarshalIndent(verify.Verification{
		Passed: true,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agDir, "verification.json"), vrBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	transcript := "line-1\nline-2\nline-3\nline-4\nline-5\n"
	if err := os.WriteFile(filepath.Join(agDir, "transcript.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	return r.Dir, runID
}

func TestInspectLatestRunTextAndJSON(t *testing.T) {
	repoDir, runID := seedInspectTestRun(t)

	var textOut bytes.Buffer
	if err := Inspect(InspectOptions{
		Dir: repoDir,
		Out: &textOut,
	}); err != nil {
		t.Fatalf("Inspect(text) error: %v", err)
	}
	txt := textOut.String()
	for _, want := range []string{runID, "plan_approved", "T1", "T2", "a1", "codex/gpt-5.6-sol", "done", "ok"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("Inspect(text) missing %q:\n%s", want, txt)
		}
	}

	var jsonOut bytes.Buffer
	if err := Inspect(InspectOptions{
		Dir:  repoDir,
		JSON: true,
		Out:  &jsonOut,
	}); err != nil {
		t.Fatalf("Inspect(json) error: %v", err)
	}
	var decoded struct {
		State         *run.State                     `json:"state"`
		Receipts      map[string]receipt.Receipt     `json:"receipts"`
		Verifications map[string]verify.Verification `json:"verifications"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatalf("Inspect(json) unmarshal failed: %v\n%s", err, jsonOut.String())
	}
	if decoded.State == nil || decoded.State.RunID != runID {
		t.Fatalf("decoded.State = %+v, want RunID=%s", decoded.State, runID)
	}
	if got := decoded.Receipts["a1"].Summary; got != "todo implementado" {
		t.Fatalf("decoded.Receipts[a1].Summary = %q", got)
	}
	if !decoded.Verifications["a1"].Passed {
		t.Fatalf("decoded.Verifications[a1].Passed = false, want true")
	}
}

func TestLogsSingleArgUsesLatestRun(t *testing.T) {
	repoDir, _ := seedInspectTestRun(t)

	var out bytes.Buffer
	if err := Logs(LogsOptions{
		Dir:     repoDir,
		AgentID: "a1",
		Tail:    2,
		Out:     &out,
	}); err != nil {
		t.Fatalf("Logs() error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"todo implementado", `"passed": true`, "line-4", "line-5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Logs() missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"line-1", "line-2", "line-3"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("Logs(Tail:2) unexpectedly included %q:\n%s", unwanted, got)
		}
	}
}

func TestLogsOnlyReceiptSkipsTranscript(t *testing.T) {
	repoDir, _ := seedInspectTestRun(t)

	var out bytes.Buffer
	if err := Logs(LogsOptions{
		Dir:         repoDir,
		AgentID:     "a1",
		OnlyReceipt: true,
		Out:         &out,
	}); err != nil {
		t.Fatalf("Logs(OnlyReceipt) error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "todo implementado") {
		t.Fatalf("Logs(OnlyReceipt) missing receipt:\n%s", got)
	}
	if strings.Contains(got, "line-5") || strings.Contains(got, "line-4") {
		t.Fatalf("Logs(OnlyReceipt) included transcript lines:\n%s", got)
	}
}

func TestLogsMissingAgentReturnsError(t *testing.T) {
	repoDir, _ := seedInspectTestRun(t)

	var out bytes.Buffer
	err := Logs(LogsOptions{
		Dir:     repoDir,
		AgentID: "a99",
		Out:     &out,
	})
	if err == nil {
		t.Fatal("Logs(a99) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "a99") {
		t.Fatalf("Logs(a99) error = %v, want agent id mentioned", err)
	}
}
