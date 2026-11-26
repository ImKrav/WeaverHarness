# WeaverHarness v1 — Plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** CLI `weaver` en Go con TUI de chat donde un orquestador Claude (Opus) diseña con SDD y delega tareas a subagentes de Claude/Codex/Antigravity/OpenCode en worktrees propios, con recibos verificados por weaver (RDD).

**Architecture:** Un solo proceso: TUI (Bubble Tea) ⇄ orquestador `claude -p` en stream-json; el orquestador llama herramientas de un servidor MCP HTTP local que delega en un `engine` (estado de la ejecución, reglas de fase, agentes, verificación, guardia de refs). Cada proveedor es un adaptador que lanza su CLI oficial como proceso hijo y normaliza su salida JSONL.

**Tech Stack:** Go 1.27 · Bubble Tea + Bubbles + Lip Gloss (charmbracelet, última versión estable) · `github.com/modelcontextprotocol/go-sdk` · `github.com/BurntSushi/toml` · `golang.org/x/sys` · git (binario).

**Spec:** `docs/superpowers/specs/2026-11-26-weaverharness-design.md`

## Global Constraints

- Módulo `github.com/ImKrav/weaverharness`; binario `weaver` en `cmd/weaver`; Go 1.27.
- Nunca leer, guardar ni reenviar credenciales de proveedores ni llamar a sus backends: solo lanzar sus binarios oficiales sin modificar. Nunca pasar `--bare` a `claude`.
- Debe compilar y pasar tests en Windows (principal), macOS y Linux.
- Dependencias permitidas: las del Tech Stack. Cualquier otra exige justificación en el commit.
- v1: solo `claude` puede ser orquestador; dos niveles (los subagentes no reciben MCP).
- Valores por defecto: `review_rounds = 2`, `check_timeout = 10m`, `task_timeout = 30m`, `max_parallel = 3` por proveedor, `summary` ≤ 150 palabras, cola de salida de checks = últimos 2000 bytes.
- Ramas: integración `weaver/<run-id>`, agente `weaver/<run-id>/<agent-id>`. Worktrees de agente: `<padre-del-repo>/<repo>-weaver/<run-id>/<agent-id>`. Worktree de sesión (orquestador): `<padre-del-repo>/<repo>-weaver/session-<run-id-inicial>`.
- Estado: `<repo>/.weaver/runs/<run-id>/state.json`, `agents/<id>/{transcript.jsonl,receipt.json,verification.json}`.
- Checks se ejecutan con `cmd /c <cmd>` en Windows y `sh -c <cmd>` en el resto, con cwd = worktree.
- Clases de error: `not_installed`, `not_logged_in`, `quota_exhausted`, `model_unknown`, `timeout`, `empty_output`, `crashed`.
- Textos visibles al usuario en español.
- Prompts largos nunca por argumento de línea de comandos (límite de ~32K y escapado de `.cmd` en Windows): por stdin o archivo.

## Review Focus

1. **Prompt con `& | % " ^`, saltos de línea y > 40 000 caracteres en Windows** → llega intacto al CLI (por stdin/archivo, nunca por argv). Test en Task 7.
2. **Línea JSONL de > 1 MB y finales de línea CRLF** → se parsea sin error de buffer y sin `\r` residual. Test en Task 7.
3. **El agente escribe la plantilla del recibo antes del recibo real (dos bloques `weaver-receipt`)** → gana el último bloque válido. Test en Task 4.
4. **weaver muere a mitad de una ejecución y se reanuda** → agentes `running`/`queued` quedan `interrupted`, sus tareas vuelven a `pending`, nada se pierde. Test en Task 12.
5. **Ctrl+C mientras el orquestador está bloqueado en `wait_agents` o `request_approval`** → ambos se desbloquean por cancelación de contexto y los árboles de procesos mueren. Tests en Tasks 12 y 13.

---

### Task 1: Esqueleto y configuración

**Files:**
- Create: `go.mod`, `cmd/weaver/main.go`, `internal/config/config.go`, `internal/config/default.toml`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  ```go
  package config
  type Duration struct{ time.Duration }              // UnmarshalText con time.ParseDuration
  type Role struct{ Provider, Model string }
  type ProviderCfg struct{ Bin string `toml:"bin"`; MaxParallel int `toml:"max_parallel"` }
  type Config struct {
      Orchestrator string                 `toml:"orchestrator"`
      ReviewRounds int                    `toml:"review_rounds"`
      CheckTimeout Duration               `toml:"check_timeout"`
      TaskTimeout  Duration               `toml:"task_timeout"`
      Roles        map[string]Role        `toml:"roles"`
      Providers    map[string]ProviderCfg `toml:"providers"`
  }
  var ProviderNames = []string{"claude", "codex", "antigravity", "opencode"}
  func Default() Config                                  // decodifica default.toml embebido
  func GlobalPath() (string, error)                      // os.UserConfigDir()/weaver/config.toml
  func Load(globalPath, projectPath string) (Config, error)
  ```

- [ ] **Step 1:** `go mod init github.com/ImKrav/weaverharness`; `cmd/weaver/main.go` con `main()` que imprime `weaver: sin implementar` y sale 0.
- [ ] **Step 2: Tests que fallan** en `config_test.go`:
  - `TestDefault`: `Orchestrator=="architect"`, `Roles["architect"]=={claude,opus}`, `Roles["implementer"]=={antigravity,gemini-4-hydrogen}`, `Roles["reviewer"]=={codex,gpt-5.6-sol}`, `ReviewRounds==2`, `CheckTimeout==10m`, `TaskTimeout==30m`, `Providers[p].MaxParallel==3` para los 4.
  - `TestLoadMerge`: global define `[roles.implementer] provider="codex" model="x"`; proyecto define `review_rounds=5`. Resultado: implementer = codex/x, review_rounds 5, architect intacto.
  - `TestLoadMissingFiles`: rutas inexistentes → `Default()` sin error.
  - `TestLoadRejects`: proveedor desconocido en un rol → error que nombra el rol; `orchestrator` apuntando a rol inexistente → error; rol orquestador con proveedor ≠ `claude` → error `"v1: solo claude puede ser orquestador"`.
- [ ] **Step 3:** `go test ./internal/config/` → FAIL.
- [ ] **Step 4: Implementar.** `Load` decodifica `default.toml`, luego global, luego proyecto, sobre la misma `Config` con `toml.DecodeFile` (un rol redefinido reemplaza el rol entero) y valida al final.
- [ ] **Step 5:** `go test ./internal/config/` → PASS.
- [ ] **Step 6: Commit** `feat(config): esqueleto del módulo y carga de configuración`.

