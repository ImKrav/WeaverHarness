# WeaverHarness — Plan 3: Utilidades de CLI

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Completar las utilidades de línea de comandos de `weaver` para operar, inspeccionar, limpiar y configurar el sistema cómodamente sin abrir JSONs internos ni levantar la TUI: `version`/`--version`, `inspect`, `logs`, `clean`, `config`, presets por flag y pre-check de arranque.

**Architecture:** Se mantiene la librería estándar `flag` en `cmd/weaver/main.go` (cero dependencias nuevas) con inyección de dependencias (`cliDeps`) para testear todos los subcomandos y códigos de salida en `cmd/weaver/main_test.go`. Cada nuevo subcomando se implementa en su propio archivo en `internal/app/` (`inspect.go`, `clean.go`, `configcmd.go`).

**Tech Stack:** Go 1.27 · `flag` (stdlib) · `text/tabwriter` · `github.com/BurntSushi/toml` · `git` CLI.

**Base:** `docs/superpowers/specs/2026-11-26-weaverharness-design.md` · `docs/superpowers/plans/2026-11-26-weaverharness-v1.md`

## Contexto del código existente y trampas conocidas

- **Trampa con `TestOrchestratorLaunchArgs` en `internal/app/session_test.go`:** ese test configura `claude.bin = fakecli` con `FAKECLI_ECHO=1` y `FAKECLI_ARGS=...`. Si Task 4 ejecuta `orchProv.Check(ctx)` (que invoca `fakecli --version` y `fakecli auth status`), `fakecli` con `FAKECLI_ECHO=1` se quedaría bloqueado leyendo `os.Stdin` y sobrescribiría `FAKECLI_ARGS`. Por eso Task 4 debe pasar `SkipPreflight: true` en `TestOrchestratorLaunchArgs` y probar el preflight con tests dedicados.
- **Trampa de `SessionDir` compartido en `weaver clean`:** múltiples runs de una misma sesión comparten el mismo `SessionDir` (`<repo>-weaver/session-<run-inicial>`, ver `TestNextRunAfterIntegration`). `weaver clean` **nunca** debe borrar un `SessionDir` mientras exista cualquier run no integrado (`Phase != PhaseIntegrated`) que apunte a esa misma ruta (comparando con normalización case-insensitive en Windows).
- **Orden de borrado en git:** `git branch -D <rama>` falla si `<rama>` está checked out en algún worktree; siempre eliminar el worktree (o `switch --detach`) antes de borrar la rama.
- **Stdlib `flag`:** en Go, los flags de cada subcomando deben colocarse antes de los argumentos posicionales (ej. `weaver inspect --json [run-id]`, `weaver logs --tail 50 [run-id] <agent-id>`). Validar argumentos sobrantes devolviendo código `2`.

## Global Constraints

- Sin dependencias nuevas en `go.mod`.
- Salida normal a `stdout`; errores y ayuda de uso a `stderr`.
- Códigos de salida consistentes: `0` éxito, `1` error operativo / `doctor` fallido, `2` error de uso / flags o argumentos inválidos.
- Comandos destructivos (`weaver clean --all`) requieren confirmación interactiva `[s/N]` salvo `--force` o `--dry-run`.
- Todos los textos visibles en español.

---

### Task 1: Versionado en compilación (`--version`) y tests del entrypoint `cmd/weaver`

**Files:**
- Create: `cmd/weaver/main_test.go`
- Modify: `cmd/weaver/main.go`

**Interfaces:**
- Consumes: `internal/app`.
- Produces:
  ```go
  // cmd/weaver/main.go (inyectables vía -ldflags "-X main.version=... -X main.commit=... -X main.date=...")
  var (
      version = "dev"
      commit  = "none"
      date    = "unknown"
  )
  type cliDeps struct {
      getwd  func() (string, error)
      stdin  io.Reader
      stdout io.Writer
      stderr io.Writer
  }
  func runMainWith(args []string, deps cliDeps) int
  ```
  - Soporta `weaver version`, `weaver -v` y `weaver --version` (imprime `weaver <version> (<commit>, <date>)\n` en `stdout`, exit `0`).
  - Rechaza argumentos extra en subcomandos que no aceptan más argumentos (`runs`, `doctor`, `version`) con exit `2`.

