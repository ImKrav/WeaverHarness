# WeaverHarness — Integración de herramientas externas, servidores MCP y skills

Fecha: 2026-11-27 · Estado: borrador para aprobación  
Base: `docs/superpowers/specs/2026-11-26-weaverharness-design.md`

## 1. Intención

Permitir que cualquier usuario conecte herramientas del ecosistema (por ejemplo **CodeGraph**, **Context7**, **GitHub MCP**, **Ponytail**, **Superpowers** o prompts/skills locales) de forma declarativa en `config.toml` (global o `.weaver/config.toml`), de modo que funcionen de manera predecible tanto en el **orquestador** como en los **subagentes de los 4 proveedores** (`claude`, `codex`, `antigravity`, `opencode`).

Actualmente:
- El orquestador (`claude`) arranca con `--strict-mcp-config` apuntando solo al servidor interno `weaver`, y una lista fija `--allowedTools` en `internal/app/session.go`. Esto aísla bien el flujo SDD/RDD, pero bloquea servidores MCP y CLIs externos (como `codegraph`) salvo editando código Go.
- El adaptador `antigravity` (`internal/provider/agy.go`) envía `"mcpServers": []any{}` fijo en `session/new`, impidiendo cualquier MCP externo en subagentes Gemini/Antigravity.
- Los overrides `.weaver/prompts/orchestrator.md` y `.weaver/prompts/worker.md` **reemplazan** todo el prompt base, obligando a duplicar reglas SDD/RDD o el bloque `weaver-receipt` si solo se quieren añadir convenciones o skills externas.

## 2. Objetivos e invariantes de seguridad

**Objetivos**
1. Declarar servidores MCP externos bajo `[mcp_servers.<nombre>]` en `config.toml` (`stdio` o `http`) con alcance configurable (`orchestrator`, `workers`, `all`) y flag `disabled`.
2. Permitir ampliar las herramientas del orquestador mediante `orchestrator_extra_tools` en `config.toml` (p. ej. `Bash(codegraph *)`, herramientas de lectura/MCP específicas).
3. Propagar los MCP de alcance `workers`/`all` de manera uniforme a los 4 adaptadores de worker (`claude`, `antigravity`, `codex`, `opencode`).
4. Cargar archivos de instrucciones y directorios de skills (`orchestrator_extra_prompts`, `worker_extra_prompts`, `skills_dirs`) **añadiéndolos** al prompt base sin sobrescribir las reglas SDD/RDD ni el recibo obligatorio.
5. Reportar en `weaver doctor` si faltan binarios `stdio` o rutas de prompts/skills configuradas.

**Invariantes no negociables**
- **Servidor `weaver` reservado:** `[mcp_servers.weaver]` queda prohibido; el servidor interno HTTP + Bearer token efímero siempre prevalece y nunca puede ser suplantado.
- **Orquestador no codificador:** `validate` rechaza entradas peligrosas en `orchestrator_extra_tools` (`Edit`, `Write`, `MultiEdit`, `NotebookEdit` fuera de `./docs/`, o `Bash` / `Bash(*)` / `Bash(**)` sin filtro de comando específico). `--strict-mcp-config` se mantiene activo en el orquestador para evitar cargas implícitas no declaradas.
- **Sin fuga ni manejo de credenciales:** WeaverHarness nunca lee credenciales de CLIs externos ni imprime secretos resueltos en `weaver config show`. Las referencias `${ENV_VAR}` en `env`, `headers` o `url` se expanden únicamente al construir la configuración en tiempo de ejecución para el proceso hijo (`0o600` en archivos temporales/`mcp.json`).
- **Reglas SDD/RDD protegidas:** Los archivos y skills externos se **apendizan** tras el prompt efectivo (`Orchestrator`/`Worker`), nunca reemplazan las instrucciones de `weaver-receipt` ni el protocolo SDD.

---

## 3. Esquema en `config.toml` (`internal/config`)

Ejemplo completo en `.weaver/config.toml` o `~/.config/weaver/config.toml`:

```toml
orchestrator_extra_tools = [
  "Bash(codegraph *)",
]

orchestrator_extra_prompts = [
  ".weaver/prompts/arquitectura-equipo.md",
]

worker_extra_prompts = [
  ".weaver/prompts/go-conventions.md",
]

skills_dirs = [
  ".agents/skills",
  "~/.claude/skills/superpowers",
]

[mcp_servers.codegraph]
type = "stdio"
command = "codegraph"
args = ["mcp"]
scope = "all" # "orchestrator" | "workers" | "all" (por defecto "all")

[mcp_servers.context7]
type = "http"
url = "https://mcp.context7.com/mcp"
headers = { Authorization = "Bearer ${CONTEXT7_TOKEN}" }
scope = "workers"
disabled = false
```