### Task 2: Procesos con árbol completo

**Files:**
- Create: `internal/procx/procx.go`, `internal/procx/procx_windows.go`, `internal/procx/procx_unix.go`
- Test: `internal/procx/procx_test.go`

**Interfaces:**
- Produces:
  ```go
  package procx
  type Proc struct{ Cmd *exec.Cmd /* + campos privados */ }
  func Start(cmd *exec.Cmd) (*Proc, error)   // Windows: Job Object con KILL_ON_JOB_CLOSE; Unix: Setpgid
  func (p *Proc) Kill() error                // mata el árbol entero; idempotente
  func (p *Proc) Wait() error
  ```

- [ ] **Step 1: Test que falla** `TestKillTree`: el test se re-ejecuta (`os.Args[0]` con env `PROCX_ROLE=parent|child`); el padre lanza un hijo que duerme 60 s y escribe su PID en un archivo; `Kill()` del padre; en ≤ 5 s el PID del hijo ya no está vivo (helper `alive(pid int) bool` por SO en el test).
- [ ] **Step 2:** `go test ./internal/procx/` → FAIL.
- [ ] **Step 3: Implementar** con `golang.org/x/sys/windows` (`CreateJobObject`, `AssignProcessToJobObject`, `TerminateJobObject`) y `syscall.SysProcAttr{Setpgid:true}` + `syscall.Kill(-pgid, SIGKILL)` en Unix.
- [ ] **Step 4:** PASS en el SO local; `GOOS=linux go vet ./internal/procx/` y `GOOS=darwin go vet ./internal/procx/` sin errores.
- [ ] **Step 5: Commit** `feat(procx): lanzar y matar árboles de procesos`.

### Task 3: Git, worktrees y guardia de refs

**Files:**
- Create: `internal/gitx/git.go`, `internal/gitx/guard.go`
- Test: `internal/gitx/git_test.go`, `internal/gitx/guard_test.go`

**Interfaces:**
- Produces:
  ```go
  package gitx
  var ErrNotGit = errors.New("no es un repositorio git")
  type Repo struct{ Dir string }
  func Open(dir string) (Repo, error)                         // Dir = git rev-parse --show-toplevel
  func (r Repo) Run(ctx context.Context, args ...string) (string, error) // git -C Dir …, stdout recortado; error incluye stderr
  func (r Repo) CurrentBranch(ctx) (string, error)
  func (r Repo) IsDirty(ctx) (bool, error)
  func (r Repo) CommonDir(ctx) (string, error)                // absoluto
  func (r Repo) RevParse(ctx, ref string) (string, error)
  func (r Repo) AddWorktree(ctx, path, newBranch, base string) error
  func (r Repo) RemoveWorktree(ctx, path string) error        // --force
  func (r Repo) DeleteBranch(ctx, name string) error          // -D
  func (r Repo) SwitchNew(ctx, branch, base string) error     // git switch -c
  func (r Repo) CommitAll(ctx, msg string) (bool, error)      // add -A + commit si hay cambios
  func (r Repo) IsAncestor(ctx, a, b string) (bool, error)
  func (r Repo) DiffNames(ctx, base, head string) ([]string, error) // rutas con '/'
  func (r Repo) MergeFFOnly(ctx, ref string) error
  func WeaverDir(repoTop string) string                       // <padre>/<repo>-weaver
  const WeaverRefPrefix = "refs/heads/weaver/"

  type Snapshot struct{ Refs map[string]string; Heads map[string]string } // Heads: worktree → HEAD
  type Violation struct{ Kind, Ref, Before, After string; Restored bool }  // Kind: moved|deleted|created|head_changed|remote_changed
  type Guard struct{ /* repo, base Snapshot, mu */ }
  func NewGuard(ctx, r Repo) (*Guard, error)
  func (g *Guard) Check(ctx) ([]Violation, error) // compara con la base; restaura; re-snapshot como nueva base
  func (g *Guard) Rebase(ctx) error               // nueva base tras cambios legítimos de weaver
  ```
- Reglas de `Check`: se ignora todo bajo `WeaverRefPrefix` y los HEAD de worktrees bajo `WeaverDir`. `refs/heads/*` y `refs/tags/*` movidas o borradas → `git update-ref` al valor anterior, `Restored=true`. Refs nuevas fuera de weaver → `created`, solo se reportan. `refs/remotes/*` cambiadas → `remote_changed`, solo se reportan. HEAD de otro worktree cambiado → `head_changed`, solo se reporta. Comentario en el código: `// shortcut: no detecta que un agente mueva otra rama weaver/ ni la de integración; atribuir por agente si hace falta`.

- [ ] **Step 1: Tests que fallan.** Todos con un helper `newRepo(t)` que crea un repo temporal **en un directorio con un espacio en el nombre**, con identidad de git local y un commit inicial:
  - `TestOpenNotGit` → `ErrNotGit`.
  - `TestWorktreeCycle`: `AddWorktree` en `WeaverDir/…/a1` con la rama `weaver/r1/a1`; archivo nuevo; `CommitAll` → true; `DiffNames(main, weaver/r1/a1) == ["nuevo.txt"]`; `IsAncestor(main, weaver/r1/a1)` → true; `RemoveWorktree` + `DeleteBranch` → la rama ya no existe.
  - `TestCommitAllClean` → false, sin commit.
  - `TestMergeFFOnly`: avanza → ok; ramas divergentes → error.
  - `TestGuardRestoresUserBranch`: `NewGuard`; `git branch -f main HEAD~0` hacia otro commit; `Check` → 1 `moved` con `Restored=true`, `main` vuelve a su SHA; un segundo `Check` → vacío.
  - `TestGuardIgnoresWeaverRefs`: crear/mover `weaver/r1/a1` → `Check` vacío.
  - `TestGuardReportsCreatedAndRemote`: rama `foo` nueva → `created`, no se borra; `update-ref refs/remotes/origin/main` → `remote_changed`.
