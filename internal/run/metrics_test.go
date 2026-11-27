package run

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSummarizeMetricsAndLegacyFallback(t *testing.T) {
	base := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

	st := &State{
		RunID: "20261127-120000",
		Plan: &Plan{
			Tasks: []*Task{
				{ID: "T1", Status: TaskAccepted},
				{ID: "T2", Status: TaskReceiptFailed},
				{ID: "T3", Status: TaskPending},
			},
		},
		Agents: []*Agent{
			{
				ID:               "a1",
				TaskID:           "T1",
				Provider:         "codex",
				Status:           AgentFinished,
				StartedAt:        base,
				EndedAt:          base.Add(30 * time.Second),
				ActiveDurationMS: 4500,
				FollowUps:        1,
				ReceiptRetries:   1,
			},
			{
				ID:               "a2",
				TaskID:           "T2",
				Provider:         "claude",
				Status:           AgentFailed,
				Err:              "quota_exhausted: limit reached",
				StartedAt:        base.Add(5 * time.Second),
				EndedAt:          base.Add(15 * time.Second),
				ActiveDurationMS: 2000,
			},
			{
				ID:               "a3",
				TaskID:           "T2",
				Provider:         "codex",
				Status:           AgentTimeout,
				Err:              "timeout: exceeded 5m",
				StartedAt:        base.Add(10 * time.Second),
				EndedAt:          base.Add(40 * time.Second),
				ActiveDurationMS: 3500,
			},
			{
				ID:               "a4",
				TaskID:           "T3",
				Provider:         "antigravity",
				Status:           AgentFailed,
				Err:              "empty_output: no text",
				StartedAt:        base.Add(12 * time.Second),
				EndedAt:          base.Add(22 * time.Second),
				ActiveDurationMS: 1000,
			},
		},
	}

	m := Summarize(st)
	if m.TotalAgents != 4 {
		t.Fatalf("TotalAgents = %d, want 4", m.TotalAgents)
	}
	if m.AcceptedTasks != 1 || m.TotalTasks != 3 {
		t.Fatalf("tasks = %d/%d, want 1/3", m.AcceptedTasks, m.TotalTasks)
	}
	if m.ReceiptRetries != 1 || m.FollowUps != 1 {
		t.Fatalf("retries/followups = %d/%d, want 1/1", m.ReceiptRetries, m.FollowUps)
	}
	if m.ActiveDuration != 11*time.Second {
		t.Fatalf("ActiveDuration = %v, want 11s", m.ActiveDuration)
	}
	if m.WallDuration != 40*time.Second {
		t.Fatalf("WallDuration = %v, want 40s", m.WallDuration)
	}
	if len(m.ByProvider) != 3 {
		t.Fatalf("len(ByProvider) = %d, want 3", len(m.ByProvider))
	}
	if m.ByProvider[0].Provider != "antigravity" || m.ByProvider[1].Provider != "claude" || m.ByProvider[2].Provider != "codex" {
		t.Fatalf("ByProvider not sorted alphabetically: %+v", m.ByProvider)
	}

	agy := m.ByProvider[0]
	if agy.Runs != 1 || agy.EmptyOutputs != 1 || agy.ActiveDuration != time.Second {
		t.Fatalf("antigravity stats = %+v", agy)
	}
	claude := m.ByProvider[1]
	if claude.Runs != 1 || claude.QuotaErrors != 1 || claude.ActiveDuration != 2*time.Second {
		t.Fatalf("claude stats = %+v", claude)
	}
	codex := m.ByProvider[2]
	if codex.Runs != 2 || codex.Succeeded != 1 || codex.Timeouts != 1 || codex.ActiveDuration != 8*time.Second {
		t.Fatalf("codex stats = %+v", codex)
	}

	// Verify backward compatibility with v1 state.json lacking metric fields.
	legacyJSON := `{
		"run_id": "20261126-100000",
		"base_branch": "main",
		"integration_branch": "weaver/20261126-100000",
		"session_dir": "/tmp/s",
		"phase": "integrated",
		"approvals": [],
		"agents": [
			{
				"id": "a1",
				"task_id": "T1",
				"role": "implementer",
				"provider": "codex",
				"model": "m1",
				"branch": "weaver/20261126-100000-a1",
				"base": "weaver/20261126-100000",
				"worktree": "/tmp/wt",
				"status": "finished",
				"started_at": "2026-11-26T10:00:00Z",
				"ended_at": "2026-11-26T10:01:15Z"
			}
		],
		"next_agent": 2
	}`
	var legacy State
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatalf("unmarshal v1 state.json failed: %v", err)
	}
	lm := Summarize(&legacy)
	if lm.ActiveDuration != 75*time.Second {
		t.Fatalf("legacy fallback ActiveDuration = %v, want 75s", lm.ActiveDuration)
	}
	if lm.WallDuration != 75*time.Second {
		t.Fatalf("legacy WallDuration = %v, want 75s", lm.WallDuration)
	}
	if len(lm.ByProvider) != 1 || lm.ByProvider[0].ActiveDuration != 75*time.Second {
		t.Fatalf("legacy ByProvider = %+v, want 75s", lm.ByProvider)
	}

	if zm := Summarize(nil); zm.TotalAgents != 0 || len(zm.ByProvider) != 0 {
		t.Fatalf("Summarize(nil) = %+v, want zero", zm)
	}
}
