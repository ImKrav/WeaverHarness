# WeaverHarness — Plan 6: Integración de herramientas externas, servidores MCP y skills

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Permitir configurar de forma declarativa servidores MCP externos (`[mcp_servers.*]`), herramientas extra del orquestador (`orchestrator_extra_tools`) y prompts/skills adicionales (`orchestrator_extra_prompts`, `worker_extra_prompts`, `skills_dirs`) en `config.toml`, propagándolos al orquestador y a los subagentes de los 4 proveedores sin romper los invariantes SDD/RDD.

**Architecture:** `internal/config` valida el esquema y protege los límites de seguridad (prohibiendo sobrescribir `weaver` o habilitar edición/bash sin filtro en el orquestador); `internal/provider` define el tipo neutro `MCPServer`, `RunRequest.MCPServers` y la traducción por adaptador (archivo temporal en `claude` worker, ACP v1 `mcpServers` en `agy`, `-c mcp_servers.<name>=...` TOML en `codex` y `OPENCODE_CONFIG_CONTENT` en `opencode`); `internal/mcpserver` fusiona `weaver` + externos en `mcp.json`; `internal/app` y `internal/engine` filtran por alcance (`orchestrator`/`workers`/`all`) y expanden `${ENV_VAR}` en runtime; `internal/prompts` apendiza prompts y `SKILL.md` sin sustituir el contrato `weaver-receipt`.

**Tech Stack:** Go 1.27 · `github.com/BurntSushi/toml` · `encoding/json` · Agent Client Protocol v1 (`agy acp`) · `claude`, `codex`, `opencode` CLIs.

**Base:** `docs/superpowers/specs/2026-11-27-external-tools-and-mcp-design.md` · `docs/superpowers/plans/2026-11-27-cross-platform-and-releases.md`

## Contexto del código existente y hallazgos clave

- **Orquestador aislado (`internal/app/session.go:30-38,244-261` y `internal/provider/claude.go:83-94`):** actualmente `mcpserver.WriteMCPConfig` solo escribe `"weaver"`, `claude` usa `--strict-mcp-config` y `AllowedTools` es una variable fija `OrchestratorTools`. Ampliarlo vía `config.toml` permite activar MCPs externos (p. ej. CodeGraph, Context7) y CLIs específicos (`Bash(codegraph *)`) manteniendo `--strict-mcp-config` y `--permission-mode dontAsk`.
- **Antigravity bloqueado (`internal/provider/agy.go:317-324`):** `session/new` (y `session/resume`) envía `"mcpServers": []any{}` fijo. Además, en **ACP v1** (`protocolVersion: 1`), `env` y `headers` son arrays de `{ "name", "value" }` y `stdio` no lleva `"type"` (solo `http` lleva `"type": "http"`).
- **Codex y OpenCode en workers (`internal/provider/codex.go`, `opencode.go`, `jsonl.go`):** `codex exec` acepta `-c mcp_servers.<name>={...}` en sintaxis tabla inline TOML; `opencode run` lee `OPENCODE_CONFIG_CONTENT` (`{"mcp":{...}}`) y `jsonl.go` requiere partir de `os.Environ()` al pasar `cfg.Env` para no perder `PATH`/`HOME`.
- **Overrides vs adición de prompts (`internal/prompts/prompts.go:53-90`):** `.weaver/prompts/orchestrator.md` y `worker.md` reemplazan todo el prompt. Añadir funciones `*WithExtras` conserva compatibilidad de firmas y mantiene intactos SDD/RDD y el bloque `weaver-receipt`.
- **Orden de dependencias entre paquetes:** `mcpserver` importa `engine`, y `engine` importa `provider` y `config`. Por tanto el tipo neutro `MCPServer` debe vivir en `internal/provider` (sin depender de `config` ni `mcpserver`).

## Global Constraints

- Preservar la compatibilidad de firmas existentes consumidas por tests externos (`mcpserver.WriteMCPConfig`, `prompts.Orchestrator`, `prompts.Worker`, `config.ApplyPreset`).
- Nunca expandir `${ENV_VAR}` al ejecutar `config show`/`RenderTOML`; expandir únicamente en memoria justo antes de lanzar el proceso hijo y escribir archivos de runtime con `0o600`.
- Mantener `gofmt`, `go test -count=1 ./...` (`-race` en Linux/macOS), `go vet ./...` (Windows/Linux/Darwin) y `golangci-lint run ./...` en `0 issues`.