- [ ] **Step 2:** `go test ./internal/gitx/` → FAIL.
- [ ] **Step 3: Implementar.** Snapshot con `git for-each-ref --format=%(refname) %(objectname)` y `git worktree list --porcelain`.
- [ ] **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(gitx): worktrees y guardia de refs`.

### Task 4: Recibos

**Files:**
- Create: `internal/receipt/receipt.go`
- Test: `internal/receipt/receipt_test.go`

**Interfaces:**
- Produces:
  ```go
  package receipt
  type Change struct{ Path string `json:"path"`; Kind string `json:"kind"` }
  type Check struct{ Cmd string `json:"cmd"`; ExitCode int `json:"exit_code"`; OutputTail string `json:"output_tail"` }
  type Receipt struct {
      TaskID       string   `json:"task_id"`
      Status       string   `json:"status"`
      Summary      string   `json:"summary"`
      SpecRefs     []string `json:"spec_refs"`
      Changes      []Change `json:"changes"`
      Commits      []string `json:"commits"`
      Verification []Check  `json:"verification"`
      OpenIssues   []string `json:"open_issues"`
  }
  var ErrNoReceipt = errors.New("no hay bloque weaver-receipt")
  func Extract(text string) (Receipt, error)     // último bloque ```weaver-receipt … ``` que parsea como JSON
  func (r Receipt) Validate(taskID string) error
  ```
- `Validate`: `task_id == taskID`; `status` ∈ {done, blocked, failed}; `summary` no vacío y ≤ 150 palabras (`strings.Fields`); cada `verification[i].cmd` no vacío; cada `changes[i].kind` ∈ {added, modified, deleted}.

- [ ] **Step 1: Tests que fallan:** `TestExtractSingle`; `TestExtractLastValidWins` (primero un bloque con la plantilla literal `"status": "done | blocked | failed"` que no valida, luego uno real → gana el real, y si el último no parsea, gana el anterior que sí parsea); `TestExtractNone` → `ErrNoReceipt`; `TestExtractCRLF`; `TestValidate` con un caso por regla (151 palabras → error que menciona `150`).
- [ ] **Step 2:** FAIL. **Step 3:** implementar con regex `(?s)` + `json.Unmarshal`, recorriendo de atrás hacia adelante. **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(receipt): extracción y validación de recibos`.

### Task 5: Verificación RDD

**Files:**
- Create: `internal/verify/verify.go`, `internal/verify/shell_windows.go`, `internal/verify/shell_unix.go`
- Test: `internal/verify/verify_test.go`

**Interfaces:**
- Consumes: `gitx.Repo`, `receipt.Receipt`, `gitx.Violation`.
- Produces:
  ```go
  package verify
  type CheckResult struct{ Cmd, Source string; ExitCode int; Claimed *int; Passed, TimedOut bool; OutputTail string } // Source: "task"|"receipt"
  type Verification struct {
      CommitsMissing []string; OnlyInReceipt, OnlyInDiff []string
      Checks []CheckResult; RefViolations []gitx.Violation; Passed bool
  }
  type Input struct{ Worktree gitx.Repo; Base, Branch string; Receipt receipt.Receipt; TaskChecks []string; Timeout time.Duration }
  func Verify(ctx context.Context, in Input) Verification
  func RunCheck(ctx context.Context, dir, cmd string, timeout time.Duration) (exit int, tail string, timedOut bool)
  func (v *Verification) Finalize() // Passed = sin commits faltantes && todos los checks Passed && sin RefViolations
  ```
- Reglas: commit "existe" si `IsAncestor(sha, Branch)`; `OnlyInReceipt`/`OnlyInDiff` comparan rutas del recibo contra `DiffNames(Base, Branch)` y solo se reportan, no afectan `Passed`. Un check de tarea pasa si sale con código 0. Un check del recibo pasa si su código de salida es igual al declarado. Si un comando del recibo es idéntico a un check de tarea, corre una vez como `task`. Timeout → `TimedOut=true`, `Passed=false`. `tail` = últimos 2000 bytes de stdout+stderr.

- [ ] **Step 1: Tests que fallan** con un repo temporal y un worktree de agente: `TestVerifyAllGood`; `TestVerifyMissingCommit`; `TestVerifyChangesMismatchReportedOnly` (Passed sigue true); `TestVerifyClaimMismatch` (el recibo dice 0 y el comando sale con 1 → falla); `TestVerifyDedup`; `TestRunCheckTimeout` (comando que duerme 5 s con timeout de 200 ms → `TimedOut`, termina en < 2 s); `TestRunCheckTail` (salida de 10 KB → tail de 2000 bytes).
- [ ] **Step 2:** FAIL. **Step 3:** implementar; `RunCheck` usa `procx` para matar el árbol al vencer el timeout. **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(verify): re-ejecución de checks y verificación de recibos`.

### Task 6: Estado de la ejecución y reglas de fase

**Files:**
- Create: `internal/run/state.go`, `internal/run/rules.go`, `internal/run/store.go`
- Test: `internal/run/rules_test.go`, `internal/run/store_test.go`

**Interfaces:**
- Consumes: `config.Role`.
- Produces:
  ```go
  package run
  type Phase string      // "open" | "spec_approved" | "plan_approved" | "integrated"
  type TaskStatus string // "pending" | "running" | "receipt_ok" | "receipt_failed" | "accepted"
  type AgentStatus string // "queued" | "running" | "finished" | "failed" | "cancelled" | "timeout" | "interrupted"
  type Task struct{ ID, Title, Role string; SpecRefs, DependsOn, Checks []string; Status TaskStatus; Rounds int; AcceptedAgent string }
  type Plan struct{ Tasks []*Task; GlobalChecks []string }
  type Agent struct{ ID, TaskID, Role, Provider, Model, Branch, Base, Worktree, SessionID, Err string; Status AgentStatus; StartedAt, EndedAt time.Time }
  type Approval struct{ Kind, Path, Summary, Comment string; Approved bool; At time.Time }
  type State struct {
      RunID, BaseBranch, IntegrationBranch, SessionDir, OrchestratorSessionID string
      Phase Phase; Plan *Plan; Approvals []Approval; Agents []*Agent; NextAgent int
  }
  const IntegrateTask = "integrate"
  var ErrSpecNotApproved, ErrPlanNotApproved, ErrTasksNotAccepted, ErrUnknownTask, ErrDepsNotMerged, ErrTaskBusy, ErrRoundsExhausted error
  func NewRunID(t time.Time) string                   // "20060102-150405"
  func ValidatePlan(p Plan, roles map[string]config.Role) error
  func (s *State) CheckRequest(kind string) error      // spec: siempre; plan: Phase ≥ spec_approved; integration: Phase == plan_approved y todas las tareas accepted
  func (s *State) Record(a Approval, p *Plan)          // si se aprueba, avanza Phase y guarda el plan
  func (s *State) CheckSpawn(taskID string, merged func(agentID string) bool) error
  func (s *State) Task(id string) *Task
  func (s *State) Agent(id string) *Agent
  func (s *State) NewAgentID() string                  // "a1", "a2", …
  func (s *State) MarkInterrupted()                    // queued/running → interrupted; su tarea → pending
  type Store struct{ Root string }                     // <repo>/.weaver/runs
  func (st Store) Save(s *State) error                 // escritura atómica: tmp + os.Rename
  func (st Store) Load(runID string) (*State, error)
  func (st Store) Latest() (*State, error)
  func (st Store) List() ([]*State, error)             // ordenados por RunID
  func (st Store) Delete(runID string) error
  func (st Store) AgentDir(runID, agentID string) string
  ```
- `ValidatePlan`: ≥ 1 tarea; IDs únicos y distintos de `integrate`; `depends_on` existentes y sin ciclos; `role` existe en `roles`; cada tarea con ≥ 1 check; `GlobalChecks` con ≥ 1 comando.
- `CheckSpawn`: exige plan aprobado; `integrate` siempre está permitido; las demás tareas deben existir, no estar `running` ni `accepted`, y tener todas sus dependencias `accepted` con `merged(AcceptedAgent)` true (si no, `ErrDepsNotMerged`).

- [ ] **Step 1: Tests que fallan** (tabla por regla): orden de aprobaciones; `CheckRequest("integration")` con una tarea `receipt_ok` → `ErrTasksNotAccepted`; `ValidatePlan` con ciclo, dependencia inexistente, rol inexistente, tarea sin checks y plan sin global checks; `CheckSpawn` antes del plan, con tarea desconocida, con dependencia aceptada pero sin merge, con `integrate`; `MarkInterrupted`; `TestStoreRoundTrip`, `Latest`, `Delete`.
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(run): estado, reglas de fase y persistencia`.

