package run

import (
	"errors"
	"testing"
	"time"

	"github.com/ImKrav/weaverharness/internal/config"
)

func testRoles() map[string]config.Role {
	return map[string]config.Role{
		"architect":   {Provider: "claude", Model: "opus"},
		"implementer": {Provider: "codex", Model: "gpt-5.6-sol"},
		"reviewer":    {Provider: "claude", Model: "sonnet"},
	}
}

func validPlan() Plan {
	return Plan{
		Tasks: []*Task{
			{ID: "T1", Title: "Primera", Role: "implementer", Checks: []string{"go test ./..."}},
			{ID: "T2", Title: "Segunda", Role: "implementer", DependsOn: []string{"T1"}, Checks: []string{"go test ./..."}},
		},
		GlobalChecks: []string{"go build ./..."},
	}
}

func TestApprovalOrder(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*State)
		kind    string
		wantErr error
	}{
		{
			name:    "spec allowed on open",
			setup:   func(s *State) {},
			kind:    "spec",
			wantErr: nil,
		},
		{
			name:    "plan rejected before spec",
			setup:   func(s *State) {},
			kind:    "plan",
			wantErr: ErrSpecNotApproved,
		},
		{
			name: "plan allowed after spec approved",
			setup: func(s *State) {
				s.Record(Approval{Kind: "spec", Approved: true}, nil)
			},
			kind:    "plan",
			wantErr: nil,
		},
		{
			name: "plan rejected when spec was rejected",
			setup: func(s *State) {
				s.Record(Approval{Kind: "spec", Approved: false}, nil)
			},
			kind:    "plan",
			wantErr: ErrSpecNotApproved,
		},
		{
			name: "integration rejected before plan",
			setup: func(s *State) {
				s.Record(Approval{Kind: "spec", Approved: true}, nil)
			},
			kind:    "integration",
			wantErr: ErrPlanNotApproved,
		},
		{
			name: "integration rejected when task is receipt_ok not accepted",
			setup: func(s *State) {
				p := validPlan()
				s.Record(Approval{Kind: "spec", Approved: true}, nil)
				s.Record(Approval{Kind: "plan", Approved: true}, &p)
				s.Task("T1").Status = TaskAccepted
				s.Task("T2").Status = TaskReceiptOK
			},
			kind:    "integration",
			wantErr: ErrTasksNotAccepted,
		},
		{
			name: "integration allowed when all tasks accepted",
			setup: func(s *State) {
				p := validPlan()
				s.Record(Approval{Kind: "spec", Approved: true}, nil)
				s.Record(Approval{Kind: "plan", Approved: true}, &p)
				s.Task("T1").Status = TaskAccepted
				s.Task("T2").Status = TaskAccepted
			},
			kind:    "integration",
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &State{RunID: "r1", Phase: PhaseOpen}
			tt.setup(s)
			err := s.CheckRequest(tt.kind)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckRequest(%q) error = %v, want %v", tt.kind, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePlan(t *testing.T) {
	roles := testRoles()

	tests := []struct {
		name    string
		plan    Plan
		wantErr bool
	}{
		{
			name:    "valid plan",
			plan:    validPlan(),
			wantErr: false,
		},
		{
			name: "cycle in depends_on",
			plan: Plan{
				Tasks: []*Task{
					{ID: "T1", Role: "implementer", DependsOn: []string{"T2"}, Checks: []string{"echo 1"}},
					{ID: "T2", Role: "implementer", DependsOn: []string{"T1"}, Checks: []string{"echo 2"}},
				},
				GlobalChecks: []string{"go test ./..."},
			},
			wantErr: true,
		},
		{
			name: "missing dependency",
			plan: Plan{
				Tasks: []*Task{
					{ID: "T1", Role: "implementer", DependsOn: []string{"T99"}, Checks: []string{"echo 1"}},
				},
				GlobalChecks: []string{"go test ./..."},
			},
			wantErr: true,
		},
		{
			name: "unknown role",
			plan: Plan{
				Tasks: []*Task{
					{ID: "T1", Role: "wizard", Checks: []string{"echo 1"}},
				},
				GlobalChecks: []string{"go test ./..."},
			},
			wantErr: true,
		},
		{
			name: "task without checks",
			plan: Plan{
				Tasks: []*Task{
					{ID: "T1", Role: "implementer", Checks: nil},
				},
				GlobalChecks: []string{"go test ./..."},
			},
			wantErr: true,
		},
		{
			name: "plan without global checks",
			plan: Plan{
				Tasks: []*Task{
					{ID: "T1", Role: "implementer", Checks: []string{"echo 1"}},
				},
				GlobalChecks: nil,
			},
			wantErr: true,
		},
		{
			name: "reserved task id integrate",
			plan: Plan{
				Tasks: []*Task{
					{ID: IntegrateTask, Role: "implementer", Checks: []string{"echo 1"}},
				},
				GlobalChecks: []string{"go test ./..."},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePlan(tt.plan, roles)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePlan() error = %v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckSpawn(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*State)
		taskID  string
		merged  func(string) bool
		wantErr error
	}{
		{
			name:    "before plan approved",
			setup:   func(s *State) { s.Phase = PhaseSpecApproved },
			taskID:  "T1",
			wantErr: ErrPlanNotApproved,
		},
		{
			name: "unknown task",
			setup: func(s *State) {
				p := validPlan()
				s.Record(Approval{Kind: "spec", Approved: true}, nil)
				s.Record(Approval{Kind: "plan", Approved: true}, &p)
			},
			taskID:  "T404",
			wantErr: ErrUnknownTask,
		},
		{
			name: "dependency accepted but not merged",
			setup: func(s *State) {
				p := validPlan()
				s.Record(Approval{Kind: "spec", Approved: true}, nil)
				s.Record(Approval{Kind: "plan", Approved: true}, &p)
				s.Task("T1").Status = TaskAccepted
				s.Task("T1").AcceptedAgent = "a1"
			},
			taskID:  "T2",
			merged:  func(agentID string) bool { return false },
			wantErr: ErrDepsNotMerged,
		},
		{
			name: "dependency accepted and merged",
			setup: func(s *State) {
				p := validPlan()
				s.Record(Approval{Kind: "spec", Approved: true}, nil)
				s.Record(Approval{Kind: "plan", Approved: true}, &p)
				s.Task("T1").Status = TaskAccepted
				s.Task("T1").AcceptedAgent = "a1"
			},
			taskID:  "T2",
			merged:  func(agentID string) bool { return agentID == "a1" },
			wantErr: nil,
		},
		{
			name: "integrate task allowed after plan",
			setup: func(s *State) {
				p := validPlan()
				s.Record(Approval{Kind: "spec", Approved: true}, nil)
				s.Record(Approval{Kind: "plan", Approved: true}, &p)
			},
			taskID:  IntegrateTask,
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &State{RunID: "r1", Phase: PhaseOpen}
			tt.setup(s)
			err := s.CheckSpawn(tt.taskID, tt.merged)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckSpawn(%q) error = %v, want %v", tt.taskID, err, tt.wantErr)
			}
		})
	}
}

func TestMarkInterrupted(t *testing.T) {
	p := validPlan()
	s := &State{RunID: "r1", Phase: PhaseOpen}
	s.Record(Approval{Kind: "spec", Approved: true}, nil)
	s.Record(Approval{Kind: "plan", Approved: true}, &p)
	s.Task("T1").Status = TaskRunning
	s.Task("T2").Status = TaskRunning
	s.Agents = []*Agent{
		{ID: "a1", TaskID: "T1", Status: AgentRunning},
		{ID: "a2", TaskID: "T2", Status: AgentQueued},
		{ID: "a3", TaskID: "T1", Status: AgentFinished},
	}

	s.MarkInterrupted()

	if s.Agent("a1").Status != AgentInterrupted || s.Agent("a2").Status != AgentInterrupted {
		t.Fatalf("running/queued agents should become interrupted: %+v", s.Agents)
	}
	if s.Agent("a3").Status != AgentFinished {
		t.Fatalf("finished agent should stay finished, got %s", s.Agent("a3").Status)
	}
	if s.Task("T1").Status != TaskPending || s.Task("T2").Status != TaskPending {
		t.Fatalf("tasks of interrupted agents should revert to pending: T1=%s T2=%s", s.Task("T1").Status, s.Task("T2").Status)
	}
}

func TestNewRunID(t *testing.T) {
	ts := time.Date(2026, 11, 26, 15, 4, 5, 0, time.UTC)
	if got := NewRunID(ts); got != "20261126-150405" {
		t.Fatalf("NewRunID() = %q, want %q", got, "20261126-150405")
	}
}
