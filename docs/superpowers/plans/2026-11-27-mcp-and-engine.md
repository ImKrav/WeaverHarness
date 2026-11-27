# WeaverHarness — Plan 2: Herramientas MCP y motor

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dar al orquestador visibilidad completa de estado y diffs sin exponer transcripciones, acotar en código el timeout de `wait_agents`, endurecer la guardia de refs sobre ramas `weaver/` **sin romper agentes en paralelo**, corregir el ciclo multi-run en una misma sesión y recuperar worktrees al reanudar.

**Architecture:** Se extienden `gitx` (diffs deterministas sin color, `AttachWorktree` y protección selectiva de refs `weaver/`), `engine` (`RunSnapshot`, `AgentDiff`, `DiffStat` persistido, allowlist dinámica de ramas activas para `Guard`, apertura automática de nueva ejecución tras `integrated`, recuperación de worktree en `Resume`) y `mcpserver` (nuevas herramientas `run_state` y `agent_diff`, `DiffStat` en `wait_agents`, clamp de `timeout_s`), actualizando `internal/prompts/orchestrator.md`.

**Tech Stack:** Go 1.27 · `github.com/modelcontextprotocol/go-sdk` · `git` CLI (`internal/gitx`).

**Base:** `docs/superpowers/specs/2026-11-26-weaverharness-design.md` · `docs/superpowers/plans/2026-11-26-weaverharness-v1.md`

## Contexto del código existente y riesgos críticos evitados

- **Test de listado exacto en MCP:** `internal/mcpserver/server_test.go` (`TestToolsListed`) afirma la lista exacta de nombres de herramientas; debe actualizarse en Task 1 (`+run_state`) y Task 2 (`+agent_diff`).
- **Agentes en paralelo y `Guard`:** pueden ejecutarse varios subagentes simultáneamente (`max_parallel = 3`). Si al terminar `a1` la guardia solo exime la rama de `a1`, **revertiría los commits recién hechos por `a2`**. Además, `Spawn` crea ramas `weaver/*` y `Cleanup` las borra; si el `Guard` no actualiza su baseline en esas operaciones legítimas del engine, reportaría falsos `created`/`deleted` o resucitaría ramas borradas. Task 3 resuelve explícitamente ambos puntos.
- **Bloqueo de `e.mu` durante comandos `git`:** ni `RunSnapshot`, ni `Wait`, ni `AgentDiff` deben mantener `e.mu.Lock()` mientras ejecutan procesos `git` o leen diffs grandes.
- **Multi-run en una misma sesión (§5.3.2 del diseño):** actualmente `afterIntegration` marca `Phase = PhaseIntegrated` pero no abre automáticamente la siguiente ejecución; si el usuario pide un segundo cambio en la misma sesión TUI, `request_approval(spec)` reutilizaría el estado ya integrado. Task 5 cierra ese hueco.

## Global Constraints

- **Anti-objetivo inviolable:** ni `wait_agents`, ni `run_state`, ni `agent_diff` pueden devolver nunca líneas de `transcript.jsonl`.
- Los errores de reglas del `Engine` se devuelven en MCP como `CallToolResult{IsError: true}` en español, nunca como error de protocolo JSON-RPC.
- Las 8 herramientas MCP existentes mantienen compatibilidad retroactiva en entradas y salidas.
- Todos los tests de `internal/gitx/`, `internal/engine/` e `internal/mcpserver/` deben pasar con `-race`.

---

### Task 1: Herramienta MCP `run_state` para inspeccionar fase, tareas y dependencias

**Files:**
- Modify: `internal/engine/engine.go`, `internal/mcpserver/tools.go`, `internal/prompts/orchestrator.md`
- Test: `internal/engine/engine_test.go`, `internal/mcpserver/server_test.go`

**Interfaces:**
- Consumes: `run.State`, `Engine.isAgentMerged`.
- Produces:
  ```go
  // internal/engine
  type TaskSnapshot struct {
      ID            string   `json:"id"`
      Title         string   `json:"title"`
      Role          string   `json:"role"`
      DependsOn     []string `json:"depends_on,omitempty"`
      Status        string   `json:"status"`
      Rounds        int      `json:"rounds"`
      AcceptedAgent string   `json:"accepted_agent,omitempty"`
      Merged        bool     `json:"merged"`
  }
  type RunSnapshot struct {
      RunID             string         `json:"run_id"`
      Phase             string         `json:"phase"`
      BaseBranch        string         `json:"base_branch"`
      IntegrationBranch string         `json:"integration_branch"`
      SpecPath          string         `json:"spec_path,omitempty"`
      Tasks             []TaskSnapshot `json:"tasks"`
      Agents            []AgentInfo    `json:"agents"`
  }
  func (e *Engine) RunSnapshot(ctx context.Context) (RunSnapshot, error)
  ```
  - MCP tool `run_state` (entrada vacía `{}`) → devuelve `RunSnapshot`.