---

### Task 1: Esquema en `config.toml`, validación de seguridad y clonado en presets

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `github.com/BurntSushi/toml`, `net/url`, `regexp`.
- Produces en `internal/config/config.go`:
  ```go
  type MCPServerCfg struct {
      Type     string            `toml:"type,omitempty"`
      Command  string            `toml:"command,omitempty"`
      Args     []string          `toml:"args,omitempty"`
      Env      map[string]string `toml:"env,omitempty"`
      URL      string            `toml:"url,omitempty"`
      Headers  map[string]string `toml:"headers,omitempty"`
      Scope    string            `toml:"scope,omitempty"`
      Disabled bool              `toml:"disabled,omitempty"`
  }
  ```
  - Campos en `Config`: `OrchestratorExtraTools []string`, `OrchestratorExtraPrompts []string`, `WorkerExtraPrompts []string`, `SkillsDirs []string`, `MCPServers map[string]MCPServerCfg`.
  - Validación en `validate`:
    - Nombres de `mcp_servers`: patrón `^[A-Za-z0-9_-]+$`, prohibido `"weaver"`.
    - `scope`: `""` normalizado o validado como `"orchestrator"` | `"workers"` | `"all"`.
    - `type`: inferido (`command`→`"stdio"`, `url`→`"http"`); mutua exclusión entre `stdio` (`command`/`args`) y `http` (`url`/`headers`); `url` con esquema `http` o `https`.
    - `orchestrator_extra_tools`: rechaza vacíos, `Edit`/`Write`/`MultiEdit`/`NotebookEdit` fuera de `./docs/`, y `Bash`/`Bash(*)`/`Bash(**)`/`Bash(* *)`.
  - `ApplyPreset`: clona profundamente slices y `MCPServers` para preservarlos al aplicar presets.

- [ ] **Step 1: Tests que fallan en `internal/config/config_test.go`:**
  - `TestLoadExternalMCPAndToolsRoundTrip`: carga capas `global.toml` + `project.toml` con `[mcp_servers.codegraph]`, `[mcp_servers.context7]`, `disabled = true`, `orchestrator_extra_tools = ["Bash(codegraph *)"]`, `orchestrator_extra_prompts`, `worker_extra_prompts`, `skills_dirs`; verifica valores, `ApplyPreset` preservando mapas/slices sin aliasing y `RenderTOML` round-trip.
  - `TestLoadRejectsUnsafeToolsAndInvalidMCP`: tabla de casos negativos — servidor `"weaver"`, nombre con punto `"bad.name"`, ambos `command`+`url`, ninguno, `url` no HTTP (`file://`), `scope` inválido, y herramientas peligrosas (`"Edit"`, `"Write(*)"`, `"Bash"`, `"Bash(*)"`, `"Bash(**)"`, `""`).
- [ ] **Step 2:** `go test ./internal/config/` → FAIL.
- [ ] **Step 3: Implementar** tipos, normalización/validación y clonado en `internal/config/config.go`.
- [ ] **Step 4:** `go test -count=1 ./internal/config/` → PASS.
- [ ] **Step 5: Commit** `feat(config): soportar mcp_servers, orchestrator_extra_tools y rutas de prompts/skills`.

---

### Task 2: `provider.MCPServer`, fusión en `mcpserver` y alcance en el orquestador (`app`)

**Files:**
- Create: `internal/provider/mcp.go`
- Modify: `internal/provider/provider.go`, `internal/mcpserver/server.go`, `internal/app/session.go`
- Test: `internal/mcpserver/server_test.go`, `internal/app/session_test.go`, `internal/provider/mcp_test.go`

