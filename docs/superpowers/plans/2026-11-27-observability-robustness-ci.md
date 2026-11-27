# WeaverHarness — Plan 4: Robustez, observabilidad y CI

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Endurecer WeaverHarness para uso continuo: métricas precisas por tarea y proveedor (retrocompatibles con v1), protección de memoria en lectura JSONL **tanto en `jsonl.go` como en `agy.go`**, recuperación automática de worktrees obsoletos, semáforo seguro ante proveedores dinámicos, `go.mod` limpio, linting con `golangci-lint` v2 + `govulncheck` y CI multiplataforma en GitHub Actions.

**Architecture:** Se añaden métricas estructuradas en `internal/run` alimentadas desde `internal/engine`, un helper compartido de lectura acotada en `internal/provider` (`jsonl.go` + `agy.go`), endurecimientos en `gitx` y `engine`, configuración `.golangci.yml` (esquema v2) y `.github/workflows/ci.yml`.

**Tech Stack:** Go 1.27 · `go test -race` · `golangci-lint` v2 · `govulncheck` · GitHub Actions (`ubuntu-latest`, `windows-latest`, `macos-latest`).

**Base:** `docs/superpowers/specs/2026-11-26-weaverharness-design.md` · `docs/superpowers/plans/2026-11-26-weaverharness-v1.md`

## Contexto del código existente y hallazgos clave

- **Lectura JSONL duplicada en `agy.go`:** `internal/provider/agy.go` (`agyRun.drive`) tiene su propio bucle `bufio.NewReader(stdout).ReadBytes('\n')` independiente de `internal/provider/jsonl.go`. Un límite de línea debe proteger **ambos** mediante un helper compartido `readLineLimited`.
- **`ActiveDurationMS` vs `EndedAt - StartedAt`:** en `internal/engine/agents.go`, `FollowUp` limpia `EndedAt` pero conserva el `StartedAt` original del `Spawn`. Si se calcula duración como `EndedAt.Sub(StartedAt)`, incluiría minutos de pausa humana entre rondas. Acumular `ActiveDurationMS` por cada llamada a `executeProvider` da tiempo real de cómputo del agente, mientras que `WallDuration` mide el ciclo completo de la ejecución.
- **Bug latente en `Engine.semaphoreFor` (`internal/engine/agents.go:517`):** si un proveedor no estaba en `e.sems`, creaba un canal nuevo `make(chan struct{}, 3)` en cada llamada sin guardarlo en el mapa. Debe inicializarlo perezosamente bajo `e.mu` para que todas las goroutines compartan el mismo semáforo.
- **Higiene actual de `go.mod`:** actualmente todas las dependencias en `go.mod` están marcadas como `// indirect` (incluso las directas como `bubbletea`, `toml`, `go-sdk`). Task 3 debe ejecutar `go mod tidy` antes de activar la verificación `go mod tidy && git diff --exit-code go.mod go.sum` en CI, o el workflow fallaría en el primer push.

## Global Constraints

- Las métricas nunca almacenan credenciales ni transcripciones completas; solo contadores, duraciones, clases de error y proveedor/modelo.
- Todo campo nuevo en `run.Agent` / `run.State` usa `omitempty` para mantener 100% de retrocompatibilidad al cargar archivos `state.json` creados por v1.
- El workflow de CI usa permisos mínimos (`permissions: contents: read`) y valida Windows, Linux y macOS.

---

### Task 1: Métricas de ejecución por tarea y proveedor

**Files:**
- Create: `internal/run/metrics.go`, `internal/run/metrics_test.go`
- Modify: `internal/run/state.go`, `internal/engine/agents.go`, `internal/engine/integrate.go`, `internal/app/runs.go`

**Interfaces:**
- Consumes: `run.State`, `run.Agent`, `verify.Verification`.
- Produces:
  ```go
  // internal/run/state.go (campos nuevos retrocompatibles en Agent)
  type Agent struct {
      // ... existentes ...
      ActiveDurationMS int64 `json:"active_duration_ms,omitempty"` // suma de ejecuciones reales del CLI
      FollowUps        int   `json:"follow_ups,omitempty"`
      ReceiptRetries   int   `json:"receipt_retries,omitempty"`
      ChecksPassed     int   `json:"checks_passed,omitempty"`
      ChecksFailed     int   `json:"checks_failed,omitempty"`
      RefViolations    int   `json:"ref_violations,omitempty"`
  }
  ```
  ```go
  // internal/run/metrics.go
  type ProviderStats struct {
      Provider       string        `json:"provider"`
      Runs           int           `json:"runs"`
      Succeeded      int           `json:"succeeded"`
      QuotaErrors    int           `json:"quota_errors"`
      Timeouts       int           `json:"timeouts"`
      EmptyOutputs   int           `json:"empty_outputs"`
      ActiveDuration time.Duration `json:"active_duration_ns"`
  }
  type RunMetrics struct {
      TotalAgents    int             `json:"total_agents"`
      AcceptedTasks  int             `json:"accepted_tasks"`
      TotalTasks     int             `json:"total_tasks"`
      WallDuration   time.Duration   `json:"wall_duration_ns"`
      ActiveDuration time.Duration   `json:"active_duration_ns"`
      ReceiptRetries int             `json:"receipt_retries"`
      FollowUps      int             `json:"follow_ups"`
      ByProvider     []ProviderStats `json:"by_provider"`
  }
  func Summarize(s *State) RunMetrics
  ```
  - Fallback para `state.json` v1 sin `ActiveDurationMS`: si `ActiveDurationMS == 0` y `!StartedAt.IsZero() && !EndedAt.IsZero() && EndedAt.After(StartedAt)`, usa `EndedAt.Sub(StartedAt)`.

