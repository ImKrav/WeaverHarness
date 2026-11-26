package run

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "runs")}
	p := validPlan()
	s := &State{
		RunID:                 "20261126-010203",
		BaseBranch:            "main",
		IntegrationBranch:     "weaver/20261126-010203",
		SessionDir:            "/tmp/session",
		OrchestratorSessionID: "orch-1",
		Phase:                 PhasePlanApproved,
		Plan:                  &p,
		Approvals: []Approval{
			{Kind: "spec", Path: "docs/spec.md", Summary: "s", Approved: true, At: time.Unix(1000, 0).UTC()},
		},
		Agents: []*Agent{
			{ID: "a1", TaskID: "T1", Role: "implementer", Provider: "codex", Model: "m", Status: AgentFinished},
		},
		NextAgent: 1,
	}

	if err := st.Save(s); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := st.Load(s.RunID)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.RunID != s.RunID || loaded.OrchestratorSessionID != "orch-1" || loaded.Phase != PhasePlanApproved {
		t.Fatalf("loaded mismatch: %+v", loaded)
	}
	if loaded.Plan == nil || len(loaded.Plan.Tasks) != 2 || loaded.Task("T1").Title != "Primera" {
		t.Fatalf("loaded plan mismatch: %+v", loaded.Plan)
	}
	if got := st.AgentDir(s.RunID, "a1"); got != filepath.Join(st.Root, s.RunID, "agents", "a1") {
		t.Fatalf("AgentDir() = %q", got)
	}
}

func TestStoreLatestAndDelete(t *testing.T) {
	st := Store{Root: filepath.Join(t.TempDir(), "runs")}

	if err := st.Save(&State{RunID: "20261126-100000", Phase: PhaseOpen}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(&State{RunID: "20261126-120000", Phase: PhaseSpecApproved}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(&State{RunID: "20261126-110000", Phase: PhaseOpen}); err != nil {
		t.Fatal(err)
	}

	list, err := st.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(list) != 3 || list[0].RunID != "20261126-100000" || list[1].RunID != "20261126-110000" || list[2].RunID != "20261126-120000" {
		t.Fatalf("List() not sorted by RunID: %+v", list)
	}

	latest, err := st.Latest()
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}
	if latest.RunID != "20261126-120000" {
		t.Fatalf("Latest() = %q, want 20261126-120000", latest.RunID)
	}

	if err := st.Delete("20261126-120000"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	latest2, err := st.Latest()
	if err != nil {
		t.Fatalf("Latest() after delete error: %v", err)
	}
	if latest2.RunID != "20261126-110000" {
		t.Fatalf("Latest() after delete = %q, want 20261126-110000", latest2.RunID)
	}
}