**Interfaces:**
- Consumes: `config.Config`, `config.MCPServerCfg`, `os.LookupEnv`.
- Produces:
  - En `internal/provider/mcp.go`:
    ```go
    type MCPServer struct {
        Name    string
        Type    string            // "stdio" | "http"
        Command string
        Args    []string
        Env     map[string]string
        URL     string
        Headers map[string]string
    }
    ```
    y helper `ExpandEnvText(s string, lookup func(string) (string, bool)) (string, error)` para placeholders `${VAR}`.
  - En `internal/provider/provider.go`: añade `MCPServers []MCPServer` a `RunRequest`.
  - En `internal/mcpserver/server.go`:
    - `WriteMCPConfigWithServers(path, url, token string, extras []provider.MCPServer) error` escribe `"weaver"` + externos en formato Claude (`0o600`), rechazando cualquier intento de sobrescribir `"weaver"`.
    - `WriteMCPConfig(path, url, token string) error` delega en `WriteMCPConfigWithServers(path, url, token, nil)`.
  - En `internal/app/session.go`:
    - Helper `resolveMCPServers(cfg config.Config, targetScope string) ([]provider.MCPServer, error)` que filtra servidores activos (`!Disabled`), compara `scope` (`"all"` o igual a `targetScope`) y expande `${VAR}` en `URL`, `Env` y `Headers`.
    - Helper `buildOrchestratorAllowedTools(extraTools []string, orchServers []provider.MCPServer) []string` que combina `OrchestratorTools`, `"mcp__" + s.Name` y `extraTools` sin duplicados.
    - `RunSession` escribe `mcp.json` con `mcpserver.WriteMCPConfigWithServers` y pasa `AllowedTools` resultante al orquestador.

- [ ] **Step 1: Tests que fallan:**
  - `TestExpandEnvText` (`provider`): expande `${TOKEN}`, falla con nombre de variable si falta, deja texto literal sin `${...}` intacto.
  - `TestWriteMCPConfigWithServersMergesAndProtectsWeaver` (`mcpserver`): serializa `"weaver"` junto a un servidor `stdio` (`command`, `args`, `env`) y uno `http` (`url`, `headers`), y falla si un extra intenta usar nombre `"weaver"`.
  - `TestOrchestratorLaunchIncludesConfiguredMCPAndExtraTools` (`app`): con `[mcp_servers.codegraph]` (`scope="all"`), `[mcp_servers.onlyworker]` (`scope="workers"`), `[mcp_servers.off]` (`disabled=true`) y `orchestrator_extra_tools = ["Bash(codegraph *)"]`, `RunSession` headless escribe `codegraph` (sin `onlyworker` ni `off`) en `mcp.json` y pasa `mcp__codegraph` y `Bash(codegraph *)` en `--allowedTools`.
- [ ] **Step 2:** `go test ./internal/provider/ ./internal/mcpserver/ ./internal/app/` → FAIL.
- [ ] **Step 3: Implementar** `internal/provider/mcp.go`, `WriteMCPConfigWithServers` y cableado en `internal/app/session.go`.
- [ ] **Step 4:** `go test -count=1 ./internal/provider/ ./internal/mcpserver/ ./internal/app/` → PASS.
- [ ] **Step 5: Commit** `feat(mcp,app): propagar mcp_servers y orchestrator_extra_tools al orquestador`.

---

### Task 3: Propagación de MCP externos a `engine` y a los 4 adaptadores de subagentes

**Files:**
- Modify: `internal/engine/agents.go`, `internal/provider/mcp.go`, `internal/provider/jsonl.go`, `internal/provider/claude.go`, `internal/provider/agy.go`, `internal/provider/codex.go`, `internal/provider/opencode.go`
- Test: `internal/engine/agents_test.go`, `internal/provider/claude_test.go`, `internal/provider/agy_test.go`, `internal/provider/codex_test.go`, `internal/provider/opencode_test.go`