### Task 7: Núcleo de proveedores, CLI falso y adaptador de Claude

**Files:**
- Create: `internal/provider/provider.go`, `internal/provider/jsonl.go`, `internal/provider/classify.go`, `internal/provider/claude.go`
- Create: `internal/provider/testdata/fakecli/main.go`, `internal/provider/testdata/claude_ok.jsonl`, `internal/provider/testdata/claude_quota.jsonl`
- Test: `internal/provider/main_test.go`, `internal/provider/claude_test.go`, `internal/provider/classify_test.go`

**Interfaces:**
- Consumes: `procx`.
- Produces:
  ```go
  package provider
  type ErrClass string // constantes: NotInstalled, NotLoggedIn, QuotaExhausted, ModelUnknown, Timeout, EmptyOutput, Crashed
  type ProviderError struct{ Class ErrClass; Msg string } // implementa error
  type EventKind string // "text" | "tool" | "turn_end" | "error" | "raw"
  type Event struct{ Kind EventKind; Text, Tool string; Raw []byte }
  type Final struct{ Text, SessionID string; Err *ProviderError }
  type Mode int // Worker | Orchestrator
  type RunRequest struct {
      Mode Mode; Dir, Model, Prompt, SystemPromptFile string
      SessionID, ResumeID, MCPConfigFile string; AllowedTools []string
  }
  type Health struct{ Installed, LoggedIn bool; Version, Detail string; Models []string }
  type Provider interface {
      Name() string
      Check(ctx context.Context) Health
      Start(ctx context.Context, req RunRequest) (Run, error)
  }
  type Run interface {
      Events() <-chan Event   // se cierra al terminar
      Send(text string) error // solo Orchestrator
      SessionID() string
      Wait() Final
      Kill() error
  }
  func New(name, bin string) (Provider, error) // bin "" → nombre por defecto (claude, codex, agy, opencode)
  func Classify(text string) ErrClass          // sin coincidencias → Crashed
  ```
- `jsonl.go`: un helper privado que lanza con `procx`, lee stdout con `bufio.Reader.ReadBytes('\n')` (sin límite de tamaño de línea), recorta `\r\n`, emite un `Event{Kind:"raw"}` por línea más los eventos que devuelva la función de parseo del adaptador, y guarda los últimos 4 KB de stderr. Si no hubo ninguna línea en stdout, devuelve `EmptyOutput` aunque el código de salida sea 0. Si sale con código ≠ 0 sin final, aplica `Classify(stderr)`. Si `exec.LookPath` falla, devuelve `NotInstalled`.
- `Classify` (sin distinguir mayúsculas): `usage limit|rate limit|quota|exhausted|429` → QuotaExhausted; `not logged in|log in|login required|unauthorized|authenticate` → NotLoggedIn; `unknown model|model not found|invalid model` → ModelUnknown.
- Argumentos de Claude:
  - Worker: `-p --output-format stream-json --verbose --model <m> --dangerously-skip-permissions --append-system-prompt-file <f>`, más `--session-id <id>` o `--resume <id>`. El prompt va por stdin y luego se cierra stdin.
  - Orchestrator: `-p --input-format stream-json --output-format stream-json --verbose --model <m> --mcp-config <file> --strict-mcp-config --append-system-prompt-file <f> --permission-mode dontAsk --allowedTools <AllowedTools…>`, más `--session-id` o `--resume`. `Send` escribe `{"type":"user","message":{"role":"user","content":<text>}}\n`. Cada evento `result` emite `turn_end`.
- Parseo de Claude: `system/init` → session id. `assistant` → un `text` por bloque de texto y un `tool` (con el nombre) por `tool_use`. `result` → `Final.Text = result`; si `is_error`, `Final.Err = Classify(result)`.
- CLI falso (`testdata/fakecli`, compilado en `TestMain` con `go build`): lee las variables de entorno `FAKECLI_FIXTURE` (archivo que reproduce por stdout), `FAKECLI_ARGS` (archivo donde escribe sus args en JSON), `FAKECLI_STDIN` (archivo donde copia lo que recibe por stdin), `FAKECLI_EXIT`, `FAKECLI_STDERR`, `FAKECLI_SLEEP` y `FAKECLI_ECHO=1`. Con `FAKECLI_ECHO=1`, por cada línea de stdin responde un `assistant` con `"eco: <content>"` y un `result`.

- [ ] **Step 1: Fixtures a mano** (`claude_ok.jsonl`):
  ```
  {"type":"system","subtype":"init","session_id":"s-1","model":"opus"}
  {"type":"assistant","message":{"content":[{"type":"text","text":"Hola"}]},"session_id":"s-1"}
  {"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]},"session_id":"s-1"}
  {"type":"result","subtype":"success","is_error":false,"result":"Listo","session_id":"s-1"}
  ```
  `claude_quota.jsonl`: init + `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Claude AI usage limit reached","session_id":"s-1"}`.
