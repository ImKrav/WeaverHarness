package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ImKrav/weaverharness/internal/config"
	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/receipt"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/ImKrav/weaverharness/internal/verify"
)

// integrationHooks allows sibling file integrate.go (Task 13) to register
// beforeIntegration and afterIntegrationHook without coupling Task 12's build.
var integrationHooks func(*Engine)

// ApprovalRequest describes a human approval gate requested by the orchestrator.
type ApprovalRequest struct {
	Kind    string
	Path    string
	Summary string
	Plan    *run.Plan
}

// Approver prompts a human (via TUI or test double) for approval of a spec, plan, or integration.
type Approver interface {
	Ask(ctx context.Context, r ApprovalRequest) (approved bool, comment string, err error)
}

// AgentInfo is the status summary pushed to the UI and agent_status queries.
type AgentInfo struct {
	ID        string
	TaskID    string
	Role      string
	Provider  string
	Model     string
	Status    string
	Err       string
	Started   time.Time
	ReceiptOK *bool
	Verified  *bool
}

// AgentResult is returned by Wait for each requested agent ID.
type AgentResult struct {
	ID           string                `json:"id"`
	Status       string                `json:"status"`
	Err          string                `json:"err,omitempty"`
	Receipt      *receipt.Receipt      `json:"receipt,omitempty"`
	Verification *verify.Verification  `json:"verification,omitempty"`
}

// Options configures an Engine instance.
type Options struct {
	Cfg       config.Config
	Repo      gitx.Repo
	Store     run.Store
	Providers map[string]provider.Provider
	Approver  Approver
	Notify    func(any)
}

type agentSummary struct {
	ReceiptOK *bool
	Verified  *bool
}

// Engine coordinates WeaverHarness runs, phase gates, subagent lifecycles, and ref protection.
type Engine struct {
	cfg       config.Config
	repo      gitx.Repo
	store     run.Store
	providers map[string]provider.Provider
	approver  Approver
	notifyFn  func(any)
	guard     *gitx.Guard

	baseCtx    context.Context
	baseCancel context.CancelFunc

	mu          sync.Mutex
	state       *run.State
	sems        map[string]chan struct{}
	doneChs     map[string]chan struct{}
	cancels     map[string]context.CancelFunc
	cancelled   map[string]bool
	runs        map[string]provider.Run
	agentChecks map[string]agentSummary

	beforeIntegration    func(context.Context) error
	afterIntegrationHook func(context.Context) (string, error)
}

// New initializes an Engine and captures the baseline repository ref guard.
func New(ctx context.Context, o Options) (*Engine, error) {
	guard, err := gitx.NewGuard(ctx, o.Repo)
	if err != nil {
		return nil, fmt.Errorf("engine: inicializando guardia de refs: %w", err)
	}

	sems := make(map[string]chan struct{}, len(config.ProviderNames))
	for _, name := range config.ProviderNames {
		limit := o.Cfg.Providers[name].MaxParallel
		if limit <= 0 {
			limit = 3
		}
		sems[name] = make(chan struct{}, limit)
	}
	for name, pc := range o.Cfg.Providers {
		if _, exists := sems[name]; !exists {
			limit := pc.MaxParallel
			if limit <= 0 {
				limit = 3
			}
			sems[name] = make(chan struct{}, limit)
		}
	}

	baseCtx, baseCancel := context.WithCancel(context.Background())
	e := &Engine{
		cfg:         o.Cfg,
		repo:        o.Repo,
		store:       o.Store,
		providers:   o.Providers,
		approver:    o.Approver,
		notifyFn:    o.Notify,
		guard:       guard,
		baseCtx:     baseCtx,
		baseCancel:  baseCancel,
		sems:        sems,
		doneChs:     map[string]chan struct{}{},
		cancels:     map[string]context.CancelFunc{},
		cancelled:   map[string]bool{},
		runs:        map[string]provider.Run{},
		agentChecks: map[string]agentSummary{},
	}
	if integrationHooks != nil {
		integrationHooks(e)
	}
	return e, nil
}

// StartRun creates a new run and prepares or reuses the session integration worktree.
func (e *Engine) StartRun(ctx context.Context) (*run.State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	runID := e.allocateRunIDLocked(ctx)
	integBranch := "weaver/" + runID

	var baseBranch, sessionDir string
	if e.state != nil && e.state.SessionDir != "" {
		baseBranch = e.state.BaseBranch
		sessionDir = e.state.SessionDir
		if _, err := os.Stat(sessionDir); err == nil {
			sessRepo, err := gitx.Open(sessionDir)
			if err != nil {
				return nil, fmt.Errorf("engine: abriendo worktree de sesión: %w", err)
			}
			if err := sessRepo.SwitchNew(ctx, integBranch, baseBranch); err != nil {
				return nil, fmt.Errorf("engine: creando rama %s en sesión: %w", integBranch, err)
			}
		} else {
			if err := e.repo.AddWorktree(ctx, sessionDir, integBranch, baseBranch); err != nil {
				return nil, fmt.Errorf("engine: recreando worktree de sesión: %w", err)
			}
		}
	} else {
		var err error
		baseBranch, err = e.repo.CurrentBranch(ctx)
		if err != nil {
			return nil, fmt.Errorf("engine: leyendo rama actual: %w", err)
		}
		if baseBranch == "" {
			baseBranch, err = e.repo.RevParse(ctx, "HEAD")
			if err != nil {
				return nil, fmt.Errorf("engine: leyendo HEAD: %w", err)
			}
		}
		sessionDir = filepath.Join(gitx.WeaverDir(e.repo.Dir), "session-"+runID)
		if err := e.repo.AddWorktree(ctx, sessionDir, integBranch, baseBranch); err != nil {
			return nil, fmt.Errorf("engine: creando worktree de sesión: %w", err)
		}
	}

	st := &run.State{
		RunID:             runID,
		BaseBranch:        baseBranch,
		IntegrationBranch: integBranch,
		SessionDir:        sessionDir,
		Phase:             run.PhaseOpen,
		Approvals:         []run.Approval{},
		Agents:            []*run.Agent{},
	}
	if err := e.store.Save(st); err != nil {
		return nil, err
	}
	e.state = st
	return st, nil
}

