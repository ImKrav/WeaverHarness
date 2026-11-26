package mcpserver

import (
	"context"
	"time"

	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type planTaskInput struct {
	ID        string   `json:"id" jsonschema:"identificador único de tarea (ej. T1)"`
	Title     string   `json:"title" jsonschema:"título breve de la tarea"`
	SpecRefs  []string `json:"spec_refs,omitempty" jsonschema:"referencias REQ-n del spec"`
	Role      string   `json:"role" jsonschema:"rol configurado para ejecutar la tarea"`
	DependsOn []string `json:"depends_on,omitempty" jsonschema:"ids de tareas previas requeridas"`
	Checks    []string `json:"checks" jsonschema:"comandos de aceptación a re-ejecutar (al menos 1)"`
}

type requestApprovalIn struct {
	Kind         string          `json:"kind" jsonschema:"tipo de aprobación: spec | plan | integration"`
	Path         string          `json:"path" jsonschema:"ruta al documento en docs/"`
	Summary      string          `json:"summary" jsonschema:"resumen claro para el usuario"`
	Tasks        []planTaskInput `json:"tasks,omitempty" jsonschema:"tareas del plan (obligatorio si kind=plan)"`
	GlobalChecks []string        `json:"global_checks,omitempty" jsonschema:"comandos de verificación global (obligatorio si kind=plan)"`
}

type requestApprovalOut struct {
	Approved bool   `json:"approved"`
	Comment  string `json:"comment,omitempty"`
}

type spawnAgentIn struct {
	TaskID       string `json:"task_id" jsonschema:"id de la tarea del plan o 'integrate'"`
	Role         string `json:"role,omitempty" jsonschema:"rol a usar (por defecto el de la tarea)"`
	Provider     string `json:"provider,omitempty" jsonschema:"proveedor opcional para sobrescribir el rol"`
	Model        string `json:"model,omitempty" jsonschema:"modelo opcional para sobrescribir el rol"`
	Instructions string `json:"instructions" jsonschema:"instrucciones específicas para el subagente"`
}

type spawnAgentOut struct {
	AgentID string `json:"agent_id"`
}

type waitAgentsIn struct {
	AgentIDs []string `json:"agent_ids" jsonschema:"lista de agent_id a esperar"`
	TimeoutS float64  `json:"timeout_s" jsonschema:"tiempo máximo de espera en segundos"`
}

type waitAgentsOut struct {
	Results []engine.AgentResult `json:"results"`
}

type followUpIn struct {
	AgentID string `json:"agent_id" jsonschema:"id del subagente a reanudar"`
	Message string `json:"message" jsonschema:"feedback o correcciones concretas"`
}

type followUpOut struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
}

type acceptTaskIn struct {
	TaskID  string `json:"task_id" jsonschema:"id de la tarea del plan"`
	AgentID string `json:"agent_id" jsonschema:"id del subagente cuyo recibo se acepta"`
}

type agentStatusIn struct {
	AgentID string `json:"agent_id,omitempty" jsonschema:"id de agente opcional; vacío lista todos"`
}

type agentStatusOut struct {
	Agents []engine.AgentInfo `json:"agents"`
}

type agentIDIn struct {
	AgentID string `json:"agent_id" jsonschema:"id del subagente"`
}

type okOut struct {
	OK bool `json:"ok"`
}

func toolErr[Out any](err error) (*mcp.CallToolResult, Out, error) {
	var zero Out
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		},
	}, zero, nil
}

func registerTools(srv *mcp.Server, e *engine.Engine) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "request_approval",
		Description: "Solicita aprobación del usuario para spec, plan o integration",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in requestApprovalIn) (*mcp.CallToolResult, requestApprovalOut, error) {
		req := engine.ApprovalRequest{
			Kind:    in.Kind,
			Path:    in.Path,
			Summary: in.Summary,
		}
		if in.Kind == "plan" || len(in.Tasks) > 0 || len(in.GlobalChecks) > 0 {
			tasks := make([]*run.Task, 0, len(in.Tasks))
			for _, t := range in.Tasks {
				tasks = append(tasks, &run.Task{
					ID:        t.ID,
					Title:     t.Title,
					Role:      t.Role,
					SpecRefs:  t.SpecRefs,
					DependsOn: t.DependsOn,
					Checks:    t.Checks,
					Status:    run.TaskPending,
				})
			}
			req.Plan = &run.Plan{
				Tasks:        tasks,
				GlobalChecks: in.GlobalChecks,
			}
		}
		approved, comment, err := e.RequestApproval(ctx, req)
		if err != nil {
			return toolErr[requestApprovalOut](err)
		}
		return nil, requestApprovalOut{Approved: approved, Comment: comment}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "spawn_agent",
		Description: "Lanza un subagente en su propio git worktree para ejecutar una tarea del plan aprobada",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in spawnAgentIn) (*mcp.CallToolResult, spawnAgentOut, error) {
		id, err := e.Spawn(ctx, in.TaskID, in.Role, in.Provider, in.Model, in.Instructions)
		if err != nil {
			return toolErr[spawnAgentOut](err)
		}
		return nil, spawnAgentOut{AgentID: id}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "wait_agents",
		Description: "Espera a que terminen los subagentes indicados y devuelve solo recibos y verificación",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in waitAgentsIn) (*mcp.CallToolResult, waitAgentsOut, error) {
		timeout := 60 * time.Second
		if in.TimeoutS > 0 {
			timeout = time.Duration(in.TimeoutS * float64(time.Second))
		}
		results := e.Wait(ctx, in.AgentIDs, timeout)
		return nil, waitAgentsOut{Results: results}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "follow_up",
		Description: "Reanuda un subagente terminado en el mismo worktree con feedback de revisión",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in followUpIn) (*mcp.CallToolResult, followUpOut, error) {
		if err := e.FollowUp(ctx, in.AgentID, in.Message); err != nil {
			return toolErr[followUpOut](err)
		}
		return nil, followUpOut{AgentID: in.AgentID, Status: string(run.AgentQueued)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "accept_task",
		Description: "Marca una tarea como aceptada tras validar su recibo y verificación RDD",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in acceptTaskIn) (*mcp.CallToolResult, okOut, error) {
		if err := e.AcceptTask(ctx, in.TaskID, in.AgentID); err != nil {
			return toolErr[okOut](err)
		}
		return nil, okOut{OK: true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "agent_status",
		Description: "Consulta el estado resumido de uno o todos los subagentes",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in agentStatusIn) (*mcp.CallToolResult, agentStatusOut, error) {
		return nil, agentStatusOut{Agents: e.Status(in.AgentID)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cancel_agent",
		Description: "Detiene un subagente en ejecución conservando su worktree para inspección",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in agentIDIn) (*mcp.CallToolResult, okOut, error) {
		if err := e.Cancel(in.AgentID); err != nil {
			return toolErr[okOut](err)
		}
		return nil, okOut{OK: true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cleanup_agent",
		Description: "Elimina el worktree y la rama de un subagente ya mergeado en la rama de integración",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentIDIn) (*mcp.CallToolResult, okOut, error) {
		if err := e.Cleanup(ctx, in.AgentID); err != nil {
			return toolErr[okOut](err)
		}
		return nil, okOut{OK: true}, nil
	})
}
