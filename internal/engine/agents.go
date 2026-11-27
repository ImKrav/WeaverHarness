package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/prompts"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/ImKrav/weaverharness/internal/receipt"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/ImKrav/weaverharness/internal/verify"
)

const (
	diffStatMaxBytes         = 4096
	defaultAgentDiffMaxBytes = 32768
	maxAgentDiffBytes        = 65536
)

// Spawn creates a task worktree and launches a subagent asynchronously, respecting max_parallel.
func (e *Engine) Spawn(ctx context.Context, taskID, role, prov, model, instructions string) (string, error) {
	e.guardMu.Lock()
	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		e.guardMu.Unlock()
		return "", errors.New("engine: no hay ejecución activa")
	}

	mergedFn := func(agentID string) bool {
		ag := e.state.Agent(agentID)
		if ag == nil {
			return false
		}
		return e.isAgentMerged(ctx, ag.Branch, ag.MergedCommit, e.state.IntegrationBranch)
	}
	if err := e.state.CheckSpawn(taskID, mergedFn); err != nil {
		e.mu.Unlock()
		e.guardMu.Unlock()
		return "", err
	}

	task := e.state.Task(taskID)
	if role == "" && task != nil {
		role = task.Role
	}
	if roleCfg, ok := e.cfg.Roles[role]; ok {
		if prov == "" {
			prov = roleCfg.Provider
		}
		if model == "" {
			model = roleCfg.Model
		}
	}

	p, ok := e.providers[prov]
	if !ok {
		e.mu.Unlock()
		e.guardMu.Unlock()
		return "", fmt.Errorf("engine: proveedor %q no configurado", prov)
	}

	agentID := e.state.NewAgentID()
	branch := fmt.Sprintf("weaver/%s-%s", e.state.RunID, agentID)
	worktree := filepath.Join(gitx.WeaverDir(e.repo.Dir), e.state.RunID, agentID)
	baseSHA, err := e.repo.RevParse(ctx, e.state.IntegrationBranch)
	if err != nil {
		e.mu.Unlock()
		e.guardMu.Unlock()
		return "", fmt.Errorf("engine: leyendo rama de integración %s: %w", e.state.IntegrationBranch, err)
	}

	if err := e.repo.AddWorktree(ctx, worktree, branch, baseSHA); err != nil {
		e.mu.Unlock()
		e.guardMu.Unlock()
		return "", fmt.Errorf("engine: creando worktree de agente: %w", err)
	}

	agentDir := e.store.AgentDir(e.state.RunID, agentID)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		e.mu.Unlock()
		e.guardMu.Unlock()
		return "", fmt.Errorf("engine: creando directorio de agente: %w", err)
	}

	wData := prompts.WorkerData{
		TaskID:       taskID,
		SpecPath:     e.latestApprovedSpecLocked(),
		Branch:       branch,
		Instructions: instructions,
	}
	if task != nil {
		wData.Title = task.Title
		wData.SpecRefs = task.SpecRefs
		wData.Checks = task.Checks
	} else {
		wData.Title = "Resolver conflictos de integración"
		if e.state.Plan != nil {
			wData.Checks = e.state.Plan.GlobalChecks
		}
	}

	workerPrompt, err := prompts.Worker(e.repo.Dir, wData)
	if err != nil {
		e.mu.Unlock()
		e.guardMu.Unlock()
		return "", fmt.Errorf("engine: renderizando prompt de worker: %w", err)
	}

	ag := &run.Agent{
		ID:        agentID,
		TaskID:    taskID,
		Role:      role,
		Provider:  prov,
		Model:     model,
		Branch:    branch,
		Base:      baseSHA,
		Worktree:  worktree,
		Status:    run.AgentQueued,
		StartedAt: time.Now().UTC(),
	}
	e.state.Agents = append(e.state.Agents, ag)
	if task != nil {
		task.Status = run.TaskRunning
	}
	if err := e.store.Save(e.state); err != nil {
		e.mu.Unlock()
		e.guardMu.Unlock()
		return "", err
	}

	runCtx, cancel := context.WithCancel(e.baseCtx)
	doneCh := make(chan struct{})
	e.cancels[agentID] = cancel
	e.doneChs[agentID] = doneCh
	e.cancelled[agentID] = false

	infos := e.snapshotInfosLocked()
	e.mu.Unlock()
	if err := e.guard.Rebase(ctx); err != nil {
		e.guardMu.Unlock()
		cancel()
		close(doneCh)
		return "", fmt.Errorf("engine: actualizando guardia de refs tras spawn: %w", err)
	}
	e.guardMu.Unlock()

	e.notify(infos)

	req := provider.RunRequest{
		Mode:  provider.Worker,
		Dir:   worktree,
		Model: model,
	}
	if p.Name() == "claude" {
		promptFile := filepath.Join(agentDir, "worker_prompt.md")
		if err := os.WriteFile(promptFile, []byte(workerPrompt), 0o600); err == nil {
			req.SystemPromptFile = promptFile
		}
		if instructions != "" {
			req.Prompt = instructions
		} else {
			req.Prompt = "Ejecuta la tarea " + taskID
		}
	} else {
		req.Prompt = workerPrompt
	}

	go e.runAgent(runCtx, cancel, doneCh, agentID, p, req)
	return agentID, nil
}