- [ ] **Step 1: Tests que fallan:**
  - `TestSummarizeMetricsAndLegacyFallback` (`run`): calcula `ByProvider` (ordenado alfabéticamente), `QuotaErrors` (`quota_exhausted`), `Timeouts`, `EmptyOutputs`, `ReceiptRetries`, `FollowUps`, `ActiveDuration` y `WallDuration`; verifica además un `state.json` v1 sin los campos nuevos.
  - `TestEngineRecordsAgentMetrics` (`engine`): una ejecución con 1 reintento de recibo y 1 `follow_up` persiste `ReceiptRetries >= 1`, `FollowUps == 1`, `ActiveDurationMS > 0`, `ChecksPassed` y `ChecksFailed` en `state.json`.
  - `TestRunsShowsDurationColumn` (`app`): `app.Runs` añade columna `DURACIÓN` (usando `run.Summarize`) y muestra `-` cuando no hay agentes.
- [ ] **Step 2:** `go test ./internal/run/ ./internal/engine/ ./internal/app/` → FAIL.
- [ ] **Step 3: Implementar:**
  - Crear `internal/run/metrics.go` y ampliar `run.Agent`.
  - En `internal/engine/agents.go`: incrementar `ag.FollowUps` en `FollowUp`, `ag.ReceiptRetries` al reintentar recibo, acumular `ag.ActiveDurationMS` por cada `executeProvider`, y registrar `ChecksPassed`, `ChecksFailed` y `RefViolations` al finalizar verificación.
  - En `internal/engine/integrate.go`: añadir una línea compacta de resumen al mensaje de `afterIntegration` (manteniendo el texto previo con `integBranch` para no romper `integrate_test.go`).
  - Actualizar `internal/app/runs.go`.
- [ ] **Step 4:** `go test -race ./internal/run/ ./internal/engine/ ./internal/app/` → PASS.
- [ ] **Step 5: Commit** `feat(run,engine,app): métricas de ejecución por tarea y proveedor`.

---

### Task 2: Endurecimiento de worktrees obsoletos, semáforo y lectura acotada en `jsonl.go` + `agy.go`

**Files:**
- Modify: `internal/gitx/git.go`, `internal/provider/jsonl.go`, `internal/provider/agy.go`, `internal/engine/engine.go`, `internal/engine/agents.go`
- Test: `internal/gitx/git_test.go`, `internal/provider/claude_test.go`, `internal/provider/agy_test.go`, `internal/engine/engine_test.go`

**Interfaces:**
- Consumes: `gitx.Repo`, `bufio.Reader`.
- Produces:
  ```go
  // internal/provider/jsonl.go
  const maxJSONLLineBytes = 16 * 1024 * 1024 // 16 MiB
  var errLineTooLong = errors.New("línea JSONL excede el tamaño máximo permitido")
  func readLineLimited(r *bufio.Reader, limit int) ([]byte, error)
  ```
  - `readLineLimited` lee fragmentos con `ReadSlice('\n')` acumulando hasta `limit` bytes; si una línea supera `limit`, devuelve `errLineTooLong`, ambos runners (`jsonlRun.pump` y `agyRun.drive`) matan el proceso hijo y devuelven `ProviderError{Class: Crashed, Msg: ...}` sin consumir memoria ilimitada.
  - En `gitx.Repo.AddWorktree`: si `worktree add` falla y `path` no existe en disco, ejecutar `git worktree prune` y reintentar una sola vez antes de devolver error.
  - En `engine.Engine.semaphoreFor`: adquirir `e.mu` y guardar en `e.sems[provName]` si falta.

- [ ] **Step 1: Tests que fallan:**
  - `TestAddWorktreePrunesStaleRegistration` (`gitx`): crea un worktree, borra su carpeta manualmente con `os.RemoveAll` sin `git worktree remove`, vuelve a llamar `AddWorktree` en la misma ruta con nueva rama → pasa sin error.
  - `TestReadLineLimitedRejectsOversizedLine` (`provider`): línea de 2 MB con `\r\n` pasa con límite de 16 MB; línea mayor que el límite configurado en test devuelve `errLineTooLong` y en `jsonlRun`/`agyRun` produce `Final.Err.Class == Crashed` sin fuga de goroutines.
  - `TestEngineCloseIdempotent` (`engine`): llamar a `Close` dos veces seguidas no produce panic ni error y conserva un run con aprobaciones.
  - `TestSemaphoreForSharesChannelForUnknownProvider` (`engine`): dos llamadas concurrentes a `semaphoreFor("custom")` devuelven el mismo canal bajo `-race`.
