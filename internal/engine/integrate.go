package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/ImKrav/weaverharness/internal/verify"
)

func init() {
	integrationHooks = func(e *Engine) {
		e.beforeIntegration = e.checkIntegration
		e.afterIntegrationHook = e.afterIntegration
	}
}

func (e *Engine) checkIntegration(ctx context.Context) error {
	e.mu.Lock()
	if e.state == nil || e.state.Plan == nil {
		e.mu.Unlock()
		return errors.New("engine: no hay plan activo para integrar")
	}

	integBranch := e.state.IntegrationBranch
	sessionDir := e.state.SessionDir
	globalChecks := append([]string(nil), e.state.Plan.GlobalChecks...)
	timeout := e.cfg.CheckTimeout.Duration

	type taskCheck struct {
		id           string
		branch       string
		mergedCommit string
	}
	var toVerify []taskCheck
	var missing []string
	for _, t := range e.state.Plan.Tasks {
		if t.Status != run.TaskAccepted || t.AcceptedAgent == "" {
			missing = append(missing, t.ID)
			continue
		}
		ag := e.state.Agent(t.AcceptedAgent)
		if ag == nil || (ag.Branch == "" && ag.MergedCommit == "") {
			missing = append(missing, t.ID)
			continue
		}
		toVerify = append(toVerify, taskCheck{id: t.ID, branch: ag.Branch, mergedCommit: ag.MergedCommit})
	}
	e.mu.Unlock()

	for _, tc := range toVerify {
		if !e.isAgentMerged(ctx, tc.branch, tc.mergedCommit, integBranch) {
			missing = append(missing, tc.id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("engine: tareas no integradas en %s: %s", integBranch, strings.Join(missing, ", "))
	}

	var failures []string
	for _, cmd := range globalChecks {
		exit, tail, timedOut := verify.RunCheck(ctx, sessionDir, cmd, timeout)
		if timedOut {
			failures = append(failures, fmt.Sprintf("- %s: timeout (%s)", cmd, strings.TrimSpace(tail)))
			continue
		}
		if exit != 0 {
			failures = append(failures, fmt.Sprintf("- %s: exit %d (%s)", cmd, exit, strings.TrimSpace(tail)))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("engine: fallaron los global_checks en %s:\n%s", integBranch, strings.Join(failures, "\n"))
	}
	return nil
}

func (e *Engine) afterIntegration(ctx context.Context) (string, error) {
	e.guardMu.Lock()
	defer e.guardMu.Unlock()

	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return "", errors.New("engine: no hay ejecución activa")
	}
	baseBranch := e.state.BaseBranch
	integBranch := e.state.IntegrationBranch
	metrics := run.Summarize(e.state)
	e.mu.Unlock()

	summaryLine := fmt.Sprintf(
		"\nResumen: %d/%d tareas aceptadas, %d agentes, duración activa %s (wall %s).",
		metrics.AcceptedTasks,
		metrics.TotalTasks,
		metrics.TotalAgents,
		formatMetricDuration(metrics.ActiveDuration),
		formatMetricDuration(metrics.WallDuration),
	)

	curBranch, err := e.repo.CurrentBranch(ctx)
	if err != nil || curBranch != baseBranch {
		return fmt.Sprintf(
			"La rama actual del repositorio es %q (se esperaba %q); no se modificó el repo principal. Integra manualmente: git merge %s%s",
			curBranch,
			baseBranch,
			integBranch,
			summaryLine,
		), nil
	}

	if err := e.repo.MergeFFOnly(ctx, integBranch); err != nil {
		return fmt.Sprintf(
			"No se pudo hacer fast-forward de %s sobre %s (%v); el repo principal quedó intacto. Integra manualmente: git merge %s%s",
			integBranch,
			baseBranch,
			err,
			integBranch,
			summaryLine,
		), nil
	}

	if err := e.guard.Rebase(ctx); err != nil {
		return "", fmt.Errorf("engine: actualizando guardia tras fast-forward: %w", err)
	}
	return fmt.Sprintf("Integración aplicada: fast-forward de %s en %s.%s", integBranch, baseBranch, summaryLine), nil
}

func formatMetricDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return d.Truncate(time.Millisecond).String()
	}
	return d.Truncate(time.Second).String()
}