- [ ] **Step 1: Tests que fallan:**
  - `TestRunSnapshotReportsPhaseAndMergedTasks` (`engine`): tras aprobar spec+plan, aceptar `T1` y mergear su rama en `IntegrationBranch` (incluso después de `Cleanup`), `RunSnapshot` reporta `Phase == "plan_approved"`, `SpecPath`, `T1.Status == "accepted"`, `T1.Merged == true` y `T2.Merged == false`.
  - `TestMCPRunStateListedAndCallable` (`mcpserver`): actualiza `TestToolsListed` para incluir `run_state` y verifica que llamarla devuelve el JSON de la ejecución activa sin ningún dato de `transcript.jsonl`.
- [ ] **Step 2:** `go test ./internal/engine/ ./internal/mcpserver/` → FAIL.
- [ ] **Step 3: Implementar.**
  - Implementar `Engine.RunSnapshot(ctx)`: copiar los datos necesarios bajo `e.mu.Lock()`, liberar `e.mu.Unlock()`, evaluar `e.isAgentMerged(...)` fuera del lock y ensamblar el `RunSnapshot`.
  - Registrar `run_state` en `internal/mcpserver/tools.go` y documentarla en `internal/prompts/orchestrator.md` (recomendando llamarla al reanudar con `weaver resume` o tras compactación de contexto).
- [ ] **Step 4:** `go test -race ./internal/engine/ ./internal/mcpserver/` → PASS.
- [ ] **Step 5: Commit** `feat(engine,mcpserver): herramienta run_state para estado de fase y tareas`.

---

### Task 2: `DiffStat` persistido en `wait_agents` y herramienta `agent_diff` (con filtro por ruta y truncado UTF-8)

**Files:**
- Modify: `internal/gitx/git.go`, `internal/engine/engine.go`, `internal/engine/agents.go`, `internal/mcpserver/tools.go`, `internal/prompts/orchestrator.md`
- Test: `internal/gitx/git_test.go`, `internal/engine/agents_test.go`, `internal/mcpserver/server_test.go`

**Interfaces:**
- Consumes: `gitx.Repo.Run`, `Engine.store`.
- Produces:
  ```go
  // internal/gitx
  // Usan -c core.quotepath=false --no-color --no-ext-diff para salida limpia y determinista.
  func (r Repo) DiffStat(ctx context.Context, base, head string) (string, error)
  func (r Repo) Diff(ctx context.Context, base, head, path string) (string, error) // path opcional ("--", path)
  ```
  ```go
  // internal/engine
  // En AgentResult:
  // DiffStat string `json:"diff_stat,omitempty"`
  func (e *Engine) AgentDiff(ctx context.Context, agentID, path string, maxBytes int) (diff string, truncated bool, err error)
  ```
  - Persistencia: al finalizar un agente en `runAgent`, guardar `diffstat.txt` (truncado a 4096 bytes) en `agentDir`, de modo que `Wait` pueda leerlo sin ejecutar `git` bajo `e.mu.Lock()` y siga disponible tras `Cleanup` o `Resume`.
  - MCP tool `agent_diff{agent_id, path?, max_bytes?}` → `{agent_id, diff, truncated}` (default `max_bytes = 32768`, tope `65536`).

- [ ] **Step 1: Tests que fallan:**
  - `TestRepoDiffAndDiffStat` (`gitx`): crea commits tocando 2 archivos (incluyendo uno con acento/espacio en el nombre) y verifica `DiffStat`, `Diff` completo y `Diff` filtrado a un solo `path`.
  - `TestWaitAgentsIncludesDiffStatAndNoTranscript` (`engine` + `mcpserver`): al terminar el subagente con cambios en `hola.txt`, `AgentResult.DiffStat` incluye `hola.txt` (y persiste tras `Cleanup`), sin incluir nunca `TRANSCRIPT-ONLY`.
  - `TestMCPAgentDiffTruncatesUTF8AndFiltersPath` (`mcpserver`): actualiza `TestToolsListed` (`+agent_diff`); verifica truncado limpio respetando límites de runas UTF-8 (`utf8.ValidString(diff)`), `truncated: true`, `len(diff) <= max_bytes` y filtrado por `path`.
  - `TestAgentDiffAfterCleanupUsesMergedCommit` (`engine`): tras `Cleanup`, `AgentDiff` resuelve `ag.Base..ag.MergedCommit` y devuelve el parche correctamente.