// FollowUp resumes a finished agent session in the same worktree with feedback, up to ReviewRounds.
func (e *Engine) FollowUp(ctx context.Context, agentID, msg string) error {
	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return errors.New("engine: no hay ejecución activa")
	}
	ag := e.state.Agent(agentID)
	if ag == nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: agente desconocido %q", agentID)
	}
	if ag.Status == run.AgentQueued || ag.Status == run.AgentRunning {
		e.mu.Unlock()
		return fmt.Errorf("engine: el agente %s sigue activo (%s)", agentID, ag.Status)
	}

	task := e.state.Task(ag.TaskID)
	if task != nil {
		if task.Rounds >= e.cfg.ReviewRounds {
			e.mu.Unlock()
			return fmt.Errorf("%w: %s (%d/%d)", run.ErrRoundsExhausted, task.ID, task.Rounds, e.cfg.ReviewRounds)
		}
		task.Rounds++
		task.Status = run.TaskRunning
	}

	p, ok := e.providers[ag.Provider]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("engine: proveedor %q no configurado", ag.Provider)
	}

	ag.Status = run.AgentQueued
	ag.Err = ""
	ag.EndedAt = time.Time{}
	if err := e.store.Save(e.state); err != nil {
		e.mu.Unlock()
		return err
	}

	runCtx, cancel := context.WithCancel(e.baseCtx)
	doneCh := make(chan struct{})
	e.cancels[agentID] = cancel
	e.doneChs[agentID] = doneCh
	e.cancelled[agentID] = false

	req := provider.RunRequest{
		Mode:     provider.Worker,
		Dir:      ag.Worktree,
		Model:    ag.Model,
		Prompt:   msg,
		ResumeID: ag.SessionID,
	}
	infos := e.snapshotInfosLocked()
	e.mu.Unlock()

	e.notify(infos)
	go e.runAgent(runCtx, cancel, doneCh, agentID, p, req)
	return nil
}