- [ ] **Step 2: Tests que fallan:**
  - `TestClaudeWorkerArgs`: args exactos según lo anterior y nunca `--bare`.
  - `TestClaudeWorkerEvents`: `text "Hola"`, `tool "Bash"`, `Final{Text:"Listo", SessionID:"s-1"}`.
  - `TestClaudeQuota` → `QuotaExhausted`.
  - `TestEmptyOutputExit0` → `EmptyOutput`.
  - `TestNotInstalled` → `NotInstalled`.
  - `TestHugeLineAndCRLF`: línea `assistant` con 1,5 MB de texto y CRLF.
  - `TestPromptViaStdin`: prompt con `& | % " ^`, `\n` y 40 000 caracteres → `FAKECLI_STDIN` idéntico y `FAKECLI_ARGS` sin el prompt.
  - `TestOrchestratorSend` (`FAKECLI_ECHO=1`): dos `Send` → dos `turn_end`, con `eco: …` correcto.
  - `TestClassify`: un caso por clase.
- [ ] **Step 3:** `go test ./internal/provider/` → FAIL. **Step 4:** implementar. **Step 5:** PASS.
- [ ] **Step 6: `Check` de Claude:** `claude --version` → Version; `claude auth status` → LoggedIn según el código de salida, con la salida en Detail. Sin test unitario; se valida en Task 17.
- [ ] **Step 7: Commit** `feat(provider): núcleo de adaptadores y adaptador de Claude`.

### Task 8: Adaptador de Codex

**Files:**
- Create: `internal/provider/codex.go`, `internal/provider/testdata/codex_ok.jsonl`, `internal/provider/testdata/codex_fail.jsonl`
- Test: `internal/provider/codex_test.go`

**Interfaces:** implementa `Provider` (Task 7). `Orchestrator` → error `"v1: solo claude puede ser orquestador"`.

- [ ] **Step 1: Capturar fixtures reales.** En un repo git temporal: `echo "Responde solo OK" | codex exec --json -m gpt-5.6-sol -s workspace-write -c approval_policy="never" -` > `codex_ok.jsonl`. Para `codex_fail.jsonl`, usa `-m modelo-inexistente`. Anota en el encabezado del test los tipos de evento observados. Lo esperado: `thread.started{thread_id}`, `item.completed{item{type:"agent_message",text}}`, `turn.completed`, `turn.failed{error.message}` / `error{message}`. Si difieren, manda la captura.
- [ ] **Step 2: Tests que fallan:**
  - `TestCodexArgs`: `exec --json -m <m> -C <dir> -s workspace-write -c approval_policy="never" -`.
  - `TestCodexResumeArgs`: `exec resume <id> …`, con los flags que acepte `codex exec resume --help`.
  - `TestCodexEvents`: `Final.Text` = último `agent_message`; SessionID = `thread_id`.
  - `TestCodexFail` → `ModelUnknown`.
  - `TestCodexSystemPrompt`: el contenido de `SystemPromptFile` va antes del prompt, en stdin.
- [ ] **Step 3:** FAIL. **Step 4:** implementar. **Step 5:** PASS.
- [ ] **Step 6:** `Check`: `codex --version`, y `codex login status` → LoggedIn.
- [ ] **Step 7: Commit** `feat(provider): adaptador de Codex`.

### Task 9: Adaptador de OpenCode

**Files:**
- Create: `internal/provider/opencode.go`, `internal/provider/testdata/opencode_ok.jsonl`
- Test: `internal/provider/opencode_test.go`

**Interfaces:** implementa `Provider`; `Orchestrator` → mismo error que Codex. OpenCode acepta un `--session <id>` propio (lo crea si no existe), así que `SessionID` = el id que genera weaver.

- [ ] **Step 1: Capturar fixtures reales.** `opencode run --format json -m <provider/model> --auto --session weaver-test-1` con el mensaje por stdin; si no lee stdin, por argumento. Anota en el test qué funcionó. Si solo acepta el prompt por argumento, el adaptador escribe el prompt completo en `<agentDir>/prompt.md` y pasa `-f <archivo>` con un mensaje corto que lo referencia (así se respeta la restricción de no usar argv).
- [ ] **Step 2: Tests que fallan:** `TestOpenCodeArgs`; `TestOpenCodeEvents` (Final.Text = el texto del último mensaje del asistente, según la captura); `TestOpenCodeLongPrompt`, con el mismo prompt de 40 000 caracteres de Task 7 → llega intacto.
- [ ] **Step 3:** FAIL. **Step 4:** implementar. **Step 5:** PASS.
- [ ] **Step 6:** `Check`: `opencode --version`, y `opencode auth list` → LoggedIn si lista al menos un proveedor; `opencode models` → Models.
- [ ] **Step 7: Commit** `feat(provider): adaptador de OpenCode`.

### Task 10: Spike de Antigravity (`agy`) en Windows — desechable

**Files:**
- Create: `docs/superpowers/notes/2026-11-26-agy-spike.md`

- [ ] **Step 1:** En un repo temporal, prueba estas tres vías con un prompt trivial que cree un archivo y termine con un bloque `weaver-receipt`. Para cada una, anota si da salida por pipe, cómo sale el id de conversación y qué pasa con la cuota agotada (si se puede provocar) y con un modelo inválido:
  1. `agy -p --output-format stream-json --model <m> --dangerously-skip-permissions --sandbox`, con stdin cerrado.
  2. `agy acp`: stdio JSON-RPC; inicializar, abrir sesión, mandar el prompt y leer la respuesta.
  3. La vía 1, pero leyendo `transcript.jsonl` en `~/.gemini/antigravity-cli/brain/…`.
- [ ] **Step 2:** Escribe en la nota la vía elegida, el formato de eventos observado (con un fragmento real) y cómo detectar "vacío = fallo". Si ninguna es fiable, escribe `RESULTADO: sin vía fiable`.
- [ ] **Step 3: Commit** `docs: resultado del spike de agy`.

### Task 11: Adaptador de Antigravity (según el spike)

**Files:**
- Create: `internal/provider/agy.go`, `internal/provider/testdata/agy_*.jsonl`
- Modify (solo si el spike dio `sin vía fiable`): `internal/config/default.toml`
- Test: `internal/provider/agy_test.go`

**Interfaces:** implementa `Provider`; `Orchestrator` → error.

- [ ] **Si el spike encontró una vía:**
  - Fixtures a partir de las capturas del spike.
  - Tests: `TestAgyArgs`, `TestAgyEvents`, `TestAgyEmptyIsFailure` (stdout vacío y exit 0 → `EmptyOutput`), `TestAgyResumeArgs` (`--conversation <id>`).
  - FAIL → implementar → PASS.
  - `Check`: `agy --version` y `agy models` (si falla con un error de autenticación → `LoggedIn=false`).
  - Commit `feat(provider): adaptador de Antigravity`.