- [ ] **Step 2:** `go test ./internal/gitx/ ./internal/engine/ ./internal/mcpserver/` → FAIL.
- [ ] **Step 3: Implementar.**
  - Añadir `DiffStat` y `Diff` en `internal/gitx/git.go`.
  - Guardar `diffstat.txt` en `runAgent` y leerlo en `Wait` (junto con `receipt.json`/`verification.json`, idealmente leyendo los archivos fuera de `e.mu` tras hacer snapshot de metadatos de agentes).
  - Implementar `Engine.AgentDiff` con helper `truncateUTF8(s string, maxBytes int) (string, bool)` y registrar `agent_diff` en `mcpserver/tools.go` + `orchestrator.md`.
- [ ] **Step 4:** `go test -race ./internal/gitx/ ./internal/engine/ ./internal/mcpserver/` → PASS.
- [ ] **Step 5: Commit** `feat(gitx,engine,mcpserver): diff_stat en wait_agents y herramienta agent_diff`.

---

### Task 3: Tope en código para `wait_agents` y aislamiento seguro de ramas `weaver/` en paralelo

**Files:**
- Modify: `internal/mcpserver/tools.go`, `internal/gitx/guard.go`, `internal/engine/engine.go`, `internal/engine/agents.go`
- Test: `internal/mcpserver/server_test.go`, `internal/gitx/guard_test.go`, `internal/engine/agents_test.go`

**Interfaces:**
- Consumes: `gitx.Guard`, `gitx.WeaverRefPrefix`.
- Produces:
  ```go
  // internal/mcpserver
  const maxWaitTimeout = 45 * time.Second
  func clampWaitTimeout(seconds float64) time.Duration
  ```
  ```go
  // internal/gitx
  // CheckExcept verifica todas las refs (incluidas refs/heads/weaver/*) excepto las listadas en allowedRefs.
  // Check(ctx) mantiene su comportamiento previo (ignora todo WeaverRefPrefix) para compatibilidad directa.
  func (g *Guard) CheckExcept(ctx context.Context, allowedRefs ...string) ([]Violation, error)
  ```
  - **Reglas de allowlist y baseline en `Engine` (crítico para concurrencia):**
    1. Tras operaciones legítimas del engine que crean/mueven/borran refs `weaver/*` (`StartRun`, `Spawn` tras `AddWorktree`, `Cleanup` tras `DeleteBranch`), llamar a `e.guard.Rebase(ctx)` para actualizar el snapshot base.
    2. Helper `e.activeWeaverRefsLocked(extra ...string) []string`: devuelve `refs/heads/<branch>` de **todos** los agentes en estado `queued` o `running`, más cualquier ref en `extra`.
    3. Al finalizar un agente `ag` en `runAgent`: llamar `e.guard.CheckExcept(ctx, e.activeWeaverRefsLocked("refs/heads/"+ag.Branch)...)` → protege ramas del usuario, tags, `IntegrationBranch` **y ramas de otros agentes ya terminados**, sin interferir jamás con otro agente que todavía esté corriendo en paralelo.
    4. En `Engine.CheckRefs(ctx)` (turno del orquestador): llamar `e.guard.CheckExcept(ctx, e.activeWeaverRefsLocked("refs/heads/"+e.state.IntegrationBranch)...)` → el orquestador puede avanzar `IntegrationBranch` haciendo merges, pero no ramas de agentes ya cerrados ni del usuario.

- [ ] **Step 1: Tests que fallan:**
  - `TestClampWaitTimeout` (`mcpserver`): `0` → `45s`, `-5` → `45s`, `10` → `10s`, `300` → `45s`.
  - `TestGuardCheckExceptProtectsOtherWeaverBranches` (`gitx`): con `refs/heads/weaver/run1` y `refs/heads/weaver/run1-a1` existentes, `CheckExcept(ctx, "refs/heads/weaver/run1-a2")` detecta y restaura una mutación sobre `weaver/run1` o `weaver/run1-a1`, mientras permite cambios en `weaver/run1-a2`.
  - `TestAgentCannotMoveIntegrationBranch` (`engine`): un agente falso que ejecuta `git branch -f weaver/<run-id> HEAD` falla verificación RDD con `ref_violations` no vacío y la rama de integración queda restaurada a su SHA anterior.
  - `TestParallelAgentsDoNotTriggerFalseRefViolations` (`engine`): con `max_parallel = 2`, dos agentes (`T1` y `T2`) corren simultáneamente, ambos commitean archivos en sus ramas y terminan en orden escalonado → **0 `ref_violations`**, ambos commits intactos y ambas verificaciones `Passed == true`.
  - `TestCleanupDoesNotTriggerDeletedViolation` (`engine`): `Cleanup` borra rama de `a1`; un posterior `CheckRefs` no reporta `deleted` ni resucita la rama.