func (e *Engine) runAgent(
	runCtx context.Context,
	cancel context.CancelFunc,
	doneCh chan struct{},
	agentID string,
	p provider.Provider,
	req provider.RunRequest,
) {
	defer close(doneCh)
	defer func() {
		cancel()
		e.mu.Lock()
		delete(e.cancels, agentID)
		delete(e.runs, agentID)
		e.mu.Unlock()
	}()

	sem := e.semaphoreFor(p.Name())
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-runCtx.Done():
		e.markCancelled(agentID)
		return
	}

	e.mu.Lock()
	if e.cancelled[agentID] || e.state == nil {
		e.mu.Unlock()
		e.markCancelled(agentID)
		return
	}
	ag := e.state.Agent(agentID)
	if ag == nil {
		e.mu.Unlock()
		return
	}
	ag.Status = run.AgentRunning
	runID := e.state.RunID
	taskID := ag.TaskID
	branch := ag.Branch
	base := ag.Base
	worktree := ag.Worktree
	var taskChecks []string
	if task := e.state.Task(taskID); task != nil {
		taskChecks = append([]string(nil), task.Checks...)
	} else if e.state.Plan != nil {
		taskChecks = append([]string(nil), e.state.Plan.GlobalChecks...)
	}
	_ = e.store.Save(e.state)
	infos := e.snapshotInfosLocked()
	e.mu.Unlock()

	e.notify(infos)

	agentDir := e.store.AgentDir(runID, agentID)
	final, cancelled, err := e.executeProvider(runCtx, agentID, p, req, agentDir)
	if cancelled || e.isCancelled(agentID) {
		e.markCancelled(agentID)
		return
	}
	if err != nil {
		e.markFailed(agentID, err.Error())
		return
	}

	// Step 1: protect uncommitted work in the agent's branch.
	wtRepo, openErr := gitx.Open(worktree)
	if openErr == nil {
		_, _ = wtRepo.CommitAll(runCtx, "weaver: cambios sin commit de "+agentID)
	}

	if final.SessionID != "" {
		e.mu.Lock()
		if a := e.state.Agent(agentID); a != nil {
			a.SessionID = final.SessionID
			_ = e.store.Save(e.state)
		}
		e.mu.Unlock()
	}

	if final.Err != nil {
		if final.Err.Class == provider.Timeout {
			e.markTimeout(agentID, final.Err.Error())
			return
		}
		e.markFailed(agentID, final.Err.Error())
		return
	}

	// Step 2: extract and validate receipt, retrying once on miss/invalid without counting a round.
	rec, recErr := extractAndValidate(final.Text, taskID)
	if recErr != nil {
		retryReq := provider.RunRequest{
			Mode:     provider.Worker,
			Dir:      worktree,
			Model:    req.Model,
			Prompt:   prompts.ReceiptRetry(e.repo.Dir),
			ResumeID: final.SessionID,
		}
		retryFinal, retryCancelled, retryStartErr := e.executeProvider(runCtx, agentID, p, retryReq, agentDir)
		if retryCancelled || e.isCancelled(agentID) {
			e.markCancelled(agentID)
			return
		}
		if openErr == nil {
			_, _ = wtRepo.CommitAll(runCtx, "weaver: cambios sin commit de "+agentID)
		}
		if retryFinal.SessionID != "" {
			e.mu.Lock()
			if a := e.state.Agent(agentID); a != nil {
				a.SessionID = retryFinal.SessionID
				_ = e.store.Save(e.state)
			}
			e.mu.Unlock()
		}
		if retryStartErr != nil {
			e.markFailed(agentID, "no_receipt")
			return
		}
		if retryFinal.Err != nil {
			if retryFinal.Err.Class == provider.Timeout {
				e.markTimeout(agentID, retryFinal.Err.Error())
				return
			}
			e.markFailed(agentID, "no_receipt")
			return
		}
		rec, recErr = extractAndValidate(retryFinal.Text, taskID)
		if recErr != nil {
			e.markFailed(agentID, "no_receipt")
			return
		}
	}

	// Step 3: check ref violations across the repo, allowing this agent and other active agents.
	violations := e.checkRefsExcept(runCtx, "refs/heads/"+branch)

	// Step 4: verify receipt against commits, diff, and re-run checks.
	ver := verify.Verification{
		CommitsMissing: []string{},
		OnlyInReceipt:  []string{},
		OnlyInDiff:     []string{},
		Checks:         []verify.CheckResult{},
		RefViolations:  []gitx.Violation{},
	}
	if openErr == nil {
		ver = verify.Verify(runCtx, verify.Input{
			Worktree:   wtRepo,
			Base:       base,
			Branch:     branch,
			Receipt:    rec,
			TaskChecks: taskChecks,
			Timeout:    e.cfg.CheckTimeout.Duration,
		})
	}
	ver.RefViolations = violations
	ver.Finalize()

	// Step 5: persist compact diffstat so wait_agents works after cleanup/resume without locking.
	if openErr == nil {
		if stat, statErr := wtRepo.DiffStat(runCtx, base, branch); statErr == nil && stat != "" {
			stat, _ = truncateUTF8(stat, diffStatMaxBytes)
			_ = os.WriteFile(filepath.Join(agentDir, "diffstat.txt"), []byte(stat), 0o600)
		}
	}

	// Step 6: write receipt.json, verification.json, update state, and notify.
	_ = writeJSONFile(filepath.Join(agentDir, "receipt.json"), rec)
	_ = writeJSONFile(filepath.Join(agentDir, "verification.json"), ver)

	e.mu.Lock()
	if e.state != nil {
		if a := e.state.Agent(agentID); a != nil {
			a.Status = run.AgentFinished
			a.EndedAt = time.Now().UTC()
		}
		rOK := rec.Status == "done"
		vOK := ver.Passed
		e.agentChecks[agentID] = agentSummary{ReceiptOK: &rOK, Verified: &vOK}
		if t := e.state.Task(taskID); t != nil {
			if rOK && vOK {
				t.Status = run.TaskReceiptOK
			} else {
				t.Status = run.TaskReceiptFailed
			}
		}
		_ = e.store.Save(e.state)
		infos = e.snapshotInfosLocked()
	}
	e.mu.Unlock()

	e.notify(infos)
}