- [ ] **Step 2:** `go test -race ./internal/gitx/ ./internal/provider/ ./internal/engine/` → FAIL.
- [ ] **Step 3: Implementar** `readLineLimited` (en `jsonl.go` y `agy.go`), retry con `prune` en `AddWorktree`, fix en `semaphoreFor` y verificación de idempotencia en `Engine.Close`.
- [ ] **Step 4:** `go test -race ./internal/gitx/ ./internal/provider/ ./internal/engine/` → PASS.
- [ ] **Step 5: Commit** `fix(gitx,provider,engine): límite JSONL en claude/agy, prune de worktrees y semáforo seguro`.

---

### Task 3: Higiene de `go.mod`, `.golangci.yml` (v2), `govulncheck` y guía de verificación

**Files:**
- Create: `.golangci.yml`
- Modify: `go.mod`, `go.sum`, `README.md` (y cualquier archivo `.go` con hallazgos reales de `gofmt`, `go vet` o `golangci-lint`)

**Interfaces:**
- Consumes: todo el módulo `github.com/ImKrav/weaverharness`.
- Produces:
  - `.golangci.yml` en formato **golangci-lint v2**:
    ```yaml
    version: "2"
    run:
      timeout: 5m
    linters:
      default: standard
      enable:
        - misspell
        - unconvert
      exclusions:
        rules:
          - path: _test\.go
            linters:
              - errcheck
    formatters:
      enable:
        - gofmt
    ```
  - `go.mod` y `go.sum` normalizados con `go mod tidy` (separando dependencias directas de `// indirect`).
  - `README.md` actualizado con comandos de verificación (`go test -race ./...`, `go vet ./...`, `golangci-lint run`, `govulncheck ./...`).

- [ ] **Step 1:** Ejecutar `go mod tidy`, `gofmt -l .`, `go vet ./...` y (si está disponible localmente) `golangci-lint run`.
- [ ] **Step 2:** Crear `.golangci.yml` y corregir cualquier advertencia real en el código (incluyendo formato `gofmt` y errores no comprobados en código de producción).
- [ ] **Step 3:** Verificar `go test -race ./...`, `go vet ./...` y `git diff --exit-code go.mod go.sum` tras `go mod tidy` → PASS.
- [ ] **Step 4:** Documentar los comandos de calidad y seguridad en `README.md`.
- [ ] **Step 5: Commit** `chore(lint): normalizar go.mod, añadir .golangci.yml v2 y guía de verificación`.

---

### Task 4: Pipeline de CI multiplataforma en GitHub Actions

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `go.mod`, `.golangci.yml`, `cmd/weaver`.
- Produces `.github/workflows/ci.yml` con:
  - `permissions: { contents: read }`
  - `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`
  - Triggers: `push` a `main` y `pull_request`.
  - **Job `test`** (`strategy.fail-fast: false`, `matrix.os: [ubuntu-latest, windows-latest, macos-latest]`):
    - `actions/checkout@v4`
    - `actions/setup-go@v5` (`go-version-file: go.mod`, `cache: true`)
    - Verificación de `go.mod`/`go.sum` en `ubuntu-latest`: `go mod tidy && git diff --exit-code go.mod go.sum`
    - Tests en Linux/macOS: `go test -race -shuffle=on -count=1 -timeout 10m ./...`
    - Tests en Windows: `go test -race -shuffle=on -count=1 -timeout 10m ./...` (con `CGO_ENABLED=1` usando `gcc` del runner; si el runner Windows no soporta `-race` para Go 1.27, caer explícitamente a `go test -shuffle=on -count=1 -timeout 10m ./...` y dejarlo documentado en un comentario del YAML)
  - **Job `cross-build`** (`ubuntu-latest`):
    - Compila `cmd/weaver` con `CGO_ENABLED=0` y `-ldflags "-s -w -X main.version=ci -X main.commit=${{ github.sha }}"` para `linux/amd64`, `windows/amd64` y `darwin/arm64`.
  - **Job `lint`** (`ubuntu-latest`):
    - `actions/checkout@v4` + `actions/setup-go@v5` (`go-version-file: go.mod`)
    - `golangci/golangci-lint-action@v7` con `version: latest`
  - **Job `security`** (`ubuntu-latest`):
    - `actions/checkout@v4` + `actions/setup-go@v5` (`go-version-file: go.mod`)
    - `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`

- [ ] **Step 1:** Crear `.github/workflows/ci.yml`.
- [ ] **Step 2:** Verificar localmente los comandos equivalentes del CI:
  - `go vet ./...`
  - `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/weaver`
  - `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/weaver`
  - `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/weaver`
  - Limpiar binarios temporales tras la comprobación.
- [ ] **Step 3: Commit** `ci: añadir workflow multiplataforma de tests, lint, govulncheck y cross-build`.