- [ ] **Si el spike dio `sin vía fiable`:**
  - `New("antigravity", …)` devuelve un adaptador cuyo `Start` falla con `NotInstalled` y el mensaje `"adaptador de agy pendiente (ver docs/superpowers/notes/…)"`.
  - En `default.toml`, `[roles.implementer]` pasa a `provider="codex"`, `model="gpt-5.6-sol"`, y `TestDefault` se actualiza a ese valor.
  - Commit `feat(provider): agy pendiente; implementador por defecto Codex`.

### Task 12: Engine — ejecución, aprobaciones y agentes

**Files:**
- Create: `internal/prompts/prompts.go`, `internal/prompts/orchestrator.md`, `internal/prompts/worker.md`, `internal/prompts/receipt_retry.md`
- Create: `internal/engine/engine.go`, `internal/engine/approval.go`, `internal/engine/agents.go`
- Test: `internal/engine/engine_test.go`, `internal/engine/agents_test.go`

**Interfaces:**
- Consumes: `config`, `gitx` (Repo, Guard, WeaverDir), `run`, `provider`, `receipt`, `verify`, `prompts`.
- Produces:
  ```go
  package prompts
  type WorkerData struct{ TaskID, Title, SpecPath, Branch, Instructions string; SpecRefs, Checks []string }
  func Orchestrator(projectDir string) string               // override: <repo>/.weaver/prompts/orchestrator.md
  func Worker(projectDir string, d WorkerData) (string, error) // text/template
  func ReceiptRetry(projectDir string) string

  package engine
  type ApprovalRequest struct{ Kind, Path, Summary string; Plan *run.Plan }
  type Approver interface{ Ask(ctx context.Context, r ApprovalRequest) (approved bool, comment string, err error) }
  type AgentInfo struct{ ID, TaskID, Role, Provider, Model, Status, Err string; Started time.Time; ReceiptOK, Verified *bool }
  type AgentResult struct{ ID, Status, Err string; Receipt *receipt.Receipt; Verification *verify.Verification }
  type Options struct {
      Cfg config.Config; Repo gitx.Repo; Store run.Store
      Providers map[string]provider.Provider; Approver Approver
      Notify func(any) // recibe []AgentInfo y []gitx.Violation
  }
  type Engine struct{ /* … */ }
  func New(ctx context.Context, o Options) (*Engine, error) // crea el Guard
  func (e *Engine) StartRun(ctx context.Context) (*run.State, error)
  func (e *Engine) Resume(ctx context.Context, runID string) (*run.State, error) // "" = la última; MarkInterrupted
  func (e *Engine) State() *run.State
  func (e *Engine) RequestApproval(ctx context.Context, r ApprovalRequest) (bool, string, error)
  func (e *Engine) Spawn(ctx context.Context, taskID, role, prov, model, instructions string) (string, error)
  func (e *Engine) FollowUp(ctx context.Context, agentID, msg string) error
  func (e *Engine) Wait(ctx context.Context, ids []string, timeout time.Duration) []AgentResult
  func (e *Engine) AcceptTask(ctx context.Context, taskID, agentID string) error
  func (e *Engine) Status(agentID string) []AgentInfo
  func (e *Engine) Cancel(agentID string) error
  func (e *Engine) Cleanup(ctx context.Context, agentID string) error
  func (e *Engine) CheckRefs(ctx context.Context) []gitx.Violation
  func (e *Engine) Close(ctx context.Context) error
  ```
- `StartRun`: la primera vez crea `SessionDir = WeaverDir/session-<runID>` como worktree con la rama `weaver/<runID>` desde `BaseBranch` (la rama actual del usuario). En las ejecuciones siguientes hace `SwitchNew` en ese mismo worktree, desde `BaseBranch`. Guarda el estado.
- `RequestApproval`: aplica `CheckRequest` y, para `plan`, también `ValidatePlan`. Para `integration` lo hace Task 13: aquí solo se llama a un hook `e.beforeIntegration`, que es nil hasta entonces. Después pregunta al `Approver`, aplica `Record` y guarda.
- `Spawn`:
  - Aplica `CheckSpawn` (`merged` = `IsAncestor(rama del agente aceptado, IntegrationBranch)`) y resuelve el rol, que se puede sobrescribir con `prov`+`model`.
  - Crea la rama `weaver/<run>/<id>` desde la punta de `IntegrationBranch` y el worktree `WeaverDir/<run>/<id>`.
  - Pone el agente en `queued` mientras espera turno en un semáforo por proveedor (`max_parallel`), y en `running` al arrancar.
  - Lanza el provider con el prompt `Worker(...)` (que va en `SystemPromptFile` para Claude y antepuesto al prompt en los demás) y `task_timeout`.
  - Copia cada `Event.Raw` a `transcript.jsonl` y devuelve el id de inmediato.
- Pipeline al terminar un proceso, en este orden:
  1. `CommitAll("weaver: cambios sin commit de <id>")`.
  2. `Extract` + `Validate`. Si falla, un reintento con `ReceiptRetry` (no cuenta como ronda); si vuelve a fallar → agente `failed`, `Err="no_receipt"`, tarea `receipt_failed`.
  3. `CheckRefs` → `RefViolations`.
  4. `verify.Verify` + `Finalize`.
  5. Tarea `receipt_ok` si el recibo dice `done` y `Passed`; si no, `receipt_failed`.
  6. Guardar `receipt.json`, `verification.json` y el estado, y llamar a `Notify`.
- `FollowUp`: exige agente terminado y `Rounds < ReviewRounds` (si no, `ErrRoundsExhausted`); `Rounds++`; relanza con `ResumeID = SessionID` en el mismo worktree.
- `AcceptTask`: exige que el último recibo del agente sea `done` y que la verificación tenga `Passed`; tarea `accepted` y `AcceptedAgent = agentID`.
- `Cancel`: mata el proceso; agente `cancelled`; se conserva el worktree.
- `Cleanup`: exige `IsAncestor(rama, IntegrationBranch)`; luego quita el worktree y borra la rama.
- `Wait`: espera a que terminen todos, a que venza el timeout o a que se cancele el `ctx`, lo que pase primero.
- `Close`: mata todos los agentes. Si la ejecución actual no tiene aprobaciones ni commits sobre `BaseBranch`, borra su rama y su estado. Si ya no queda ninguna ejecución abierta (`Phase != integrated`) que use el worktree de sesión, también lo borra.
- Contenido de los prompts:
  - `orchestrator.md`: flujo SDD de §5.6, herramientas de §5.3, prohibición de escribir código (§5.3.1), aprobación final y recibos. Tras mergear la rama de un agente aceptado, llama a `cleanup_agent` con ese agente.
  - `worker.md`: tarea, spec, rama, checks; commit solo en tu rama; prohibido `git push`, `checkout` o `reset` de otras ramas; termina con el bloque `weaver-receipt` de §5.4.