func (e *Engine) executeProvider(
	runCtx context.Context,
	agentID string,
	p provider.Provider,
	req provider.RunRequest,
	agentDir string,
) (provider.Final, bool, error) {
	timeoutCtx, timeoutCancel := context.WithTimeout(runCtx, e.cfg.TaskTimeout.Duration)
	defer timeoutCancel()

	pr, err := p.Start(timeoutCtx, req)
	if err != nil {
		if errors.Is(runCtx.Err(), context.Canceled) {
			return provider.Final{}, true, nil
		}
		return provider.Final{}, false, err
	}

	e.mu.Lock()
	if e.cancelled[agentID] {
		e.mu.Unlock()
		_ = pr.Kill()
		for range pr.Events() {
		}
		_ = pr.Wait()
		return provider.Final{}, true, nil
	}
	e.runs[agentID] = pr
	e.mu.Unlock()

	transcriptFile, openErr := os.OpenFile(
		filepath.Join(agentDir, "transcript.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	for ev := range pr.Events() {
		if openErr == nil && ev.Kind == provider.EventRaw {
			_, _ = transcriptFile.Write(ev.Raw)
			_, _ = transcriptFile.Write([]byte("\n"))
		}
	}
	if openErr == nil {
		_ = transcriptFile.Close()
	}

	final := pr.Wait()

	e.mu.Lock()
	delete(e.runs, agentID)
	e.mu.Unlock()

	if errors.Is(runCtx.Err(), context.Canceled) {
		return final, true, nil
	}
	return final, false, nil
}

func extractAndValidate(text, taskID string) (receipt.Receipt, error) {
	rec, err := receipt.Extract(text)
	if err != nil {
		return receipt.Receipt{}, err
	}
	if err := rec.Validate(taskID); err != nil {
		return receipt.Receipt{}, err
	}
	return rec, nil
}

func (e *Engine) markCancelled(agentID string) {
	e.setTerminalStatus(agentID, run.AgentCancelled, "")
}

func (e *Engine) markTimeout(agentID, msg string) {
	e.setTerminalStatus(agentID, run.AgentTimeout, msg)
}

func (e *Engine) markFailed(agentID, msg string) {
	e.setTerminalStatus(agentID, run.AgentFailed, msg)
}

func (e *Engine) setTerminalStatus(agentID string, status run.AgentStatus, errMsg string) {
	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return
	}
	if ag := e.state.Agent(agentID); ag != nil {
		ag.Status = status
		ag.Err = errMsg
		ag.EndedAt = time.Now().UTC()
		if t := e.state.Task(ag.TaskID); t != nil && t.Status == run.TaskRunning {
			if status == run.AgentCancelled {
				t.Status = run.TaskPending
			} else {
				t.Status = run.TaskReceiptFailed
			}
		}
	}
	_ = e.store.Save(e.state)
	infos := e.snapshotInfosLocked()
	e.mu.Unlock()

	e.notify(infos)
}

