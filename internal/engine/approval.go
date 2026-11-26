package engine

import (
	"context"
	"errors"
	"time"

	"github.com/ImKrav/weaverharness/internal/run"
)

// RequestApproval enforces phase/plan rules, prompts the configured Approver, and persists the outcome.
func (e *Engine) RequestApproval(ctx context.Context, r ApprovalRequest) (bool, string, error) {
	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return false, "", errors.New("engine: no hay ejecución activa")
	}
	if err := e.state.CheckRequest(r.Kind); err != nil {
		e.mu.Unlock()
		return false, "", err
	}
	if r.Kind == "plan" {
		if r.Plan == nil {
			e.mu.Unlock()
			return false, "", errors.New("engine: el plan es obligatorio para aprobar plan")
		}
		if err := run.ValidatePlan(*r.Plan, e.cfg.Roles); err != nil {
			e.mu.Unlock()
			return false, "", err
		}
	}
	beforeHook := e.beforeIntegration
	afterHook := e.afterIntegrationHook
	approver := e.approver
	e.mu.Unlock()

	if r.Kind == "integration" && beforeHook != nil {
		if err := beforeHook(ctx); err != nil {
			return false, "", err
		}
	}
	if approver == nil {
		return false, "", errors.New("engine: no hay approver configurado")
	}

	approved, comment, err := approver.Ask(ctx, r)
	if err != nil {
		return false, "", err
	}

	e.mu.Lock()
	if e.state == nil {
		e.mu.Unlock()
		return false, "", errors.New("engine: la ejecución fue cerrada")
	}
	e.state.Record(run.Approval{
		Kind:     r.Kind,
		Path:     r.Path,
		Summary:  r.Summary,
		Comment:  comment,
		Approved: approved,
		At:       time.Now().UTC(),
	}, clonePlan(r.Plan))
	if err := e.store.Save(e.state); err != nil {
		e.mu.Unlock()
		return false, "", err
	}
	e.mu.Unlock()

	if r.Kind == "integration" && approved && afterHook != nil {
		msg, hookErr := afterHook(ctx)
		if hookErr != nil {
			return approved, comment, hookErr
		}
		if msg != "" {
			if comment == "" {
				comment = msg
			} else {
				comment = comment + "\n" + msg
			}
		}
	}

	return approved, comment, nil
}

func clonePlan(p *run.Plan) *run.Plan {
	if p == nil {
		return nil
	}
	cp := &run.Plan{
		Tasks:        make([]*run.Task, 0, len(p.Tasks)),
		GlobalChecks: append([]string(nil), p.GlobalChecks...),
	}
	for _, t := range p.Tasks {
		nt := *t
		nt.SpecRefs = append([]string(nil), t.SpecRefs...)
		nt.DependsOn = append([]string(nil), t.DependsOn...)
		nt.Checks = append([]string(nil), t.Checks...)
		cp.Tasks = append(cp.Tasks, &nt)
	}
	return cp
}
