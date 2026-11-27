# WeaverHarness — Plan 5: Compatibilidad multiplataforma (Linux, macOS y Windows) y Releases

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hacer que WeaverHarness funcione de forma predecible y robusta en **Linux, macOS y Windows**, corrigiendo divergencias reales de symlinks en macOS, manejo de `SIGTERM`/`SIGINT` al cerrar sesiones, descubrimiento de binarios de proveedores fuera del `PATH`, normalización de rutas y contexto de SO en verificación/prompts, ergonomía TUI (`F2` para ratón en vivo) y pipeline de releases multiplataforma con GoReleaser.

**Architecture:** Cada ajuste permanece desacoplado por capa: `internal/gitx` y `internal/verify` normalizan symlinks y rutas estilo Windows; `cmd/weaver` + `internal/app` propagan `signal.NotifyContext` a `tea.WithContext`; `internal/provider` añade un resolvedor de binarios consciente de rutas estándar por SO; `internal/prompts` inyecta `runtime.GOOS` y el shell nativo sin romper firmas públicas; `internal/tui` añade alternancia en caliente de captura de ratón; `.goreleaser.yml` y `.github/workflows/release.yml` automatizan la publicación de binarios.

**Tech Stack:** Go 1.27 · Bubble Tea (`tea.WithContext`, `tea.DisableMouse`, `tea.EnableMouseCellMotion`) · `git` worktrees · GoReleaser v2 · GitHub Actions (`ubuntu-latest`, `macos-latest`, `windows-latest`).

**Base:** `docs/superpowers/specs/2026-11-26-weaverharness-design.md` · `docs/superpowers/plans/2026-11-27-observability-robustness-ci.md`

## Contexto del código existente y hallazgos clave

- **Symlinks en `gitx.Guard` vs `app/clean.go`:** `internal/app/clean.go` resuelve `filepath.EvalSymlinks` dentro de `normPath`, pero `internal/gitx/guard.go:238-244` solo ejecuta `filepath.Clean(filepath.FromSlash(p))`. En macOS (`/tmp` → `/private/tmp`, `/var` → `/private/var`), `git worktree list --porcelain` reporta rutas reales mientras los tests o llamadas con symlinks usan la ruta sin resolver, rompiendo la comparación en `snap.Heads`.
- **Sesiones TUI sin cancelación por señales:** `cmd/weaver/main.go` usa `signal.NotifyContext(context.Background(), os.Interrupt)` en `clean` y `doctor`, pero la sesión principal (`weaver` y `weaver resume`) pasa `context.Background()` sin `os.Interrupt` ni `syscall.SIGTERM` (en `!windows`). Al recibir `SIGTERM` en Linux/macOS no se ejecuta ordenadamente `tea.Quit`/`Kill`/`eng.Close`.
- **Resolución de CLIs solo por `exec.LookPath`:** en macOS (Homebrew `/opt/homebrew/bin`, `Antigravity.app`, `nvm`/`fnm`) y Linux (`~/.local/bin`, `~/.opencode/bin`, `npm -g`), sesiones lanzadas fuera de un shell interactivo pueden fallar `LookPath` aunque el CLI esté instalado; en Windows los shims `.cmd` requieren coexistir limpiamente si existe `.exe`.
- **Rutas con `\` en `verify.compareChanges` y prompts agnósticos de SO:** `internal/verify/verify.go:128` solo elimina `./` antes de `filepath.ToSlash`, dejando `.\archivo.go` como `./archivo.go` en recibos generados en Windows. Además `orchestrator.md` y `worker.md` describen ambos shells pero no informan al modelo en qué SO real está ejecutándose.
- **Ctrl+M vs `F2` en TUI:** en terminales Unix/macOS/Windows, `Ctrl+M` equivale a `Enter` (`\r`), por lo que el atajo en vivo para alternar captura de ratón debe usar `F2` (`tea.KeyF2`) sin interferir con el `textarea`.

## Global Constraints

- Toda lógica específica de sistema operativo se aísla mediante build tags (`//go:build windows` / `//go:build !windows`) o funciones puras parametrizables por `goos` para poder testear reglas de los 3 SOs desde cualquier máquina.
- No romper firmas existentes consumidas por otros paquetes (`prompts.Orchestrator(projectDir)`, `prompts.Worker(projectDir, d)`, `runMainWith` con wrapper compatible o actualización de sus tests).
- Mantener `go test -count=1 ./...` (`-race` en Linux/macOS CI), `go vet ./...` y `golangci-lint run ./...` en `0 issues`.

---

### Task 1: Normalización de symlinks en `gitx.Guard` y rutas Windows en `verify`