func (e *Engine) isCancelled(agentID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cancelled[agentID]
}

func (e *Engine) semaphoreFor(provName string) chan struct{} {
	if ch, ok := e.sems[provName]; ok {
		return ch
	}
	return make(chan struct{}, 3)
}

func (e *Engine) latestApprovedSpecLocked() string {
	if e.state == nil {
		return ""
	}
	for i := len(e.state.Approvals) - 1; i >= 0; i-- {
		a := e.state.Approvals[i]
		if a.Kind == "spec" && a.Approved {
			return a.Path
		}
	}
	return ""
}

// Wait blocks until all ids complete, timeout elapses, or ctx is cancelled.
func (e *Engine) Wait(ctx context.Context, ids []string, timeout time.Duration) []AgentResult {
	e.mu.Lock()
	chs := make([]chan struct{}, len(ids))
	for i, id := range ids {
		chs[i] = e.doneChs[id]
	}
	e.mu.Unlock()

	var timerCh <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerCh = timer.C
	}

waitLoop:
	for _, ch := range chs {
		if ch == nil {
			continue
		}
		select {
		case <-ch:
		case <-timerCh:
			break waitLoop
		case <-ctx.Done():
			break waitLoop
		}
	}

	type waitMeta struct {
		id       string
		status   string
		err      string
		agentDir string
	}

	e.mu.Lock()
	metas := make([]waitMeta, 0, len(ids))
	for _, id := range ids {
		m := waitMeta{id: id}
		if e.state != nil {
			ag := e.state.Agent(id)
			if ag == nil {
				m.err = "agente desconocido"
			} else {
				m.status = string(ag.Status)
				m.err = ag.Err
				if ag.Status != run.AgentQueued && ag.Status != run.AgentRunning {
					m.agentDir = e.store.AgentDir(e.state.RunID, id)
				}
			}
		}
		metas = append(metas, m)
	}
	e.mu.Unlock()

	results := make([]AgentResult, 0, len(metas))
	for _, m := range metas {
		res := AgentResult{
			ID:     m.id,
			Status: m.status,
			Err:    m.err,
		}
		if m.agentDir != "" {
			if recBytes, err := os.ReadFile(filepath.Join(m.agentDir, "receipt.json")); err == nil {
				var rec receipt.Receipt
				if json.Unmarshal(recBytes, &rec) == nil {
					res.Receipt = &rec
				}
			}
			if verBytes, err := os.ReadFile(filepath.Join(m.agentDir, "verification.json")); err == nil {
				var ver verify.Verification
				if json.Unmarshal(verBytes, &ver) == nil {
					res.Verification = &ver
				}
			}
			if statBytes, err := os.ReadFile(filepath.Join(m.agentDir, "diffstat.txt")); err == nil {
				res.DiffStat = string(statBytes)
			}
		}
		results = append(results, res)
	}
	return results
}