### 3.1 Tipos Go en `internal/config`

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

Campos añadidos a `config.Config`:
- `OrchestratorExtraTools []string` (`toml:"orchestrator_extra_tools,omitempty"`)
- `OrchestratorExtraPrompts []string` (`toml:"orchestrator_extra_prompts,omitempty"`)
- `WorkerExtraPrompts []string` (`toml:"worker_extra_prompts,omitempty"`)
- `SkillsDirs []string` (`toml:"skills_dirs,omitempty"`)
- `MCPServers map[string]MCPServerCfg` (`toml:"mcp_servers,omitempty"`)

### 3.2 Reglas de validación (`config.validate`)

1. **Nombre del servidor (`[mcp_servers.<nombre>]`):**
   - Obligatorio, coincide con `^[A-Za-z0-9_-]+$` (evita colisiones con separadores de `codex -c mcp_servers.<nombre>` y nombres inválidos en Claude `mcp__<nombre>`).
   - No puede ser `"weaver"` (reservado).
2. **`scope`:** `""` equivale a `"all"`; solo se admiten `"orchestrator"`, `"workers"` o `"all"`.
3. **`type` y transporte:**
   - Si `type == ""`: se infiere `"stdio"` cuando solo `command != ""`, o `"http"` cuando solo `url != ""`.
   - Solo se admiten `"stdio"` o `"http"`.
   - Si `type == "stdio"`: `command` no vacío (sin `url` ni `headers`).
   - Si `type == "http"`: `url` no vacía con esquema `http://` o `https://` (sin `command` ni `args`).
4. **`orchestrator_extra_tools`:**
   - Se rechazan entradas vacías o solo espacios.
   - Se rechazan herramientas de edición general (`Edit`, `Write`, `MultiEdit`, `NotebookEdit`) salvo reglas acotadas bajo `./docs/` (que ya vienen incluidas por defecto).
   - Se rechaza `Bash` sin paréntesis y comodines globales (`Bash(*)`, `Bash(**)`, `Bash( )`, `Bash(* *)`). Se exigen prefijos de comando específicos como `Bash(codegraph *)` o `Bash(gh pr view *)`.
5. **`ApplyPreset` y `RenderTOML`:**
   - `ApplyPreset` clona profundamente `MCPServers` (mapas y slices) y los slices de herramientas/prompts/skills sin perder la configuración externa del usuario al cambiar de preset.
   - `RenderTOML` conserva un round-trip válido con `toml.Decode`.

---

## 4. Propagación de servidores MCP (`provider`, `mcpserver`, `engine`, `app`)

Para evitar ciclos de importación (`mcpserver` importa `engine`, `engine` importa `provider`), el tipo neutro reside en `internal/provider`:

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

`RunRequest` añade `MCPServers []MCPServer`.

### 4.1 Expansión de `${ENV_VAR}` en runtime

Al traducir `config.MCPServerCfg` a `provider.MCPServer` (justo antes de ejecutar orquestador o subagente):
- Se expanden placeholders `${VAR}` en `Env`, `Headers` y `URL` usando `os.LookupEnv` (si una variable referenciada no está definida, falla explícitamente con error legible indicando qué variable falta en qué servidor, salvo si el servidor está `disabled = true`).
- `weaver config show` **no** expande `${VAR}`, manteniendo el TOML seguro para inspección y copia.

### 4.2 Orquestador (`app` + `mcpserver` + `claude`)

- `app.RunSession` filtra los servidores habilitados con `scope` igual a `"orchestrator"` o `"all"`.
- `mcpserver.WriteMCPConfigWithServers(path, url, token, extras)` escribe `mcp.json` (`0o600`) con `"weaver"` (HTTP + Bearer token efímero) y todos los externos en formato Claude (`stdio` o `http`), protegiendo que ningún externo pise `"weaver"`. `WriteMCPConfig(path, url, token)` delega en `WriteMCPConfigWithServers` para mantener compatibilidad.
- `app.RunSession` compone `AllowedTools` como:
  1. `OrchestratorTools` base,
  2. `"mcp__" + srv.Name` por cada servidor externo habilitado para el orquestador,
  3. `cfg.OrchestratorExtraTools` (deduplicando preservando orden).
- Claude orquestador sigue lanzándose con `--mcp-config <runDir/mcp.json> --strict-mcp-config --permission-mode dontAsk --allowedTools ...`.

### 4.3 Subagentes (`engine` + los 4 adaptadores en `provider`)

`engine.Spawn`, `engine.FollowUp` y el reintento de recibo (`retryReq`) adjuntan en `req.MCPServers` los servidores habilitados con `scope` `"workers"` o `"all"`.

Cada adaptador traduce `req.MCPServers` a su protocolo nativo:

| Proveedor | Mecanismo cuando `len(req.MCPServers) > 0` | Comportamiento si está vacío |
|---|---|---|
| `claude` (Worker) | Crea archivo JSON temporal `0o600` (`mcpServers` en formato Claude), pasa `--mcp-config <tmp>` **sin** `--strict-mcp-config` (combina con los MCP locales del usuario) y limpia el archivo al terminar `Wait()`/`Kill()`. | Sin cambios en flags. |
| `antigravity` (`agy acp`) | Envía el slice en `session/new` (`mcpServers`, y en `session/resume` si aplica) en formato **ACP v1**: `stdio` sin campo `"type"` y `env` como `[{"name","value"}]`; `http` con `"type":"http"` y `headers` como `[{"name","value"}]`. Si `initialize` declara `agentCapabilities.mcpCapabilities.http == false`, omite servidores `http` y emite `EventError` informativo antes de continuar con los `stdio`. | Envía `"mcpServers": []` como hoy. |
| `codex` (`codex exec`) | Añade flags `-c mcp_servers.<name>={...}` usando tabla inline TOML (`command`/`args`/`env` o `url`/`http_headers`) tanto en `exec` inicial como en `exec resume`. | Sin flags `-c mcp_servers` extra. |
| `opencode` (`opencode run`) | Parte de `os.Environ()`, fusiona la clave `"mcp"` (`type: "local"` con `command: [cmd, args...]` + `environment`; o `type: "remote"` con `url` + `headers`, `enabled: true`) dentro de `OPENCODE_CONFIG_CONTENT` (respetando un JSON previo en la variable si existiera). | Sin modificar `cmd.Env`. |

---

## 5. Prompts adicionales y `skills_dirs` (`internal/prompts`)

Se añaden sin romper las firmas actuales `Orchestrator(projectDir)` ni `Worker(projectDir, d)`:

```go
type ExtraOptions struct {
	PromptFiles []string
	SkillsDirs  []string
}

func OrchestratorWithExtras(projectDir string, opts ExtraOptions) (string, error)
func WorkerWithExtras(projectDir string, d WorkerData, opts ExtraOptions) (string, error)
```

Reglas de resolución y seguridad:
1. **Rutas:** `~/...` se expande con `os.UserHomeDir()`; rutas relativas se resuelven respecto a `projectDir`; rutas absolutas se limpian con `filepath.Clean`.
2. **Carga de `PromptFiles`:** se leen en el orden declarado; si no existe o es un directorio, retorna error explícito.
3. **Carga de `SkillsDirs`:**
   - Si `<dir>/SKILL.md` existe, carga ese skill individual.
   - Si no existe en la raíz, escanea subdirectorios inmediatos `<dir>/*/SKILL.md` ordenados lexicográficamente por nombre.
   - Si el directorio no existe o no contiene ningún `SKILL.md` ni en raíz ni en hijos inmediatos, retorna error explícito.
4. **Composición:** el contenido se normaliza (`TrimSpace`) y se **apendiza** al final del prompt efectivo bajo secciones claras (`## Instrucciones adicionales` / `## Skill: <nombre>`), garantizando que las reglas SDD/RDD y el formato `weaver-receipt` permanecen intactos (incluso si existe `.weaver/prompts/orchestrator.md` o `worker.md`).
5. **Tope de contexto:** límite agregado de `256 KiB` para contenido adicional por prompt, fallando con error claro si se supera para evitar agotar el contexto por apuntar por error a un directorio enorme.

---

## 6. Diagnóstico en `weaver doctor` y documentación

`app.Doctor` incorpora comprobaciones no destructivas tras la tabla de proveedores cuando existen configuraciones externas:
- Verifica con `resolveBinary` / `exec.LookPath` que cada `command` de `[mcp_servers.*]` habilitado de tipo `stdio` sea ejecutable.
- Verifica que cada ruta de `orchestrator_extra_prompts`, `worker_extra_prompts` y `skills_dirs` exista y tenga permisos de lectura (resolviendo `~/` y relativas al proyecto).
- Muestra `NOTAS` o avisos accionables cuando falte alguna herramienta externa, manteniendo el código de salida vinculado a la disponibilidad del orquestador (para que un skill opcional ausente en una máquina no bloquee `doctor` si el usuario solo consulta proveedores, o reportando el detalle claramente). *(Nota: en `RunSession` y `Spawn`, si una ruta de prompt/skill explícitamente configurada no existe, sí falla temprano con mensaje claro para no ejecutar silenciosamente sin el skill esperado).*
- `README.md` documentará ejemplos prácticos para **CodeGraph**, **Superpowers** (`skills_dirs`) y servidores MCP HTTP/stdio en `config.toml`.