**Files:**
- Modify: `internal/gitx/guard.go`, `internal/verify/verify.go`
- Test: `internal/gitx/guard_test.go`, `internal/verify/verify_test.go`

**Interfaces:**
- Consumes: `filepath.EvalSymlinks`, `gitx.Repo.DiffNames`, `receipt.Receipt`.
- Produces:
  - En `internal/gitx/guard.go`: `normPath(p string) string` resuelve `filepath.Abs` + `filepath.EvalSymlinks` (si existe en disco; fallback limpio si no) antes de `filepath.Clean` y `strings.ToLower` en Windows.
  - En `internal/verify/verify.go`: helper `normalizeReceiptPath(raw string) string` que convierte `\` → `/`, aplica `path.Clean` y quita `./` inicial, usado en `compareChanges`.

- [ ] **Step 1: Tests que fallan:**
  - `TestGuardHandlesSymlinkedRepoPath` (`gitx`): crea un repo en `realDir`, crea un symlink `linkDir -> realDir` (usando `t.Skip` únicamente si el entorno Windows carece de privilegio para symlinks) y crea un worktree `weaver/r1/a1` abriendo `gitx.Open(linkDir)`; `g.Check(ctx)` no debe reportar falsas violaciones `worktree_head_changed` ni `moved`.
  - `TestCompareChangesNormalizesBackslashesAndDotPrefix` (`verify`): recibo con `.\hello.txt` y `sub\file.go` comparado contra `git diff --name-only` (`hello.txt`, `sub/file.go`) produce `OnlyInReceipt` y `OnlyInDiff` vacíos.
- [ ] **Step 2:** `go test ./internal/gitx/ ./internal/verify/` → FAIL.
- [ ] **Step 3: Implementar** la resolución de symlinks en `gitx.normPath` y `normalizeReceiptPath` en `internal/verify/verify.go`.
- [ ] **Step 4:** `go test -count=1 ./internal/gitx/ ./internal/verify/` → PASS.
- [ ] **Step 5: Commit** `fix(gitx,verify): resolver symlinks en Guard y normalizar rutas Windows en recibos`.

---

### Task 2: Manejo de señales (`SIGINT` + `SIGTERM`) y apagado limpio de sesiones

**Files:**
- Create: `cmd/weaver/signals_unix.go`, `cmd/weaver/signals_windows.go`
- Modify: `cmd/weaver/main.go`, `internal/app/session.go`
- Test: `cmd/weaver/main_test.go`, `internal/app/session_test.go`

**Interfaces:**
- Consumes: `os/signal`, `syscall.SIGTERM` (`!windows`), `tea.WithContext`, `tea.ErrProgramKilled`.
- Produces:
  ```go
  // cmd/weaver/signals_unix.go (//go:build !windows) y signals_windows.go (//go:build windows)
  func shutdownSignals() []os.Signal
  ```
  - `cmd/weaver/main.go`: `signal.NotifyContext(context.Background(), shutdownSignals()...)` propagado a `app.RunSession(ctx, ...)`, `app.Clean(ctx, ...)` y `app.Doctor(ctx, ...)`.
  - `internal/app/session.go`: `tea.NewProgram` incluye `tea.WithContext(ctx)`. Si `ctx.Err() != nil` y `errors.Is(runErr, tea.ErrProgramKilled)`, `RunSession` ejecuta el `defer` de limpieza (`orchRun.Kill()`, `eng.Close(context.Background())`) y devuelve `nil` (o error contextualizado sin crash sucio).

- [ ] **Step 1: Tests que fallan:**
  - `TestShutdownSignalsNonEmpty` (`cmd/weaver`): verifica que `shutdownSignals()` incluye `os.Interrupt` (y en `!windows` `syscall.SIGTERM`).
  - `TestRunSessionHeadlessCancelsCleanlyOnContextCancel` (`app`): cancelar `ctx` mientras corre una sesión `RunSession` (simulando `SIGINT`/`SIGTERM`) mata el proceso falso, cierra el `Engine` (conservando estado persistido) y retorna en `< 2s`.
- [ ] **Step 2:** `go test ./cmd/weaver ./internal/app/` → FAIL.
- [ ] **Step 3: Implementar** `shutdownSignals()`, propagación de `ctx` en `cmd/weaver/main.go` y `tea.WithContext(ctx)` + manejo limpio en `internal/app/session.go`.
- [ ] **Step 4:** `go test -count=1 ./cmd/weaver ./internal/app/` → PASS.
- [ ] **Step 5: Commit** `fix(cli,app): manejar SIGTERM/SIGINT en sesiones y cerrar procesos hijos limpiamente`.

---

### Task 3: Descubrimiento multiplataforma de CLIs de proveedores y diagnóstico en `doctor`

**Files:**
- Create: `internal/provider/resolve.go`, `internal/provider/resolve_test.go`
- Modify: `internal/provider/claude.go`, `internal/provider/codex.go`, `internal/provider/opencode.go`, `internal/provider/agy.go`, `internal/provider/jsonl.go`, `internal/app/doctor.go`
- Test: `internal/provider/resolve_test.go`, `internal/app/doctor_test.go` (nuevo o existente si aplica)

**Interfaces:**
- Consumes: `exec.LookPath`, `runtime.GOOS`, `os.UserHomeDir`, variables de entorno (`LOCALAPPDATA`, `APPDATA`, `HOME`, `XDG_BIN_HOME`).
- Produces:
  ```go
  // internal/provider/resolve.go
  func resolveBinary(providerName, bin string) (string, error)
  func candidatePaths(goos, home string, getenv func(string) string, providerName, bin string) []string
  func InstallHint(goos, providerName string) string
  ```
  - Si `bin` contiene separador de ruta o `exec.LookPath(bin)` lo encuentra (en Windows prefiriendo `.exe` si hay ambigüedad o respetando `PATHEXT`), devuelve ese resultado.
  - Si `exec.LookPath(bin)` falla para un nombre por defecto sin separadores, inspecciona rutas estándar del SO:
    - `darwin`: `/opt/homebrew/bin/<bin>`, `/usr/local/bin/<bin>`, `~/.local/bin/<bin>`, `~/.opencode/bin/<bin>`, `~/.npm-global/bin/<bin>`, y para `antigravity` (`/Applications/Antigravity.app/Contents/Resources/app/bin/agy`).
    - `linux`: `~/.local/bin/<bin>`, `~/.opencode/bin/<bin>`, `~/.npm-global/bin/<bin>`, `/usr/local/bin/<bin>`, `/home/linuxbrew/.linuxbrew/bin/<bin>`.
    - `windows`: `%LOCALAPPDATA%\Programs\...`, `%APPDATA%\npm\<bin>.cmd`, `%USERPROFILE%\.local\bin\<bin>.exe`, `%USERPROFILE%\.opencode\bin\<bin>.exe`.
  - `app.Doctor`: cuando un CLI no está instalado, añade en `NOTAS` una pista accionable usando `provider.InstallHint(runtime.GOOS, name)` (incluyendo ejemplo de `[providers.<name>] bin = ...`); en Windows además comprueba `git config --get core.longpaths` y emite nota informativa si no es `true`.

- [ ] **Step 1: Tests que fallan:**
  - `TestCandidatePathsByOS` (`provider`): verifica rutas candidatas generadas para `darwin`, `linux` y `windows`, incluyendo `Antigravity.app` en `darwin`, `~/.local/bin` en `linux`/`darwin` y `%APPDATA%\npm\codex.cmd` en `windows`.
  - `TestResolveBinaryFallsBackToCandidateAndRejectsMissingExplicitPath` (`provider`): un binario en un directorio temporal candidato (no en `PATH`) se resuelve si coincide con el nombre por defecto, mientras que una ruta explícita inexistente falla inmediatamente sin fallback.
  - `TestDoctorShowsInstallHintWhenMissing` (`app`): con un proveedor configurado a binario inexistente, `Doctor` incluye sugerencia de configuración en `NOTAS`.
- [ ] **Step 2:** `go test ./internal/provider/ ./internal/app/` → FAIL.
- [ ] **Step 3: Implementar** `resolveBinary`, integrarlo en `claude.go`, `codex.go`, `opencode.go`, `agy.go`, `jsonl.go`, y actualizar `internal/app/doctor.go`.
- [ ] **Step 4:** `go test -count=1 ./internal/provider/ ./internal/app/` → PASS.
- [ ] **Step 5: Commit** `feat(provider,app): búsqueda multiplataforma de CLIs fuera del PATH y pistas en doctor`.

---

### Task 4: Prompts conscientes del sistema operativo y tests de shell en `verify`

**Files:**
- Create: `internal/prompts/prompts_test.go`
- Modify: `internal/prompts/prompts.go`, `internal/prompts/orchestrator.md`, `internal/prompts/worker.md`
- Test: `internal/prompts/prompts_test.go`, `internal/verify/verify_test.go`

**Interfaces:**
- Consumes: `runtime.GOOS`, `runtime.GOARCH`.
- Produces:
  - En `internal/prompts/prompts.go`:
    - `WorkerData` añade campos opcionales `TargetOS string` y `TargetShell string` (si vienen vacíos, `Worker` los rellena automáticamente para `runtime.GOOS`).
    - `Orchestrator(projectDir string)` inyecta bloque de entorno nativo (`targetOS` y `shell` — `cmd /s /c` en `windows` vs `sh -c` POSIX en `linux`/`darwin`) preservando overrides locales de `.weaver/prompts/orchestrator.md`.
    - Helper interno `platformContext(goos string) (osLabel, shellLabel string)` testeable para `windows`, `linux` y `darwin`.

- [ ] **Step 1: Tests que fallan:**
  - `TestPromptsIncludeOSAndShellContext` (`prompts`): verifica `platformContext` para `windows`, `linux` y `darwin`, y comprueba que tanto `Orchestrator("")` como `Worker("", WorkerData{...})` incluyen el SO y shell efectivos en el texto generado (manteniendo intactos los overrides de `.weaver/prompts/`).
  - `TestRunCheckHandlesQuotedArgsOnCurrentOS` (`verify`): ejecuta un `RunCheck` con argumentos entrecomillados en el worktree (`go env GOOS` / comando `echo "hola mundo"`) verificando exit code `0` y salida esperada en Windows, Linux y macOS.
- [ ] **Step 2:** `go test ./internal/prompts/ ./internal/verify/` → FAIL.
- [ ] **Step 3: Implementar** `platformContext`, actualizar `orchestrator.md`/`worker.md` y añadir tests en `prompts_test.go` y `verify_test.go`.
- [ ] **Step 4:** `go test -count=1 ./internal/prompts/ ./internal/verify/` → PASS.
- [ ] **Step 5: Commit** `feat(prompts,verify): inyectar SO y shell nativo en prompts y validar comillas multiplataforma`.

---

### Task 5: Toggle de ratón en vivo (`F2`), documentación por SO y pipeline de Releases (GoReleaser)

**Files:**
- Create: `.goreleaser.yml`, `.github/workflows/release.yml`
- Modify: `internal/tui/model.go`, `internal/tui/view.go`, `internal/app/session.go`, `README.md`, `.github/workflows/ci.yml`
- Test: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `tea.DisableMouse`, `tea.EnableMouseCellMotion`, `goreleaser/goreleaser-action@v6` (o `v7` según esquema vigente), variables `main.version`, `main.commit`, `main.date` de `cmd/weaver/main.go`.
- Produces:
  - En `tui.Options` y `tui.Model`: campo `MouseEnabled bool` (por defecto `true` salvo `--no-mouse` en `session.go`); tecla `F2` (`tea.KeyF2`) alterna `m.mouseEnabled`, emite `tea.DisableMouse` o `tea.EnableMouseCellMotion`, registra nota breve en el chat y actualiza el footer (`F2 ratón: on/off`).
  - `.goreleaser.yml` (`version: 2`): compila `./cmd/weaver` con `CGO_ENABLED=0` para `linux`/`darwin`/`windows` en `amd64` y `arm64`, `-ldflags "-s -w -X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}}"`, archivos `.tar.gz` (`.zip` en Windows) y `checksums.txt`.
  - `.github/workflows/release.yml`: disparo en tags `v*`, `permissions: contents: write`, `goreleaser release --clean`.
  - `.github/workflows/ci.yml`: añade `linux/arm64` al job `cross-build` para cubrir ARM64 en Linux junto a `darwin/arm64`.
  - `README.md`: notas por SO (macOS `Option as Esc+` vs `Ctrl+J`, rutas `PATH` de CLIs, `core.longpaths` en Windows, comandos smoke en `bash`/`zsh` y PowerShell).

- [ ] **Step 1: Tests que fallan:**
  - `TestToggleMouseWithF2` (`tui`): pulsando `tea.KeyF2` en un `Model` con `MouseEnabled: true`, desactiva `mouseEnabled`, añade mensaje informativo y devuelve `tea.Cmd` no nulo; pulsando `tea.KeyF2` de nuevo lo reactiva.
- [ ] **Step 2:** `go test ./internal/tui/` → FAIL.
- [ ] **Step 3: Implementar** toggle `F2` en `tui`, cableado desde `app/session.go`, `.goreleaser.yml`, `.github/workflows/release.yml`, ajuste de `ci.yml` y actualización de `README.md`.
- [ ] **Step 4: Verificación final:**
  - `go test -count=1 ./...` → PASS.
  - `go vet ./...` y `GOOS=linux go vet ./...` y `GOOS=darwin go vet ./...` → PASS.
  - `golangci-lint run ./...` → `0 issues`.
  - Cross-build local `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64` con `CGO_ENABLED=0` (limpiando binarios temporales).
- [ ] **Step 5: Commit** `feat(tui,release,docs): toggle de ratón F2, guía Linux/macOS/Windows y GoReleaser`.