**Interfaces:**
- Consumes: `provider.RunRequest.MCPServers`, `os.Environ()`.
- Produces:
  - En `internal/engine/agents.go`: helper `(e *Engine) workerMCPServers() ([]provider.MCPServer, error)` usado en `Spawn`, `FollowUp` y `retryReq` (receipt retry); falla con error legible si falta una `${VAR}` referenciada.
  - En `internal/provider/jsonl.go`: `Cleanup func()` opcional en `jsonlConfig`, ejecutado mediante `sync.Once` al finalizar `proc.Wait()` o al fallar arranque.
  - En `internal/provider/mcp.go`: helpers testeables por adaptador:
    - `writeClaudeMCPTemp(servers []MCPServer) (path string, cleanup func(), err error)`
    - `buildACPMCPServers(servers []MCPServer, httpSupported bool) (included []map[string]any, skippedHTTP []string)` (ACP v1: `stdio` sin `"type"`, `env`/`headers` como `[{"name","value"}]`; `http` con `"type":"http"`).
    - `buildCodexMCPArgs(servers []MCPServer) []string` (pares `"-c"`, `"mcp_servers.<name>={...}"` en TOML válido).
    - `buildOpenCodeEnv(baseEnv []string, servers []MCPServer) ([]string, error)` (parte de `baseEnv` / `os.Environ()`, fusiona `"mcp"` dentro de `OPENCODE_CONFIG_CONTENT`).
  - Cableado en `claude.go` (Worker: `--mcp-config <tmp>` sin `--strict-mcp-config`), `agy.go` (`initialize` lee `agentCapabilities.mcpCapabilities.http` cuando venga informado y envía `mcpServers` en `session/new` y `session/resume`), `codex.go` (`buildCodexArgs` en `exec` y `exec resume`) y `opencode.go` (`cfg.Env` cuando `len(req.MCPServers) > 0`).

- [ ] **Step 1: Tests que fallan:**
  - `TestSpawnPassesWorkerMCPServersAndFiltersOrchestratorOnly` (`engine`): verifica que `Spawn` pasa a `Provider.Start` los servidores `workers`/`all` y excluye `orchestrator` y `disabled`.
  - `TestClaudeWorkerMCPConfigTempAndCleanup` (`provider`): con `Mode: Worker` y `MCPServers` no vacío, pasa `--mcp-config <file>` sin `--strict-mcp-config`, el archivo JSON existe durante la ejecución y se elimina tras `Wait()`.
  - `TestAgyWorkerSendsACPMCPServers` (`provider`): verifica `stdin.jsonl` de `fakecli` ACP: `stdio` sin `"type"` con `env: [{"name":...,"value":...}]` y `http` con `"type":"http"` + `headers: [{"name":...,"value":...}]`, más caso `agentCapabilities.mcpCapabilities.http = false` omitiendo solo el `http`.
  - `TestCodexWorkerMCPConfigArgs` (`provider`): verifica `-c mcp_servers.<name>={...}` en `exec` y `exec resume` (sin alterar args cuando `MCPServers` está vacío).
  - `TestOpenCodeWorkerMCPConfigEnv` (`provider`): verifica que `OPENCODE_CONFIG_CONTENT` incluye `"mcp"` (`local`/`remote`) y preserva el resto del entorno (`PATH`, JSON previo).
- [ ] **Step 2:** `go test ./internal/engine/ ./internal/provider/` → FAIL.
- [ ] **Step 3: Implementar** helpers en `internal/provider/mcp.go`, `Cleanup` en `jsonl.go`, los 4 adaptadores y `workerMCPServers()` en `engine/agents.go`.
- [ ] **Step 4:** `go test -count=1 ./internal/engine/ ./internal/provider/` → PASS.
- [ ] **Step 5: Commit** `feat(engine,provider): propagar mcp_servers en claude, antigravity, codex y opencode`.

---

### Task 4: Carga segura de `extra_prompts` y `skills_dirs` en `prompts`, `app` y `engine`

**Files:**
- Modify: `internal/prompts/prompts.go`, `internal/app/session.go`, `internal/engine/agents.go`
- Test: `internal/prompts/prompts_test.go`, `internal/app/session_test.go`, `internal/engine/agents_test.go`

**Interfaces:**
- Consumes: `os.ReadFile`, `os.ReadDir`, `os.UserHomeDir`, `config.Config`.
- Produces en `internal/prompts/prompts.go`:
  ```go
  type ExtraOptions struct {
      PromptFiles []string
      SkillsDirs  []string
  }
  func OrchestratorWithExtras(projectDir string, opts ExtraOptions) (string, error)
  func WorkerWithExtras(projectDir string, d WorkerData, opts ExtraOptions) (string, error)
  func ValidateExtras(projectDir string, opts ExtraOptions) ([]string, error) // usado por doctor
  ```
  - Resuelve `~/...` y rutas relativas a `projectDir`.
  - `SkillsDirs`: carga `<dir>/SKILL.md` si existe, o `<dir>/*/SKILL.md` ordenado por nombre; falla si el directorio falta o no tiene skills; límite total `256 KiB` de extras por prompt.
  - Apendiza el bloque adicional tras `Orchestrator(projectDir)` / `Worker(projectDir, d)` conservando las reglas SDD/RDD y `weaver-receipt`.
  - `app/session.go` llama a `prompts.OrchestratorWithExtras` con `OrchestratorExtraPrompts` + `SkillsDirs`; `engine/agents.go` llama a `prompts.WorkerWithExtras` con `WorkerExtraPrompts` + `SkillsDirs`.