// AgentDiff returns the git diff for agentID, optionally filtered by path and safely truncated at UTF-8 rune boundaries.
func (e *Engine) AgentDiff(ctx context.Context, agentID, path string, maxBytes int) (string, bool, error) {
	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return "", false, errors.New("engine: no hay ejecución activa")
	}
	ag := e.state.Agent(agentID)
	if ag == nil {
		e.mu.Unlock()
		return "", false, fmt.Errorf("engine: agente desconocido %q", agentID)
	}
	base := ag.Base
	branch := ag.Branch
	mergedCommit := ag.MergedCommit
	e.mu.Unlock()

	if maxBytes <= 0 {
		maxBytes = defaultAgentDiffMaxBytes
	} else if maxBytes > maxAgentDiffBytes {
		maxBytes = maxAgentDiffBytes
	}

	var head string
	if branch != "" {
		if _, err := e.repo.RevParse(ctx, "refs/heads/"+branch); err == nil {
			head = "refs/heads/" + branch
		}
	}
	if head == "" {
		head = mergedCommit
	}
	if head == "" {
		return "", false, fmt.Errorf("engine: la rama %s del agente %s ya no existe", branch, agentID)
	}

	raw, err := e.repo.Diff(ctx, base, head, path)
	if err != nil {
		return "", false, err
	}
	diff, truncated := truncateUTF8(raw, maxBytes)
	return diff, truncated, nil
}

func truncateUTF8(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		if s == "" {
			return "", false
		}
		return "", true
	}
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// AcceptTask marks taskID as accepted after verifying agentID's latest receipt is done and passed verification.
func (e *Engine) AcceptTask(ctx context.Context, taskID, agentID string) error {
	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return errors.New("engine: no hay ejecución activa")
	}
	task := e.state.Task(taskID)
	if task == nil {
		e.mu.Unlock()
		return fmt.Errorf("%w: %s", run.ErrUnknownTask, taskID)
	}
	ag := e.state.Agent(agentID)
	if ag == nil || ag.TaskID != taskID {
		e.mu.Unlock()
		return fmt.Errorf("engine: el agente %q no pertenece a la tarea %q", agentID, taskID)
	}
	if ag.Status != run.AgentFinished {
		e.mu.Unlock()
		return fmt.Errorf("engine: el agente %s no finalizó correctamente (%s)", agentID, ag.Status)
	}

	dir := e.store.AgentDir(e.state.RunID, agentID)
	recBytes, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: falta recibo de %s: %w", agentID, err)
	}
	var rec receipt.Receipt
	if err := json.Unmarshal(recBytes, &rec); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: recibo inválido de %s: %w", agentID, err)
	}
	if rec.Status != "done" {
		e.mu.Unlock()
		return fmt.Errorf("engine: el recibo de %s tiene status=%q (requiere done)", agentID, rec.Status)
	}

	verBytes, err := os.ReadFile(filepath.Join(dir, "verification.json"))
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: falta verificación de %s: %w", agentID, err)
	}
	var ver verify.Verification
	if err := json.Unmarshal(verBytes, &ver); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: verificación inválida de %s: %w", agentID, err)
	}
	if !ver.Passed {
		e.mu.Unlock()
		return fmt.Errorf("engine: la verificación RDD de %s no pasó", agentID)
	}

	task.Status = run.TaskAccepted
	task.AcceptedAgent = agentID
	if err := e.store.Save(e.state); err != nil {
		e.mu.Unlock()
		return err
	}

	infos := e.snapshotInfosLocked()
	e.mu.Unlock()

	e.notify(infos)
	return nil
}

// Status returns summaries for agentID, or all agents when agentID is empty.
func (e *Engine) Status(agentID string) []AgentInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state == nil {
		return []AgentInfo{}
	}
	all := e.snapshotInfosLocked()
	if agentID == "" {
		return all
	}
	for _, info := range all {
		if info.ID == agentID {
			return []AgentInfo{info}
		}
	}
	return []AgentInfo{}
}