- [ ] **Step 1: Tests que fallan** en `cmd/weaver/main_test.go`:
  - `TestHelpExitZero`: `-h` y `--help` salen con `0` e imprimen ayuda en `stderr`.
  - `TestVersionCommandAndFlags`: `version`, `-v` y `--version` salen con `0` e incluyen `version`, `commit` y `date` en `stdout`.
  - `TestUnknownSubcommandExitTwo`: `weaver inventado` sale con `2` y muestra ayuda en `stderr`.
  - `TestUnexpectedExtraArgsExitTwo`: `weaver runs extra` y `weaver doctor extra` salen con `2`.
  - `TestRunsOnNonGitExitOne`: `weaver runs` en un `t.TempDir()` fuera de git sale con `1`.
- [ ] **Step 2:** `go test ./cmd/weaver/` → FAIL.
- [ ] **Step 3: Implementar** `version`/`commit`/`date`, `cliDeps`, `runMainWith` y actualizar `usage()` en `cmd/weaver/main.go`.
- [ ] **Step 4:** `go test -race ./cmd/weaver/` → PASS.
- [ ] **Step 5: Commit** `feat(cli): flags de versión, inyección ldflags y tests de main`.

---

### Task 2: Comandos `weaver inspect` y `weaver logs`

**Files:**
- Create: `internal/app/inspect.go`, `internal/app/inspect_test.go`
- Modify: `cmd/weaver/main.go`

**Interfaces:**
- Consumes: `gitx.Open`, `run.Store`, `receipt.Receipt`, `verify.Verification`.
- Produces:
  ```go
  // internal/app
  type InspectOptions struct {
      Dir   string
      RunID string // vacío = última ejecución
      JSON  bool
      Out   io.Writer
  }
  type LogsOptions struct {
      Dir         string
      RunID       string // vacío = última ejecución
      AgentID     string // obligatorio
      Tail        int    // <= 0 usa 100
      OnlyReceipt bool   // omite transcript.jsonl
      Out         io.Writer
  }
  func Inspect(o InspectOptions) error
  func Logs(o LogsOptions) error
  ```
  - Subcomandos en `cmd/weaver/main.go`:
    - `weaver inspect [--json] [run-id]`: muestra cabecera (`RunID`, `Phase`, `BaseBranch`, `IntegrationBranch`, `SessionDir`), aprobaciones, tabla de tareas (`ID`, `ROL`, `ESTADO`, `RONDAS`, `AGENTE`) y tabla de agentes (`ID`, `TAREA`, `PROVEEDOR/MODELO`, `ESTADO`, `DURACIÓN`, `RECIBO`, `VERIF`, `ERROR`). Con `--json` imprime un objeto estructurado con `state`, `receipts` y `verifications`.
    - `weaver logs [--tail N] [--only-receipt] [run-id] <agent-id>`: si recibe 1 argumento posicional lo interpreta como `<agent-id>` del último run; si recibe 2, `<run-id> <agent-id>`; 0 o >2 argumentos → exit `2`. Imprime `receipt.json`, `verification.json` y (salvo `--only-receipt`) las últimas `N` líneas crudas de `transcript.jsonl` (sin acoplar parsers específicos de cada CLI).

- [ ] **Step 1: Tests que fallan** en `internal/app/inspect_test.go` y `cmd/weaver/main_test.go`:
  - `TestInspectLatestRunTextAndJSON`: con un `state.json` de 2 tareas y 1 agente con `receipt.json` + `verification.json`, valida salida tabular y que `--json` decodifica correctamente.
  - `TestLogsSingleArgUsesLatestRun`: con 1 argumento (`a1`) resuelve el último run, muestra recibo/verificación y respeta `Tail: 2` sobre un `transcript.jsonl` de 5 líneas.
  - `TestLogsOnlyReceiptSkipsTranscript`: con `OnlyReceipt: true` no incluye líneas de `transcript.jsonl`.
  - `TestLogsMissingAgentReturnsError`: agente inexistente devuelve error claro en español.
  - `TestLogsBadArgCountExitTwo` (`cmd/weaver`): `weaver logs` sin args y `weaver logs a b c` salen con código `2`.
- [ ] **Step 2:** `go test ./internal/app/ ./cmd/weaver/` → FAIL.
- [ ] **Step 3: Implementar** `internal/app/inspect.go` y cablear `inspect` y `logs` en `cmd/weaver/main.go`.
- [ ] **Step 4:** `go test -race ./internal/app/ ./cmd/weaver/` → PASS.
- [ ] **Step 5: Commit** `feat(app,cli): comandos inspect y logs para auditar ejecuciones`.