- [ ] **Step 1: Tests que fallan:**
  - `TestPromptsWithExtrasAndSkillsDirs` (`prompts`): carga `PromptFiles` + `SkillsDirs` (tanto skill directo como carpeta con subcarpetas `a/SKILL.md`, `b/SKILL.md`), expande `~/`, conserva bloque base SDD/RDD y `weaver-receipt`, y falla con error claro ante ruta inexistente, carpeta sin `SKILL.md` o exceso de `256 KiB`.
  - `TestRunSessionIncludesOrchestratorExtraPrompt` (`app`) y `TestSpawnIncludesWorkerExtraPromptAndSkills` (`engine`): verifican extremo a extremo que el prompt escrito para el orquestador y para el worker incluye las instrucciones configuradas en `config.toml`.
- [ ] **Step 2:** `go test ./internal/prompts/ ./internal/app/ ./internal/engine/` → FAIL.
- [ ] **Step 3: Implementar** `ExtraOptions`, `*WithExtras`, `ValidateExtras` y cableado en `app/session.go` y `engine/agents.go`.
- [ ] **Step 4:** `go test -count=1 ./internal/prompts/ ./internal/app/ ./internal/engine/` → PASS.
- [ ] **Step 5: Commit** `feat(prompts,app,engine): cargar extra_prompts y skills_dirs conservando reglas SDD/RDD`.

---

### Task 5: Diagnóstico en `weaver doctor`, documentación en `README.md` y verificación integral

**Files:**
- Modify: `internal/app/doctor.go`, `README.md`
- Test: `internal/app/doctor_test.go` (o `internal/app/session_test.go`)

**Interfaces:**
- Consumes: `provider.ExpandEnvText` / `exec.LookPath` / `prompts.ValidateExtras`.
- Produces:
  - En `internal/app/doctor.go`: cuando `cfg.MCPServers`, `cfg.OrchestratorExtraPrompts`, `cfg.WorkerExtraPrompts` o `cfg.SkillsDirs` no están vacíos, imprime un resumen claro de estado/advertencias (binario `command` faltante en servidores `stdio` activos, variables `${VAR}` sin definir, o rutas de prompts/skills inaccesibles) sin romper el contrato de exit code del orquestador.
  - En `README.md`: sección práctica **"Herramientas externas, MCP y Skills"** con ejemplos listos para copiar de **CodeGraph** (`stdio` + `Bash(codegraph *)`), **Superpowers** (`skills_dirs`) y servidores MCP HTTP con `${TOKEN}`, además de tabla rápida de soporte por proveedor.

- [ ] **Step 1: Tests que fallan:**
  - `TestDoctorReportsExternalMCPAndPromptChecks` (`app`): con `[mcp_servers.codegraph]` apuntando a un binario inexistente y un `skills_dirs` inexistente, `Doctor` reporta advertencias claras con el nombre del servidor y la ruta problemática.
- [ ] **Step 2:** `go test ./internal/app/` → FAIL.
- [ ] **Step 3: Implementar** verificaciones externas en `internal/app/doctor.go` y actualizar `README.md`.
- [ ] **Step 4: Verificación final:**
  - `gofmt -l ./cmd ./internal` → vacío.
  - `go test -count=1 ./...` → PASS en todos los paquetes.
  - `go vet ./...`, `GOOS=linux go vet ./...` y `GOOS=darwin go vet ./...` → PASS.
  - `golangci-lint run ./...` → `0 issues`.
- [ ] **Step 5: Commit** `feat(doctor,docs): validar herramientas externas en doctor y documentar MCP/skills`.
