package run

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ImKrav/weaverharness/internal/config"
)

var (
	ErrSpecNotApproved  = errors.New("el spec aún no está aprobado")
	ErrPlanNotApproved  = errors.New("el plan aún no está aprobado")
	ErrTasksNotAccepted = errors.New("hay tareas pendientes de aceptar")
	ErrUnknownTask      = errors.New("tarea desconocida en el plan")
	ErrDepsNotMerged    = errors.New("las dependencias de la tarea no están aceptadas y mergeadas")
	ErrTaskBusy         = errors.New("la tarea ya está en ejecución o aceptada")
	ErrRoundsExhausted  = errors.New("se agotaron las rondas de revisión para la tarea")
)

// ValidatePlan verifies task structure, roles, checks, and dependency acyclicity.
func ValidatePlan(p Plan, roles map[string]config.Role) error {
	if len(p.Tasks) == 0 {
		return errors.New("el plan debe tener al menos una tarea")
	}
	if len(p.GlobalChecks) == 0 {
		return errors.New("el plan debe tener al menos un global_check")
	}
	for i, gc := range p.GlobalChecks {
		if strings.TrimSpace(gc) == "" {
			return fmt.Errorf("global_checks[%d] no puede estar vacío", i)
		}
	}

	byID := make(map[string]*Task, len(p.Tasks))
	for _, t := range p.Tasks {
		if strings.TrimSpace(t.ID) == "" {
			return errors.New("todas las tareas deben tener id")
		}
		if t.ID == IntegrateTask {
			return fmt.Errorf("el id %q está reservado", IntegrateTask)
		}
		if _, dup := byID[t.ID]; dup {
			return fmt.Errorf("id de tarea duplicado: %q", t.ID)
		}
		if _, ok := roles[t.Role]; !ok {
			return fmt.Errorf("tarea %q usa rol desconocido %q", t.ID, t.Role)
		}
		if len(t.Checks) == 0 {
			return fmt.Errorf("tarea %q debe definir al menos un check", t.ID)
		}
		for i, c := range t.Checks {
			if strings.TrimSpace(c) == "" {
				return fmt.Errorf("tarea %q tiene check[%d] vacío", t.ID, i)
			}
		}
		byID[t.ID] = t
	}

	for _, t := range p.Tasks {
		for _, dep := range t.DependsOn {
			if _, ok := byID[dep]; !ok {
				return fmt.Errorf("tarea %q depende de %q que no existe", t.ID, dep)
			}
		}
	}

	// 0 = unvisited, 1 = visiting, 2 = visited
	visit := make(map[string]int, len(p.Tasks))
	var hasCycle func(id string) bool
	hasCycle = func(id string) bool {
		switch visit[id] {
		case 1:
			return true
		case 2:
			return false
		}
		visit[id] = 1
		for _, dep := range byID[id].DependsOn {
			if hasCycle(dep) {
				return true
			}
		}
		visit[id] = 2
		return false
	}
	for _, t := range p.Tasks {
		if hasCycle(t.ID) {
			return fmt.Errorf("dependencia cíclica detectada en torno a %q", t.ID)
		}
	}

	return nil
}

// CheckRequest enforces phase gates before requesting human approval for kind.
func (s *State) CheckRequest(kind string) error {
	switch kind {
	case "spec":
		return nil
	case "plan":
		if phaseRank(s.Phase) < phaseRank(PhaseSpecApproved) {
			return ErrSpecNotApproved
		}
		return nil
	case "integration":
		if s.Phase != PhasePlanApproved || s.Plan == nil {
			if s.Phase == PhaseIntegrated {
				return errors.New("la ejecución ya fue integrada")
			}
			return ErrPlanNotApproved
		}
		var pending []string
		for _, t := range s.Plan.Tasks {
			if t.Status != TaskAccepted {
				pending = append(pending, fmt.Sprintf("%s (%s)", t.ID, t.Status))
			}
		}
		if len(pending) > 0 {
			return fmt.Errorf("%w: %s", ErrTasksNotAccepted, strings.Join(pending, ", "))
		}
		return nil
	default:
		return fmt.Errorf("tipo de aprobación desconocido %q", kind)
	}
}

// Record stores an approval decision and advances run phase when approved.
func (s *State) Record(a Approval, p *Plan) {
	s.Approvals = append(s.Approvals, a)
	if !a.Approved {
		return
	}
	switch a.Kind {
	case "spec":
		if phaseRank(s.Phase) < phaseRank(PhaseSpecApproved) {
			s.Phase = PhaseSpecApproved
		}
	case "plan":
		s.Phase = PhasePlanApproved
		if p != nil {
			for _, t := range p.Tasks {
				if t.Status == "" {
					t.Status = TaskPending
				}
			}
			s.Plan = p
		}
	case "integration":
		s.Phase = PhaseIntegrated
	}
}

// CheckSpawn validates whether taskID can currently be spawned as a subagent.
func (s *State) CheckSpawn(taskID string, merged func(agentID string) bool) error {
	if s.Phase != PhasePlanApproved || s.Plan == nil {
		return ErrPlanNotApproved
	}
	if taskID == IntegrateTask {
		return nil
	}
	t := s.Task(taskID)
	if t == nil {
		return fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	if t.Status == TaskRunning || t.Status == TaskAccepted {
		return fmt.Errorf("%w: %s (%s)", ErrTaskBusy, taskID, t.Status)
	}
	for _, depID := range t.DependsOn {
		dep := s.Task(depID)
		if dep == nil {
			return fmt.Errorf("%w: %s", ErrUnknownTask, depID)
		}
		if dep.Status != TaskAccepted || dep.AcceptedAgent == "" {
			return fmt.Errorf("%w: dependencia %s en estado %s", ErrDepsNotMerged, depID, dep.Status)
		}
		if merged != nil && !merged(dep.AcceptedAgent) {
			return fmt.Errorf("%w: rama del agente %s (%s) aún no mergeada", ErrDepsNotMerged, dep.AcceptedAgent, depID)
		}
	}
	return nil
}

func phaseRank(p Phase) int {
	switch p {
	case PhaseOpen:
		return 0
	case PhaseSpecApproved:
		return 1
	case PhasePlanApproved:
		return 2
	case PhaseIntegrated:
		return 3
	default:
		return -1
	}
}
