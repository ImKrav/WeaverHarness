package run

import (
	"fmt"
	"time"
)

// Phase represents the lifecycle stage of a WeaverHarness run.
type Phase string

const (
	PhaseOpen         Phase = "open"
	PhaseSpecApproved Phase = "spec_approved"
	PhasePlanApproved Phase = "plan_approved"
	PhaseIntegrated   Phase = "integrated"
)

// TaskStatus represents the verification and acceptance state of a plan task.
type TaskStatus string

const (
	TaskPending       TaskStatus = "pending"
	TaskRunning       TaskStatus = "running"
	TaskReceiptOK     TaskStatus = "receipt_ok"
	TaskReceiptFailed TaskStatus = "receipt_failed"
	TaskAccepted      TaskStatus = "accepted"
)

// AgentStatus represents the execution state of a spawned subagent.
type AgentStatus string

const (
	AgentQueued      AgentStatus = "queued"
	AgentRunning     AgentStatus = "running"
	AgentFinished    AgentStatus = "finished"
	AgentFailed      AgentStatus = "failed"
	AgentCancelled   AgentStatus = "cancelled"
	AgentTimeout     AgentStatus = "timeout"
	AgentInterrupted AgentStatus = "interrupted"
)

// IntegrateTask is the reserved task ID allowed after plan approval for merge resolution.
const IntegrateTask = "integrate"

// Task describes one planned unit of work and its acceptance checks.
type Task struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Role          string     `json:"role"`
	SpecRefs      []string   `json:"spec_refs"`
	DependsOn     []string   `json:"depends_on"`
	Checks        []string   `json:"checks"`
	Status        TaskStatus `json:"status"`
	Rounds        int        `json:"rounds"`
	AcceptedAgent string     `json:"accepted_agent,omitempty"`
}

// Plan holds the ordered task DAG and integration-level global checks.
type Plan struct {
	Tasks        []*Task  `json:"tasks"`
	GlobalChecks []string `json:"global_checks"`
}

// Agent holds runtime metadata for one subagent invocation.
type Agent struct {
	ID        string      `json:"id"`
	TaskID    string      `json:"task_id"`
	Role      string      `json:"role"`
	Provider  string      `json:"provider"`
	Model     string      `json:"model"`
	Branch    string      `json:"branch"`
	Base      string      `json:"base"`
	Worktree  string      `json:"worktree"`
	SessionID string      `json:"session_id,omitempty"`
	Err       string      `json:"err,omitempty"`
	Status    AgentStatus `json:"status"`
	StartedAt time.Time   `json:"started_at"`
	EndedAt   time.Time   `json:"ended_at,omitempty"`
}

// Approval records one human approval or rejection decision.
type Approval struct {
	Kind     string    `json:"kind"`
	Path     string    `json:"path"`
	Summary  string    `json:"summary"`
	Comment  string    `json:"comment,omitempty"`
	Approved bool      `json:"approved"`
	At       time.Time `json:"at"`
}

// State is the persisted state of one WeaverHarness run.
type State struct {
	RunID                 string     `json:"run_id"`
	BaseBranch            string     `json:"base_branch"`
	IntegrationBranch     string     `json:"integration_branch"`
	SessionDir            string     `json:"session_dir"`
	OrchestratorSessionID string     `json:"orchestrator_session_id,omitempty"`
	Phase                 Phase      `json:"phase"`
	Plan                  *Plan      `json:"plan,omitempty"`
	Approvals             []Approval `json:"approvals"`
	Agents                []*Agent   `json:"agents"`
	NextAgent             int        `json:"next_agent"`
}

// NewRunID formats timestamp t as a sortable run identifier.
func NewRunID(t time.Time) string {
	return t.Format("20060102-150405")
}

// Task returns the planned task matching id, or nil if not found.
func (s *State) Task(id string) *Task {
	if s.Plan == nil {
		return nil
	}
	for _, t := range s.Plan.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Agent returns the agent matching id, or nil if not found.
func (s *State) Agent(id string) *Agent {
	for _, a := range s.Agents {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// NewAgentID allocates the next sequential agent ID ("a1", "a2", ...).
func (s *State) NewAgentID() string {
	s.NextAgent++
	return fmt.Sprintf("a%d", s.NextAgent)
}

// MarkInterrupted transitions any queued/running agents to interrupted and resets their running tasks to pending.
func (s *State) MarkInterrupted() {
	for _, a := range s.Agents {
		if a.Status == AgentQueued || a.Status == AgentRunning {
			a.Status = AgentInterrupted
			if t := s.Task(a.TaskID); t != nil && t.Status == TaskRunning {
				t.Status = TaskPending
			}
		}
	}
}