- [ ] **Step 1: Tests que fallan.** Todos con un repo temporal, providers falsos en memoria que implementan `provider.Provider` (devuelven un `Final` configurable y pueden escribir/commitear un archivo en `req.Dir`) y un `Approver` falso:
  - `TestStartRunCreatesSessionWorktree`.
  - `TestApprovalOrder`: plan antes del spec → error y el Approver no es llamado.
  - `TestSpawnBeforePlanRejected`.
  - `TestSpawnHappyPath`: recibo válido → tarea `receipt_ok`, `verification.json` con `Passed`.
  - `TestAutoCommit`: el provider deja cambios sin commit → hay commit `weaver: cambios sin commit de a1`.
  - `TestReceiptRetry`: sin recibo la primera vez y con recibo en el reintento → ok, `Rounds == 0`; sin recibo dos veces → `no_receipt`.
  - `TestRefViolationFailsVerification`: el provider mueve `main` → restaurado y `Passed == false`.
  - `TestMaxParallelQueues`: `max_parallel = 1` y dos spawns → el segundo queda `queued` hasta que termina el primero.
  - `TestFollowUpRounds`: la tercera llamada → `ErrRoundsExhausted`.
  - `TestAcceptRequiresVerified`.
  - `TestCleanupRequiresMerged`.
  - `TestTaskTimeout`: provider que duerme y `task_timeout` de 200 ms → agente `timeout`.
  - `TestWaitCancel`: cancelar el `ctx` desbloquea `Wait` en < 1 s.
  - `TestApprovalCancel`: cancelar el `ctx` mientras el Approver espera → `RequestApproval` vuelve con error.
  - `TestResumeMarksInterrupted`: estado guardado con un agente `running` → `Resume` lo deja `interrupted` y la tarea `pending`.
  - `TestCloseDeletesEmptyRun`.
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS con `go test -race ./internal/engine/`.
- [ ] **Step 5: Commit** `feat(engine): ejecuciones, aprobaciones y ciclo de vida de agentes`.

### Task 13: Engine — integración final

**Files:**
- Create: `internal/engine/integrate.go`
- Test: `internal/engine/integrate_test.go`

**Interfaces:**
- Consumes: Task 12, `verify.RunCheck`, `gitx`.
- Produces: el hook `beforeIntegration` y `func (e *Engine) afterIntegration(ctx) (msg string, err error)`, conectados a `RequestApproval` para `kind == "integration"`.
- Antes de preguntar:
  - Cada tarea debe estar `accepted`, con la rama de su `AcceptedAgent` ya contenida en `IntegrationBranch` (si no, error que nombra las tareas).
  - Se ejecuta cada `GlobalChecks` en `SessionDir` con `RunCheck`. Si alguno falla, devuelve un error al orquestador con los resultados y **no** se le pregunta al usuario.
- Tras aprobar:
  - Si la rama actual del repo principal es `BaseBranch`, se hace `MergeFFOnly(IntegrationBranch)` y `Guard.Rebase`.
  - Si no es la rama base, o el fast-forward falla, no se toca nada y el mensaje dice qué rama mergear.
  - `Phase = integrated`, se guarda, y `StartRun` abre la siguiente ejecución.

- [ ] **Step 1: Tests que fallan:** `TestIntegrationBlockedByUnmergedTask`; `TestIntegrationGlobalCheckFails` (el Approver no es llamado); `TestIntegrationFastForward` (main avanza y el Guard no lo reporta); `TestIntegrationNonFF` (main divergió → main intacto, mensaje con `weaver/<run>`); `TestNextRunAfterIntegration`.
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS con `-race`.
- [ ] **Step 5: Commit** `feat(engine): integración final y siguiente ejecución`.

### Task 14: Servidor MCP

**Files:**
- Create: `internal/mcpserver/server.go`, `internal/mcpserver/tools.go`
- Test: `internal/mcpserver/server_test.go`

**Interfaces:**
- Consumes: `*engine.Engine`.
- Produces:
  ```go
  package mcpserver
  func NewHandler(e *engine.Engine) http.Handler                       // go-sdk: mcp.NewServer + mcp.AddTool + NewStreamableHTTPHandler
  func Listen(ctx context.Context, h http.Handler) (url, token string, err error) // 127.0.0.1:0, exige "Authorization: Bearer <token>", token de 32 bytes con crypto/rand
  func WriteMCPConfig(path, url, token string) error
  // {"mcpServers":{"weaver":{"type":"http","url":"<url>","headers":{"Authorization":"Bearer <token>"}}}}
  ```
- Herramientas, con entradas y salidas en JSON, mapeadas 1:1 al Engine: `request_approval{kind,path,summary,tasks?,global_checks?}`, `spawn_agent{task_id,role?,provider?,model?,instructions}`, `wait_agents{agent_ids,timeout_s}`, `follow_up{agent_id,message}`, `accept_task{task_id,agent_id}`, `agent_status{agent_id?}`, `cancel_agent{agent_id}`, `cleanup_agent{agent_id}`. Los errores del Engine vuelven como resultado de herramienta con `IsError` y un mensaje en español, no como error de protocolo.