- [ ] **Step 2:** `go test ./internal/mcpserver/ ./internal/gitx/ ./internal/engine/` → FAIL.
- [ ] **Step 3: Implementar** `clampWaitTimeout`, `Guard.CheckExcept`, `activeWeaverRefsLocked` y llamadas a `Rebase` en `StartRun`/`Spawn`/`Cleanup`.
- [ ] **Step 4:** `go test -race ./internal/mcpserver/ ./internal/gitx/ ./internal/engine/` → PASS.
- [ ] **Step 5: Commit** `fix(mcpserver,gitx,engine): acotar wait_agents y aislar ramas weaver/ sin romper paralelismo`.

---

### Task 4: Recuperación de worktree en `Resume`, limpieza de cola cancelada y multi-run continuo

**Files:**
- Modify: `internal/gitx/git.go`, `internal/engine/engine.go`, `internal/engine/agents.go`, `internal/engine/approval.go`
- Test: `internal/gitx/git_test.go`, `internal/engine/engine_test.go`, `internal/engine/agents_test.go`, `internal/engine/integrate_test.go`

**Interfaces:**
- Consumes: `e.repo`, `e.store`, `e.guard`.
- Produces:
  ```go
  // internal/gitx
  func (r Repo) AttachWorktree(ctx context.Context, path, existingBranch string) error // git worktree add <path> <existingBranch>
  ```
  - **En `Engine.Resume(ctx, runID)`:**
    - Si `st.SessionDir` no existe en disco, comprueba si `st.IntegrationBranch` existe (`RevParse`):
      - Si existe → `e.repo.AttachWorktree(ctx, st.SessionDir, st.IntegrationBranch)` + `e.guard.Rebase(ctx)`.
      - Si no existe → error accionable indicando que la rama `weaver/<run-id>` ya no existe.
  - **En `Engine.Spawn` / `runAgent`:**
    - Si se solicita un proveedor desconocido/no configurado, el error enumera los proveedores disponibles.
    - Si `runAgent` es cancelado mientras esperaba el semáforo (`queued`, antes de lanzar ningún proceso), elimina el worktree vacío y su rama `weaver/<run>-<agent>` + `e.guard.Rebase(ctx)`. (Si ya había arrancado `running`, se mantiene la regla del spec §5.3: conservar el worktree para inspección).
  - **Multi-run en una misma sesión (§5.3.2):**
    - En `Engine.RequestApproval(ctx, r)`: si `r.Kind == "spec"` y `e.state.Phase == run.PhaseIntegrated`, abrir automáticamente la siguiente ejecución llamando internamente a `StartRun` preservando `OrchestratorSessionID` del run anterior y notificando el nuevo progreso a la TUI (sin romper `TestNextRunAfterIntegration` que llama explícitamente a `StartRun`).

- [ ] **Step 1: Tests que fallan:**
  - `TestResumeRecreatesMissingSessionWorktree`: borra `st.SessionDir` manteniendo `st.IntegrationBranch`, llama `Resume`, verifica que `SessionDir` vuelve a existir en el mismo commit.
  - `TestSpawnCancelledWhileQueuedCleansEmptyWorktree`: con `max_parallel = 1`, `a2` queda `queued`, se llama `Cancel("a2")` antes de liberar `a1`, y tras esperar a `a2` su worktree y rama no existen; `a1` termina normalmente sin violaciones de refs.
  - `TestSpawnUnknownProviderListsAvailable`: pedir `provider: "inexistente"` devuelve error que nombra los proveedores configurados.
  - `TestSecondSpecAfterIntegrationStartsNewRun`: tras integrar un run (`Phase == PhaseIntegrated`), llamar `RequestApproval(spec)` abre un nuevo `RunID` con `Phase == PhaseSpecApproved`, reutiliza `SessionDir` y conserva `OrchestratorSessionID`.
- [ ] **Step 2:** `go test ./internal/gitx/ ./internal/engine/` → FAIL.
- [ ] **Step 3: Implementar** `AttachWorktree`, recuperación en `Resume`, limpieza de cola cancelada en `runAgent`, y transición multi-run en `approval.go`.
- [ ] **Step 4:** `go test -race ./internal/gitx/ ./internal/engine/` → PASS.
- [ ] **Step 5: Commit** `feat(engine,gitx): recuperar worktree en resume, limpiar cola cancelada y soporte multi-run`.