---

### Task 3: Comando `weaver clean` para limpiar worktrees, ramas y runs antiguos de forma segura

**Files:**
- Create: `internal/app/clean.go`, `internal/app/clean_test.go`
- Modify: `cmd/weaver/main.go`, `internal/gitx/git.go`

**Interfaces:**
- Consumes: `gitx.Open`, `gitx.WeaverDir`, `run.Store`.
- Produces:
  ```go
  // internal/gitx
  type WorktreeInfo struct {
      Path   string
      Branch string // nombre corto sin "refs/heads/", vacío si detached
      Head   string
  }
  func (r Repo) ListWorktrees(ctx context.Context) ([]WorktreeInfo, error)
  func (r Repo) PruneWorktrees(ctx context.Context) error
  func (r Repo) ListWeaverBranches(ctx context.Context) ([]string, error) // nombres cortos "weaver/..."
  ```
  ```go
  // internal/app
  type CleanOptions struct {
      Dir    string
      DryRun bool
      All    bool // incluye runs no integrados (open/spec_approved/plan_approved)
      Force  bool // omite confirmación [s/N] cuando All == true
      In     io.Reader
      Out    io.Writer
  }
  func Clean(ctx context.Context, o CleanOptions) error
  ```
  - **Reglas de seguridad de `Clean`:**
    1. Si `o.Dir` está dentro de `WeaverDir(repo.Dir)`, abortar con error claro pidiendo ejecutar `weaver clean` desde el repositorio principal.
    2. Sin `--all`:
       - Conserva todos los runs con `Phase != run.PhaseIntegrated`, sus ramas (`IntegrationBranch` y ramas de sus agentes) y **cualquier `SessionDir` referenciado por un run abierto** (incluso si también fue usado por un run ya `integrated`).
       - Limpia worktrees/ramas/directorios de runs `PhaseIntegrated` y worktrees/ramas huérfanas bajo `WeaverDir` / `weaver/*` que no pertenezcan a ningún run abierto.
    3. Con `--all`: pide confirmación (`¿Eliminar también N ejecuciones abiertas y todos los worktrees de weaver? [s/N] `) salvo `--force` o `--dry-run`.
    4. Orden de borrado: primero `RemoveWorktree` para cada worktree objetivo, luego `PruneWorktrees`, luego `DeleteBranch` para ramas objetivo, luego `store.Delete` y finalmente borrar `WeaverDir` solo si quedó vacío.

- [ ] **Step 1: Tests que fallan** en `internal/app/clean_test.go`:
  - `TestCleanDefaultPreservesOpenRunsAndSharedSessionDir`: run 1 (`integrated`) y run 2 (`plan_approved`) comparten `SessionDir`; además existe una rama huérfana `weaver/huerfana`. `Clean` sin `--all` borra la rama del run 1 y la huérfana, pero conserva intactos el run 2, su worktree de agente y el `SessionDir` compartido.
  - `TestCleanDryRunChangesNothing`: `DryRun: true` imprime el plan de limpieza pero no elimina worktrees, ramas ni estados.
  - `TestCleanAllDeclinedAndForced`: con `All: true` y respuesta `"n"` no borra runs abiertos; con `All: true, Force: true` limpia todos los worktrees bajo `WeaverDir`, todas las ramas `weaver/*` y vacía `.weaver/runs`.
  - `TestCleanRefusesRunningInsideWeaverWorktree`: ejecutar `Clean` con `Dir` dentro de un worktree de `WeaverDir` devuelve error claro.
- [ ] **Step 2:** `go test ./internal/gitx/ ./internal/app/ ./cmd/weaver/` → FAIL.
- [ ] **Step 3: Implementar** helpers de `gitx`, `internal/app/clean.go` y subcomando `clean [--dry-run] [--all] [--force]` en `cmd/weaver/main.go`.
- [ ] **Step 4:** `go test -race ./internal/gitx/ ./internal/app/ ./cmd/weaver/` → PASS.
- [ ] **Step 5: Commit** `feat(app,cli,gitx): comando weaver clean para podar worktrees y runs`.

---

### Task 4: Comando `weaver config` (`show` / `init`), presets explícitos y pre-check de arranque

