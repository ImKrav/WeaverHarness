package app

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/run"
)

func TestRunsShowsDurationColumn(t *testing.T) {
	r := newAppTestRepo(t)
	store := run.Store{Root: filepath.Join(r.Dir, ".weaver", "runs")}

	base := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	if err := store.Save(&run.State{
		RunID:             "20261127-100000",
		BaseBranch:        "main",
		IntegrationBranch: "weaver/20261127-100000",
		SessionDir:        r.Dir,
		Phase:             run.PhaseOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&run.State{
		RunID:             "20261127-110000",
		BaseBranch:        "main",
		IntegrationBranch: "weaver/20261127-110000",
		SessionDir:        r.Dir,
		Phase:             run.PhasePlanApproved,
		Plan:              &run.Plan{Tasks: []*run.Task{{ID: "T1", Status: run.TaskAccepted}}},
		Agents: []*run.Agent{
			{
				ID:               "a1",
				TaskID:           "T1",
				Provider:         "codex",
				Status:           run.AgentFinished,
				StartedAt:        base,
				EndedAt:          base.Add(45 * time.Second),
				ActiveDurationMS: 12000,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Runs(r.Dir, &out); err != nil {
		t.Fatalf("Runs() error: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "DURACIÓN") {
		t.Fatalf("Runs output missing DURACIÓN header:\n%s", text)
	}
	if !strings.Contains(text, "12s") {
		t.Fatalf("Runs output missing active duration 12s:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "20261127-100000") && !strings.Contains(line, "-") {
			t.Fatalf("expected '-' duration for run without agents, got line: %q", line)
		}
	}
}