func (e *Engine) snapshotInfosLocked() []AgentInfo {
	if e.state == nil {
		return []AgentInfo{}
	}
	infos := make([]AgentInfo, 0, len(e.state.Agents))
	for _, ag := range e.state.Agents {
		sum := e.agentChecks[ag.ID]
		infos = append(infos, AgentInfo{
			ID:        ag.ID,
			TaskID:    ag.TaskID,
			Role:      ag.Role,
			Provider:  ag.Provider,
			Model:     ag.Model,
			Status:    string(ag.Status),
			Err:       ag.Err,
			Started:   ag.StartedAt,
			ReceiptOK: sum.ReceiptOK,
			Verified:  sum.Verified,
		})
	}
	return infos
}

// Cancel terminates an active agent's process tree while preserving its worktree.
func (e *Engine) Cancel(agentID string) error {
	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return errors.New("engine: no hay ejecución activa")
	}
	ag := e.state.Agent(agentID)
	if ag == nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: agente desconocido %q", agentID)
	}
	e.cancelled[agentID] = true
	cancel := e.cancels[agentID]
	pr := e.runs[agentID]
	if ag.Status == run.AgentQueued || ag.Status == run.AgentRunning {
		ag.Status = run.AgentCancelled
		ag.EndedAt = time.Now().UTC()
		if t := e.state.Task(ag.TaskID); t != nil && t.Status == run.TaskRunning {
			t.Status = run.TaskPending
		}
		_ = e.store.Save(e.state)
	}
	infos := e.snapshotInfosLocked()
	e.mu.Unlock()

	if pr != nil {
		_ = pr.Kill()
	}
	if cancel != nil {
		cancel()
	}
	e.notify(infos)
	return nil
}

// Cleanup removes an agent's worktree and branch after verifying it is merged into IntegrationBranch.
func (e *Engine) Cleanup(ctx context.Context, agentID string) error {
	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return errors.New("engine: no hay ejecución activa")
	}
	ag := e.state.Agent(agentID)
	if ag == nil {
		e.mu.Unlock()
		return fmt.Errorf("engine: agente desconocido %q", agentID)
	}
	branch := ag.Branch
	worktree := ag.Worktree
	integBranch := e.state.IntegrationBranch
	e.mu.Unlock()

	merged, err := e.repo.IsAncestor(ctx, branch, integBranch)
	if err != nil {
		return fmt.Errorf("engine: verificando merge de %s: %w", branch, err)
	}
	if !merged {
		return fmt.Errorf("engine: la rama %s aún no está mergeada en %s", branch, integBranch)
	}
	headSHA, err := e.repo.RevParse(ctx, branch)
	if err != nil {
		return fmt.Errorf("engine: resolviendo HEAD de %s: %w", branch, err)
	}

	e.mu.Lock()
	if cur := e.state.Agent(agentID); cur != nil {
		cur.MergedCommit = headSHA
		if err := e.store.Save(e.state); err != nil {
			e.mu.Unlock()
			return err
		}
	}
	e.mu.Unlock()

	e.guardMu.Lock()
	defer e.guardMu.Unlock()
	if err := e.repo.RemoveWorktree(ctx, worktree); err != nil {
		return err
	}
	if err := e.repo.DeleteBranch(ctx, branch); err != nil {
		return err
	}
	return e.guard.Rebase(ctx)
}

func (e *Engine) isAgentMerged(ctx context.Context, branch, mergedCommit, integBranch string) bool {
	if branch != "" {
		if ok, err := e.repo.IsAncestor(ctx, branch, integBranch); err == nil && ok {
			return true
		}
	}
	if mergedCommit != "" {
		if ok, err := e.repo.IsAncestor(ctx, mergedCommit, integBranch); err == nil && ok {
			return true
		}
	}
	return false
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