**Files:**
- Create: `internal/app/configcmd.go`, `internal/app/configcmd_test.go`
- Modify: `cmd/weaver/main.go`, `internal/config/config.go`, `internal/app/session.go`, `internal/app/session_test.go`
- Test: `internal/config/config_test.go`, `internal/app/configcmd_test.go`, `internal/app/session_test.go`

**Interfaces:**
- Consumes: `config.Load`, `config.Default`, `provider.Provider.Check`.
- Produces:
  ```go
  // internal/config
  type Preset struct {
      Name        string
      Description string
      Roles       map[string]Role
  }
  func Presets() []Preset
  func ApplyPreset(cfg Config, name string) (Config, error) // valida resultado con validate(&cfg)
  func RenderTOML(cfg Config) (string, error)
  ```
  - **Presets concretos v1:**
    - `default`: `architect=claude/opus`, `implementer=antigravity/gemini-4-hydrogen`, `reviewer=codex/gpt-5.6-sol`.
    - `claude-codex`: `architect=claude/opus`, `implementer=codex/gpt-5.6-sol`, `reviewer=codex/gpt-5.6-sol`.
    - `claude-opencode`: `architect=claude/opus`, `implementer=opencode/opencode/big-pickle`, `reviewer=codex/gpt-5.6-sol`.
    - `claude-only`: `architect=claude/opus`, `implementer=claude/sonnet`, `reviewer=claude/sonnet`.
  - **Precedencia de `--preset` en runtime:** `default.toml` → global → `.weaver/config.toml` → `--preset` (sobrescribe únicamente `cfg.Roles`, preservando `cfg.Providers[*].Bin`, `MaxParallel` y timeouts configurados por el usuario).
  ```go
  // internal/app
  type ConfigShowOptions struct{ Dir, Preset string; Out io.Writer }
  type ConfigInitOptions struct{ Dir, Preset string; Global, Force bool; Out io.Writer }
  func ConfigShow(o ConfigShowOptions) error
  func ConfigInit(o ConfigInitOptions) error
  ```
  - En `SessionOptions`: añadir `Preset string` y `SkipPreflight bool`.
  - Pre-check en `RunSession` (antes de `StartRun` y antes de crear la TUI): si `!o.SkipPreflight`, llama a `orchProv.Check(preflightCtx)` con timeout de 10s; si `!h.Installed || !h.LoggedIn`, aborta con error accionable sugiriendo `weaver doctor` o iniciar sesión en el CLI (y sin dejar runs vacíos ni abrir pantalla alternativa).
  - Flags:
    - `weaver [--preset <nombre>] [--skip-check] [--no-mouse]`
    - `weaver resume [--preset <nombre>] [--skip-check] [--no-mouse] [run-id]`
    - `weaver config show [--preset <nombre>]`
    - `weaver config init [--preset <nombre>] [--global] [--force]`

- [ ] **Step 1: Tests que fallan:**
  - `TestApplyPresetAndRenderRoundTrip` (`config`): `ApplyPreset` preserva `Providers["claude"].Bin` y aplica los roles del preset; preset desconocido devuelve error que enumera los 4 presets válidos; `RenderTOML` → `toml.Decode` hace round-trip exacto (incluyendo `Duration` como `"10m0s"`/`"30m0s"`).
  - `TestConfigInitAndShow` (`app`): `ConfigInit` escribe `.weaver/config.toml` con el preset elegido, rechaza sobrescribir si ya existe sin `Force: true`, y `ConfigShow` imprime la configuración efectiva fusionada.
  - `TestRunSessionPreflightFailsFastWithoutCreatingRun` (`app`): con `claude.bin` inexistente y `SkipPreflight: false`, `RunSession` devuelve error claro mencionando `weaver doctor` antes de crear `.weaver/runs/` ni invocar `Headless`.
  - Actualizar `TestOrchestratorLaunchArgs` en `internal/app/session_test.go` para pasar `SkipPreflight: true` (por el uso de `FAKECLI_ECHO=1`).
- [ ] **Step 2:** `go test ./internal/config/ ./internal/app/ ./cmd/weaver/` → FAIL.
- [ ] **Step 3: Implementar** presets y `RenderTOML` en `internal/config`, `internal/app/configcmd.go`, preflight en `internal/app/session.go` y flags en `cmd/weaver/main.go`.
- [ ] **Step 4:** `go test -race ./internal/config/ ./internal/app/ ./cmd/weaver/` → PASS.
- [ ] **Step 5: Commit** `feat(config,app,cli): comando config, presets y verificación previa de arranque`.