func (e *Engine) allocateRunIDLocked(ctx context.Context) string {
	base := run.NewRunID(time.Now())
	candidate := base
	for suffix := 2; ; suffix++ {
		_, storeErr := e.store.Load(candidate)
		_, refErr := e.repo.RevParse(ctx, "refs/heads/weaver/"+candidate)
		inMemory := e.state != nil && e.state.RunID == candidate
		if storeErr != nil && refErr != nil && !inMemory {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
}

// Resume loads runID (or the latest run when empty) and marks queued/running agents interrupted.
func (e *Engine) Resume(ctx context.Context, runID string) (*run.State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var (
		st  *run.State
		err error
	)
	if runID == "" {
		st, err = e.store.Latest()
	} else {
		st, err = e.store.Load(runID)
	}
	if err != nil {
		return nil, err
	}

	st.MarkInterrupted()
	if err := e.store.Save(st); err != nil {
		return nil, err
	}

	e.state = st
	e.agentChecks = map[string]agentSummary{}
	for _, ag := range st.Agents {
		sum := e.loadAgentSummary(st.RunID, ag.ID)
		if sum.ReceiptOK != nil || sum.Verified != nil {
			e.agentChecks[ag.ID] = sum
		}
	}
	return st, nil
}

// State returns the current run state.
func (e *Engine) State() *run.State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// SetOrchestratorSessionID persists the orchestrator's CLI session ID safely.
func (e *Engine) SetOrchestratorSessionID(sessionID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state == nil {
		return errors.New("engine: no hay ejecución activa")
	}
	e.state.OrchestratorSessionID = sessionID
	return e.store.Save(e.state)
}

// CheckRefs verifies non-weaver git refs via Guard and notifies on any violations.
func (e *Engine) CheckRefs(ctx context.Context) []gitx.Violation {
	violations, err := e.guard.Check(ctx)
	if err != nil {
		return nil
	}
	if len(violations) > 0 {
		e.notify(violations)
	}
	return violations
}

// Close stops active agents, prunes empty runs, and removes the session worktree if no open runs use it.
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	var (
		cancels []context.CancelFunc
		runs    []provider.Run
		waitChs []chan struct{}
	)
	for id, cancel := range e.cancels {
		e.cancelled[id] = true
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	for _, pr := range e.runs {
		if pr != nil {
			runs = append(runs, pr)
		}
	}
	for _, ch := range e.doneChs {
		waitChs = append(waitChs, ch)
	}
	e.mu.Unlock()

	for _, pr := range runs {
		_ = pr.Kill()
	}
	for _, cancel := range cancels {
		cancel()
	}
	e.baseCancel()

	for _, ch := range waitChs {
		select {
		case <-ch:
		case <-ctx.Done():
			break
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state == nil {
		return nil
	}

	runID := e.state.RunID
	integBranch := e.state.IntegrationBranch
	baseBranch := e.state.BaseBranch
	sessionDir := e.state.SessionDir

	emptyRun := len(e.state.Approvals) == 0 && !e.hasCommitsOverBase(ctx, baseBranch, integBranch)
	if emptyRun {
		_ = e.store.Delete(runID)
	}

	openUsesSession := e.anyOpenRunUsesSessionDir(sessionDir)
	if !openUsesSession && sessionDir != "" {
		if _, err := os.Stat(sessionDir); err == nil {
			_ = e.repo.RemoveWorktree(ctx, sessionDir)
		}
	} else if emptyRun && sessionDir != "" {
		if sessRepo, err := gitx.Open(sessionDir); err == nil {
			_, _ = sessRepo.Run(ctx, "switch", "--detach")
		}
	}

	if emptyRun && integBranch != "" {
		_ = e.repo.DeleteBranch(ctx, integBranch)
		e.state = nil
	}
	return nil
}

func (e *Engine) hasCommitsOverBase(ctx context.Context, baseBranch, integBranch string) bool {
	if baseBranch == "" || integBranch == "" {
		return false
	}
	out, err := e.repo.Run(ctx, "rev-list", "--count", baseBranch+".."+integBranch)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	return err == nil && n > 0
}

func (e *Engine) anyOpenRunUsesSessionDir(sessionDir string) bool {
	if sessionDir == "" {
		return false
	}
	runs, err := e.store.List()
	if err != nil {
		return false
	}
	for _, s := range runs {
		if s.Phase != run.PhaseIntegrated && s.SessionDir == sessionDir {
			return true
		}
	}
	return false
}

func (e *Engine) notify(v any) {
	if e.notifyFn != nil {
		e.notifyFn(v)
	}
}

func (e *Engine) loadAgentSummary(runID, agentID string) agentSummary {
	var sum agentSummary
	dir := e.store.AgentDir(runID, agentID)
	if recBytes, err := os.ReadFile(filepath.Join(dir, "receipt.json")); err == nil {
		var rec receipt.Receipt
		if json.Unmarshal(recBytes, &rec) == nil {
			ok := rec.Status == "done"
			sum.ReceiptOK = &ok
		}
	}
	if verBytes, err := os.ReadFile(filepath.Join(dir, "verification.json")); err == nil {
		var ver verify.Verification
		if json.Unmarshal(verBytes, &ver) == nil {
			ok := ver.Passed
			sum.Verified = &ok
		}
	}
	return sum
}