- [ ] **Step 1: Tests que fallan** con el cliente del go-sdk contra `Listen`:
  - `TestRejectsWithoutToken` → 401.
  - `TestToolsListed`: los 8 nombres.
  - `TestSpawnBeforePlanIsToolError`.
  - `TestWaitAgentsNoTranscript`: la respuesta no contiene ninguna línea de `transcript.jsonl` (el fake escribe un marcador `TRANSCRIPT-ONLY` solo en el transcript).
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(mcpserver): herramientas MCP del orquestador`.

### Task 15: TUI

**Files:**
- Create: `internal/tui/model.go`, `internal/tui/view.go`, `internal/tui/approver.go`
- Test: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `provider.Event`, `engine.AgentInfo`, `engine.ApprovalRequest`, `gitx.Violation`.
- Produces:
  ```go
  package tui
  type OrchestratorEvent struct{ provider.Event }
  type OrchestratorExited struct{ Err error }
  type AgentsChanged struct{ Agents []engine.AgentInfo }
  type RefViolations struct{ Items []gitx.Violation }
  type ApprovalAnswer struct{ Approved bool; Comment string }
  type ApprovalAsked struct{ Req engine.ApprovalRequest; Reply chan<- ApprovalAnswer }
  type AgentDetail struct{ ID, Receipt, Verification, LogTail string }
  func New(title string, send func(string) error, detail func(agentID string) AgentDetail) Model
  type Approver struct{ P *tea.Program }   // implementa engine.Approver; respeta ctx
  ```
- Comportamiento:
  - Chat en un viewport y entrada en un textarea; Enter llama a `send`.
  - Los eventos `text` del orquestador se muestran como `opus ›`, los `tool` como una línea tenue `· <tool>` y los `error` en rojo.
  - Panel de agentes con ↑/↓. Enter abre `detail` en un modal de solo lectura y Esc lo cierra.
  - `ApprovalAsked` muestra `⚑ Aprobación: <kind> — [a]probar [r]echazar`. Con `r` se pide un comentario y Enter lo envía.
  - `RefViolations` muestra un aviso en el chat.
  - Ctrl+C pide confirmación (`¿Parar todo? [s/N]`) y con `s` sale (`tea.Quit`).
  - `OrchestratorExited` muestra el error y `weaver resume` para retomar.

- [ ] **Step 1: Tests que fallan** con `Update` directo sobre el `Model` y aserciones sobre `View()` y los canales:
  - `TestEnterSends`.
  - `TestOrchestratorTextRendered`.
  - `TestApprovalApprove`: `a` → `Reply` recibe `{true, ""}`.
  - `TestApprovalRejectWithComment`.
  - `TestAgentsPanel`: 3 agentes con sus estados e iconos ● ✓ ✗.
  - `TestCtrlCConfirm`: `n` cancela y `s` → `tea.Quit`.
  - `TestApproverCtxCancel`: `Approver.Ask` vuelve al cancelar el `ctx` (con un programa de prueba).
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(tui): chat, panel de agentes y aprobaciones`.

### Task 16: App y comandos

**Files:**
- Create: `internal/app/session.go`, `internal/app/doctor.go`, `internal/app/runs.go`
- Modify: `cmd/weaver/main.go`
- Test: `internal/app/session_test.go`

**Interfaces:**
- Consumes: todo lo anterior.
- Produces:
  ```go
  package app
  var OrchestratorTools = []string{"Read", "Grep", "Glob", "Edit(./docs/**)", "Write(./docs/**)", "Bash(git *)", "mcp__weaver"}
  type SessionOptions struct {
      Dir, ResumeID string; Resume bool; In io.Reader; Out io.Writer
      Headless func(send func(string) error, events <-chan provider.Event) error // solo tests; nil = TUI
  }
  func RunSession(ctx context.Context, o SessionOptions) error
  func Doctor(ctx context.Context, dir string, out io.Writer) int // código de salida
  func Runs(dir string, out io.Writer) error
  ```
- Subcomandos de `main.go` (stdlib `flag`): `weaver` → `RunSession`; `weaver resume [run-id]`; `weaver runs`; `weaver doctor`; `-h` en español.
- `RunSession`, en este orden:
  1. `gitx.Open`; si no es git, explica y termina con código 1.
  2. Si el árbol está sucio, pregunta por `in`/`out`: `Hay cambios sin commit; no entrarán en la ejecución. ¿Continuar? [s/N]`.
  3. Añade `.weaver/` a `<CommonDir>/info/exclude` si no está.
  4. `config.Load`, construye los providers y crea el `engine` (`Approver` = `tui.Approver`, `Notify` → `p.Send`).
  5. `StartRun` o `Resume`.
  6. `Listen` + `WriteMCPConfig` en `.weaver/runs/<id>/mcp.json`, y escribe el prompt del orquestador en `.weaver/runs/<id>/orchestrator.md`.
  7. Lanza el orquestador (`Mode: Orchestrator`, `Dir: SessionDir`, el modelo del rol orquestador, `AllowedTools: OrchestratorTools`), con `SessionID` nuevo (UUID v4 con crypto/rand) guardado en el estado o con `ResumeID` al reanudar.
  8. Reenvía sus eventos a la TUI. En cada `turn_end`, `CheckRefs` y, si hay violaciones, `RefViolations`.
  9. Al salir, mata el orquestador y llama a `engine.Close`.
- `Doctor`: tabla con proveedor, instalado, versión, sesión, y los modelos de los roles que no aparecen en `Health.Models` cuando ese proveedor lista modelos. A Antigravity le añade la nota `soporte de términos no confirmado`. Código de salida 1 si al proveedor del rol orquestador le falta algo.

- [ ] **Step 1: Tests que fallan:**
  - `TestNotGitRepo`.
  - `TestDirtyPromptDecline`: responder `n` → termina sin crear ejecución.
  - `TestExcludeAddedOnce`: dos llamadas → una sola línea.
  - `TestOrchestratorLaunchArgs`: con el CLI falso de Task 7 como `bin` de claude → los args contienen `--mcp-config <…mcp.json>`, `--permission-mode dontAsk`, cada `OrchestratorTools` y `--session-id`. Usa `SessionOptions.Headless` con una función que envía una línea, espera un `turn_end` y sale.
  - `TestDoctorMissingOrchestrator` → código de salida 1.
- [ ] **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS; `go build ./cmd/weaver` funciona.
- [ ] **Step 5: Commit** `feat(app): sesión, doctor y comandos`.

### Task 17: Pruebas de humo con CLIs reales

**Files:**
- Create: `internal/provider/smoke_test.go` (con `//go:build smoke`)
- Modify: `README.md` (sección "Uso" con `weaver`, `weaver doctor`, `weaver resume`, `weaver runs`, y cómo correr el humo)

- [ ] **Step 1:** Escribe `TestSmoke`: por cada proveedor de `WEAVER_SMOKE_PROVIDERS` (por ejemplo `claude,codex,opencode,antigravity`), en un repo temporal, lanza un Worker con el prompt `Worker(...)` de la tarea `T1: crea hola.txt con "hola", haz commit`, con check `git log -1`. Afirma: `Final.Err == nil`, recibo válido y `verify.Passed`. Imprime la duración y el SessionID.
- [ ] **Step 2:** Corre `$env:WEAVER_SMOKE_PROVIDERS="claude,codex,opencode,antigravity"; go test -tags smoke -run TestSmoke -v ./internal/provider/` → PASS para cada proveedor instalado y con sesión (sin `agy` si el spike lo descartó). Si alguno falla por formato, actualiza su fixture y su adaptador.
- [ ] **Step 3:** `weaver doctor` en este repo → tabla sin errores para el orquestador.
- [ ] **Step 4:** Prueba manual de punta a punta en un repo de juguete: `weaver`, pedir "añade un archivo NOTAS.md con una línea", aprobar el spec, el plan y la integración, y comprobar que `main` avanzó y que no quedaron worktrees (`git worktree list`).
- [ ] **Step 5: Commit** `test: pruebas de humo por proveedor y README de uso`.
