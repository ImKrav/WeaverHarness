# WeaverHarness — Diseño

Fecha: 2026-11-26 · Estado: borrador para aprobación

## 1. Intención

Hoy, aprovechar varias suscripciones de IA (Claude Pro/Max, ChatGPT/Codex, Google Antigravity, OpenCode Go) obliga a saltar entre el CLI de cada proveedor. WeaverHarness es **un solo CLI en Go con interfaz de chat en la terminal (TUI)** desde el que el usuario habla con **un orquestador** que dirige una **jerarquía de agentes de distintos proveedores**, poniendo cada modelo donde rinde más y repartiendo el consumo entre suscripciones.

Ejemplo de referencia del usuario: **Opus 5.5 como arquitecto/orquestador**, que despliega **subagentes en Gemini 4 Hydrogen** (vía Antigravity) para implementar, trabajando con **SDD** (Spec-Driven Development) y **RDD** (Receipt-Driven Review).

**Receipt-Driven Review (definición acordada):** ningún subagente puede solo declarar "terminé". Entrega un **recibo** con evidencia verificable (diff/commits, comandos corridos y su resultado, tests, qué requisitos del spec cubre). La revisión se hace **contra el recibo, re-ejecutando** lo necesario, no confiando en lo que el agente dice.

## 2. Objetivos, no-objetivos, anti-objetivos

**Objetivos (v1)**
- TUI de chat en Go: el usuario conversa solo con el orquestador y ve en vivo el árbol de subagentes (proveedor, modelo, tarea, estado, resultado del recibo).
- Orquestador (por defecto Claude Code / Opus) que delega tareas a subagentes de cualquiera de los 4 proveedores.
- Cada subagente trabaja en **su propio git worktree**; el orquestador **integra** los cambios.
- Los subagentes devuelven al orquestador **recibos resumidos**, nunca transcripciones completas.
- SDD con puntos de aprobación del usuario: spec → plan → integración final.
- RDD aplicado **en código** por WeaverHarness (no solo por prompt).
- Roles configurables (presets); "Opus arquitecto + Gemini implementador" es un preset, no algo fijo.

**No-objetivos (v1)**
- Uso por API key / facturación por tokens (todo va por suscripción vía los CLIs oficiales).
- Hablar directamente con subagentes (el usuario confirmó que no hace falta). El panel de agentes es de solo lectura.
- Más de dos niveles de jerarquía (orquestador → subagentes; los subagentes no pueden lanzar otros).
- Interfaz web, ejecución en la nube, panel de costos/cuotas.

**Anti-objetivos (sería un fracaso aunque funcione)**
- Leer, copiar, almacenar o reenviar tokens/credenciales de cualquier proveedor, o llamar a sus backends directamente.
- Que un subagente pueda dañar la rama del usuario o perder trabajo sin que quede rastro.
- Que el orquestador se llene de contexto por recibir transcripciones (quema la cuota de Claude).

## 3. Restricciones

### 3.1 Términos de servicio — la regla común
WeaverHarness **solo lanza el binario oficial y sin modificar de cada proveedor como proceso hijo**, con la sesión que el propio usuario inició en ese CLI, y se comunica por stdin/stdout. Nunca toca credenciales. Es exactamente la frontera que marcan los proveedores:

| Proveedor | Qué está permitido | Fuente |
|---|---|---|
| Claude | El usuario puede iniciar sesión con su suscripción en el binario **sin modificar** de Claude Code. Lo prohibido: ofrecer login de Claude.ai en apps propias, enrutar peticiones con credenciales de plan, o intermediar tokens. El Agent SDK está pensado para API key → **no usar Agent SDK** (además no existe para Go). | https://code.claude.com/docs/en/legal-and-compliance |
| Codex | `codex exec` / `codex app-server` local con login de ChatGPT hecho por Codex. La autenticación del app-server no está permitida para servicios comerciales u hospedados. | https://developers.openai.com/codex/app-server |
| Antigravity | `agy -p` headless con credenciales en caché. En el foro de Google, la cuenta "Engineer760" (no está confirmado que sea de Google) dice que invocar `agy` por pipes desde un orquestador local está soportado, y que extraer tokens o llamar endpoints directo no lo está. **Pero, según el mismo hilo, el FAQ dice que envolver `agy` en otra app no está soportado en planes de consumo** → riesgo abierto (§7). | https://discuss.ai.google.dev/t/is-external-orchestration-of-antigravity-cli-headless-mode-supported-with-account-based-usage/183051 |
| OpenCode Go | Diseñado para OpenCode y agentes similares; OpenCode es open source y expone `opencode run` / `opencode serve`. | https://opencode.ai/docs/go/ |

Consecuencias de diseño:
- Login: siempre lo hace el usuario en cada CLI (`claude`, `codex login`, `opencode auth login`, `agy`). `weaver doctor` solo **consulta** el estado con los comandos de cada CLI; nunca lee archivos de credenciales.
- **Nunca** usar `claude --bare` (fuerza API key, no lee OAuth).
- Si WeaverHarness se distribuye a otras personas, cada una instala y autentica sus propios CLIs; WeaverHarness no ofrece login de nadie.

### 3.2 Plataforma
- Go 1.27. Debe funcionar en **Windows** (máquina principal del usuario), macOS y Linux.
- CLIs verificados localmente: Claude Code 2.1.332, codex-cli 0.169.1, opencode 2.4.1, agy 1.5.17.

## 4. Enfoques considerados

**A. Orquestador delega vía herramientas MCP — ELEGIDO por el usuario.**
WeaverHarness levanta un servidor MCP local que expone herramientas al orquestador (`spawn_agent`, `wait_agents`, `request_approval`…). El orquestador decide cuándo delegar; WeaverHarness lanza los CLIs, gestiona worktrees y **hace cumplir SDD/RDD en el borde de las herramientas** (rechaza recibos incompletos, re-ejecuta verificaciones, no deja lanzar implementadores sin plan aprobado).
Pro: poco código, el orquestador es un agente real, las reglas críticas se aplican en código. Contra: el orden fino de fases depende de que el orquestador siga sus instrucciones; WeaverHarness solo lo frena en las herramientas.

**B. Flujo fijo en Go (máquina de estados).** WeaverHarness codifica las fases y llama a cada modelo con prompts por fase, leyendo JSON. Pro: orden garantizado, fácil de reanudar. Contra: rígido, mucho más código, el "orquestador" deja de ser agente, y exige salida estructurada fiable de 4 CLIs distintos.

**Descartados:** Agent SDK (no hay Go, y Anthropic lo dirige a API key); manejar las TUIs interactivas de cada CLI con una pseudo-terminal (frágil); híbrido A+B (A ya aplica las reglas en las herramientas).

## 5. Diseño

### 5.1 Vista general

```
           ┌──────────────── weaver (un proceso Go) ────────────────┐
 usuario ⇄ │ TUI (chat + panel de agentes)                          │
           │   │                                                    │
           │   ▼ stdin/stdout stream-json                           │
           │ Orquestador: `claude -p` (Opus) ──MCP(HTTP local)──► Servidor MCP de weaver
           │                                                        │   │
           │                          ┌─────────────────────────────┘   │
           │                          ▼                                 │
           │ Gestor de agentes ── adaptadores ──► claude │ codex │ agy │ opencode   (procesos hijos,
           │   │                                   cada uno en su worktree)          uno por subagente)
           │   ▼                                                        │
           │ Verificador RDD (git + re-ejecución de comandos)           │
           │ Estado en disco (.weaver/)                                 │
           └────────────────────────────────────────────────────────────┘
```

Un único proceso `weaver` contiene la TUI, el servidor MCP, el gestor de agentes y el verificador. El servidor MCP escucha en `127.0.0.1` (puerto aleatorio) con un token aleatorio por ejecución en cabecera; el orquestador lo recibe vía `--mcp-config` (JSON inline, tipo `http`).

### 5.2 Adaptadores de proveedor

Interfaz común (Go):

```go
type Provider interface {
    Name() string                       // "claude" | "codex" | "antigravity" | "opencode"
    Check(ctx context.Context) Health   // instalado, versión, sesión iniciada (vía el propio CLI)
    Start(ctx context.Context, req RunRequest) (Run, error)
}
type RunRequest struct {
    Dir, Model, Prompt, SystemPrompt string
    Mode      Mode      // Orchestrator | Worker
    ResumeID  string    // reanudar sesión del CLI (follow-up)
}
type Run interface {
    Events() <-chan Event   // texto, tool-use, error, final; normalizado
    Send(msg string) error  // solo orquestador (sesión larga)
    SessionID() string
    Wait() (Final, error)   // Final.Text = último mensaje; Final.Err clasificado
}
```

Errores clasificados: `not_installed`, `not_logged_in`, `quota_exhausted`, `model_unknown`, `timeout`, `empty_output`, `crashed`. Cada adaptador mapea los mensajes de su CLI a estas clases.

Invocaciones base (flags verificados con `--help` de las versiones locales; el constructor ajusta detalles):

| Proveedor | Orquestador (sesión larga) | Subagente (una tarea) | Reanudar |
|---|---|---|---|
| Claude | `claude -p --input-format stream-json --output-format stream-json --verbose --model <m> --mcp-config <json> --strict-mcp-config --append-system-prompt <prompt weaver> --allowedTools <ver §5.3.1>` | `claude -p --output-format stream-json --verbose --model <m> --append-system-prompt <prompt worker> --permission-mode bypassPermissions` (cwd = worktree) | `--resume <session-id>` |
| Codex | (no orquestador en v1) | `codex exec --json -m <m> -C <worktree> -s workspace-write <prompt>`, sin pedir aprobaciones (`-c approval_policy="never"` o `--approve-for-me`; lo elige el constructor) | `codex exec resume <id>` |
| Antigravity | (no orquestador en v1) | `agy -p --output-format stream-json --model <m> --dangerously-skip-permissions --sandbox <prompt>` (cwd = worktree). Alternativa: `agy acp` (Agent Client Protocol por stdio) | `--conversation <id>` |
| OpenCode | (no orquestador en v1) | `opencode run --format json -m <provider/model> --auto <prompt>` (cwd = worktree) | `--session <id>` |

- v1: **solo Claude puede ser orquestador** (es el que tiene sesión larga por stream-json + MCP inline verificados). La interfaz no lo impide para otros más adelante.
- Los subagentes **no** reciben el servidor MCP de weaver: su recibo sale en el mensaje final (§5.4). Así no hay que configurar MCP en 4 CLIs distintos.
- **Permisos de subagentes (decidido por el usuario: shell libre + guardia de refs).** Los subagentes necesitan shell para correr tests, así que se lanzan sin pedir aprobaciones (tabla de arriba). Donde el CLI tiene sandbox propio se activa (Codex `workspace-write`, `agy --sandbox`). El worktree **no** es una frontera de seguridad: todos comparten el mismo `.git`, y Claude y OpenCode pueden escribir fuera de su carpeta. Para cubrir el anti-objetivo de no dañar la rama del usuario existe la **guardia de refs**:
  - Antes de lanzar cada subagente (y al terminar cada `follow_up`), weaver guarda una foto de todas las refs (`git for-each-ref`) y del HEAD del repo principal y de los demás worktrees.
  - Al terminar, compara. Un subagente solo puede mover **su propia** rama `weaver/<run-id>/<agent-id>`. Cualquier otro cambio (rama del usuario, otras ramas, tags, HEAD de otro worktree) se **restaura** a la foto, se marca en `weaver_verification.ref_violations` y se avisa en la TUI. Las refs nuevas fuera de `weaver/` se reportan, pero no se borran.
  - `git push` no se puede deshacer: el prompt del trabajador lo prohíbe, y weaver lanza los subagentes con `GIT_TERMINAL_PROMPT=0` y además detecta en la foto de refs remotas cualquier cambio de `refs/remotes/*` y lo reporta.
  - Riesgo aceptado por el usuario: escrituras de Claude/OpenCode fuera del worktree no se detectan en v1.
- **Windows y procesos:** cancelar debe matar el árbol de procesos completo (los CLIs lanzan hijos). En Windows usar Job Objects (`golang.org/x/sys/windows`); en Unix, grupo de procesos.
- **Antigravity en Windows:** hay un bug reportado (issue #76 de `google-antigravity/antigravity-cli`) en el que `agy -p` con stdout redirigido no imprime nada y sale con código 0, y lo mismo pasa con la cuota agotada. El adaptador **debe** tratar "salida vacía" como `empty_output`, nunca como éxito. Antes de implementar el adaptador se hace un **spike** para elegir entre: (1) `agy acp`, (2) leer `transcript.jsonl` de `~/.gemini/antigravity-cli/brain/…`, (3) ConPTY.

### 5.3 Servidor MCP de weaver (herramientas del orquestador)

| Herramienta | Entrada | Salida | Reglas que aplica weaver |
|---|---|---|---|
| `request_approval` | `kind` (`spec`\|`plan`\|`integration`), `path` del documento, `summary`, y para `plan`: `tasks[]` y `global_checks[]` (ver abajo) | `{approved: bool, comment}` (bloquea hasta que el usuario responde en la TUI) | Valida el formato del plan. Guarda la aprobación. `plan` requiere `spec` aprobado. `integration` requiere **todas las tareas en estado `accepted`** y que weaver haya re-ejecutado él mismo los `global_checks` en `weaver/<run-id>` con éxito; si no, la rechaza sin molestar al usuario y le dice al orquestador qué falta. |
| `spawn_agent` | `task_id`, `role` (o `provider`+`model`), `instructions` | `{agent_id}` (asíncrono) | Rechaza si no hay plan aprobado o si `task_id` no está en él (excepción: `task_id: "integrate"`, para resolver conflictos de merge, permitido tras el plan). Respeta el límite de concurrencia por proveedor. Crea el worktree y la rama. |
| `accept_task` | `task_id`, `agent_id` | ok | Solo acepta si el último recibo de ese agente es `done` y su `weaver_verification` pasó entero. Marca la tarea como `accepted`. Es lo único que deja integrar una rama. |
| `wait_agents` | `agent_ids[]`, `timeout_s` | recibos + verificación de los que terminaron, estado del resto | Solo devuelve recibo + verificación, **nunca la transcripción**. |
| `follow_up` | `agent_id`, `message` | `{agent_id}` (asíncrono) | Reanuda la sesión del mismo CLI en el mismo worktree. Máx. `review_rounds` (por defecto 2) por tarea. |
| `agent_status` | `agent_id?` | estado(s) | — |
| `cancel_agent` | `agent_id` | ok | Mata el árbol de procesos y conserva el worktree para inspección. |
| `cleanup_agent` | `agent_id` | ok | Borra el worktree y la rama solo si la rama ya está integrada en la rama de la ejecución. |

Formato de tarea del plan:

```json
{ "id": "T1", "title": "…", "spec_refs": ["REQ-2"], "role": "implementer",
  "depends_on": [], "checks": ["go test ./internal/auth/..."] }
```

`checks` son los comandos de aceptación que el subagente debe correr y que weaver re-ejecuta. El plan lleva además `global_checks` (p. ej. `["go build ./...", "go test ./..."]`), que weaver corre sobre la rama de integración antes de la aprobación final.

Estados de tarea: `pending → running → receipt_ok | receipt_failed → accepted` (con `follow_up` se vuelve a `running`). Los comandos se ejecutan con `cmd /c` en Windows y `sh -c` en Unix, dentro del worktree.

### 5.3.1 Permisos del orquestador
El orquestador es arquitecto: **no escribe código**. Se lanza con permisos limitados a: leer el repo; escribir solo en `docs/**`; `Bash(git *)` para merges en el worktree de integración; y las herramientas MCP de weaver. Si un merge tiene conflictos, no los resuelve editando código: lanza `spawn_agent(task_id: "integrate")`. Así SDD y RDD no se pueden saltar y la cuota de Claude se va en diseñar y revisar.

### 5.3.2 Alcance de una ejecución
Una **sesión** de TUI puede tener varias **ejecuciones**. Una ejecución = un ciclo spec → plan → tareas → integración, con su propio `run-id`, rama `weaver/<run-id>` y aprobaciones. Conversar o hacer preguntas al orquestador no crea ejecución; la crea la primera `request_approval(spec)`. Todo cambio de código pasa por una ejecución (para algo pequeño, spec y plan pueden ser de pocas líneas). Tras la integración, la ejecución se cierra y la sesión sigue: el siguiente pedido abre otra ejecución en la misma sesión del orquestador (conserva el contexto).

### 5.4 Recibos (RDD)

El prompt de trabajador (lo añade weaver) exige terminar con un bloque:

````
```weaver-receipt
{ "task_id": "T1",
  "status": "done | blocked | failed",
  "summary": "≤ 150 palabras: qué se hizo y por qué",
  "spec_refs": ["REQ-2"],
  "changes": [{"path": "internal/auth/login.go", "kind": "added|modified|deleted"}],
  "commits": ["<sha>"],
  "verification": [{"cmd": "go test ./internal/auth/...", "exit_code": 0, "output_tail": "…"}],
  "open_issues": ["…"] }
```
````

Al terminar el proceso, weaver:
1. **Protege el trabajo:** si quedan cambios sin commit en el worktree, los commitea en la rama del agente (`weaver: cambios sin commit de <agent_id>`).
2. **Extrae el recibo** del último mensaje. Si falta o es inválido → un `follow_up` automático pidiendo solo el recibo; si vuelve a fallar → `status: failed`, motivo `no_receipt`. Donde el CLI lo soporte, el adaptador puede usar su flag nativo de esquema (`--json-schema` en claude/agy, `--output-schema` en codex); a decisión del constructor.
3. **Verifica** (resultado adjunto como `weaver_verification`):
   - los `commits` existen en la rama del agente;
   - `changes` coincide con `git diff --name-only <base>..<rama>` (las diferencias se reportan, no se corrigen);
   - re-ejecuta en el worktree cada comando de `checks` de la tarea **y** de `verification`, con timeout (por defecto 10 min), y compara códigos de salida.
4. Entrega al orquestador **solo** recibo + `weaver_verification`. La transcripción completa queda en disco para la TUI.

Los comandos que se re-ejecutan vienen del plan aprobado por el usuario o del propio subagente, y corren solo dentro del worktree del agente, con el mismo nivel de confianza que ya tenía ese subagente.

### 5.5 Worktrees e integración
- Al iniciar una ejecución, weaver crea la rama de integración `weaver/<run-id>` desde el HEAD actual del usuario.
- Cada subagente: rama `weaver/<run-id>/<agent-id>` desde `weaver/<run-id>` (o desde la rama de su dependencia), y worktree en **`<padre-del-repo>/<repo>-weaver/<run-id>/<agent-id>`**. Va fuera del repo para que herramientas como `go test ./...` del repo principal no entren en los worktrees.
- **El orquestador integra**: corre en un worktree de `weaver/<run-id>` y hace los merges con git usando sus propias herramientas. Si hay conflictos, los resuelve él o lanza un agente para resolverlos.
- La rama del usuario **no se toca** hasta la aprobación `integration`. Al aprobarla, weaver hace merge fast-forward de `weaver/<run-id>` a la rama original. Si no puede ser fast-forward, no hace nada y le dice al usuario qué rama mergear.
- Ejecución con el árbol de trabajo del usuario sucio: weaver avisa y pregunta antes de empezar (los cambios sin commit no entran en la ejecución).

### 5.6 Flujo SDD (lo guía el prompt del orquestador; lo fuerzan las herramientas)
1. El usuario describe el objetivo en el chat.
2. El orquestador escribe el spec (con requisitos `REQ-n`) en `docs/specs/` → `request_approval(spec)`.
3. El orquestador escribe el plan con tareas → `request_approval(plan, tasks)`.
4. Lanza tareas según `depends_on`, en paralelo las independientes, cada una con el rol del plan.
5. RDD: lee recibo + verificación, revisa contra el spec y llama `accept_task` o manda `follow_up`. Opcionalmente lanza un agente con rol `reviewer` (debe ser una tarea del plan).
6. Mergea las ramas aceptadas en `weaver/<run-id>` → `request_approval(integration)` (weaver corre los `global_checks` antes de mostrársela al usuario).
7. Si un agente cae con `quota_exhausted`, el orquestador puede relanzar la tarea con otro rol o proveedor. Weaver informa el error, pero no reasigna solo.

Los prompts del orquestador y del trabajador van embebidos en el binario (`embed`) y se pueden sobrescribir por proyecto.

### 5.7 TUI
Bubble Tea + Bubbles + Lip Gloss (charmbracelet), el estándar de facto en Go.

```
┌ WeaverHarness · run 20261126-a · orquestador claude/opus ──────────────┐
│ tú › implementa login con email según el spec   │ Agentes               │
│ opus › Spec lista: docs/specs/login.md          │ ● a1 T2 agy/gemini    │
│ ⚑ Aprobación: spec — [a]probar [r]echazar       │   corriendo · 3m12s   │
│ opus › Lanzando T1, T2…                         │ ✓ a2 T1 codex         │
│                                                 │   recibo ✓ checks 4/4 │
│                                                 │ ✗ a3 T3 opencode      │
│                                                 │   no_receipt          │
├─────────────────────────────────────────────────┴───────────────────────┤
│ › _                                                                     │
└─────────────────────────────────────────────────────────────────────────┘
```

- Panel de agentes de solo lectura. Al seleccionar uno: recibo, verificación y la cola del log en vivo.
- Las aprobaciones aparecen en el chat. Al rechazar, el usuario puede escribir un comentario que se devuelve al orquestador.
- Teclas mínimas: enviar, navegar el panel, aprobar/rechazar, `Ctrl+C` = parar todo (con confirmación: mata los árboles de procesos y conserva worktrees y estado).
- Las peticiones de permiso del orquestador se muestran en la TUI (vía `--permission-prompt-tool` apuntando a una herramienta MCP de weaver, o con el protocolo de control del modo stream-json; decide el constructor).

### 5.8 Configuración
TOML (`BurntSushi/toml`): global en el directorio de configuración del usuario (`os.UserConfigDir()/weaver/config.toml`) y por proyecto en `.weaver/config.toml`. La del proyecto sobrescribe la global.

```toml
orchestrator = "architect"
review_rounds = 2
check_timeout = "10m"

[roles.architect]
provider = "claude"
model = "opus"

[roles.implementer]
provider = "antigravity"
model = "gemini-4-hydrogen"   # nombre exacto según `agy models`

[roles.reviewer]
provider = "codex"
model = "gpt-5.6-sol"

[providers.antigravity]
max_parallel = 3
```

Comandos: `weaver` (abre la TUI en el repo actual), `weaver resume [run-id]`, `weaver doctor`, `weaver runs` (lista ejecuciones).

### 5.9 Estado en disco
`.weaver/` en el repo (weaver lo añade al `.gitignore`):
```
.weaver/config.toml
.weaver/runs/<run-id>/state.json          # fase, aprobaciones, plan, session-id del orquestador, agentes
.weaver/runs/<run-id>/agents/<id>/transcript.jsonl
.weaver/runs/<run-id>/agents/<id>/receipt.json
.weaver/runs/<run-id>/agents/<id>/verification.json
```
`weaver resume` reabre el orquestador con `claude --resume <session-id>`. Los agentes que estaban corriendo cuando murió el proceso quedan como `interrupted` y el orquestador decide si los vuelve a lanzar.

### 5.10 Casos borde
- CLI no instalado o sin sesión: `spawn_agent` falla con `not_installed`/`not_logged_in` y el orquestador elige otro rol. `weaver doctor` lo muestra antes.
- El repo no es git: weaver se niega a empezar y explica por qué.
- El orquestador muere: la TUI lo muestra y ofrece reanudar.
- Un subagente se cuelga: timeout por tarea configurable (por defecto 30 min) → `timeout`.

## 6. Pruebas
- **Adaptadores:** CLIs falsos (binarios Go de prueba) que reproducen salidas grabadas de cada proveedor, incluido el caso "vacío + exit 0" de `agy`, cuota agotada y salida truncada.
- **Recibos:** extracción y validación (bloque ausente, JSON inválido, campos faltantes); verificación con un repo git temporal (commits inexistentes, `changes` que no cuadra, checks que fallan).
- **Herramientas MCP:** reglas de fase (`spawn_agent` sin plan, `task_id` desconocido, límite de concurrencia, `cleanup_agent` de rama no integrada).
- **Humo manual por proveedor** (requiere sesiones reales): un script que lanza una tarea trivial por adaptador y valida el recibo.

## 7. Riesgos y verificaciones pendientes
1. **Términos de Antigravity:** la respuesta del foro y el FAQ se contradicen. v1 es **de uso personal** y el usuario **asume el riesgo**; el adaptador de `agy` entra en v1. Si algún día se publica, hay que confirmarlo antes. `weaver doctor` lo marca como "soporte no confirmado". **Si el spike de `agy` no encuentra una forma fiable en Windows**, v1 sale sin Antigravity y el rol `implementer` por defecto pasa a Codex (o a OpenCode si Codex no está disponible); el adaptador de `agy` queda para después.
2. **Facturación de `claude -p`:** hubo reportes de que el uso headless o del SDK pasaría a un crédito mensual aparte de los límites del plan; no se pudo confirmar si sigue vigente. No cambia el diseño, pero sí cuánto rinde Opus como orquestador.
3. **Bug de salida vacía de `agy` en Windows:** lo cubre el spike de §5.2.
4. **Contexto del orquestador:** aunque solo reciba recibos, en ejecuciones largas puede llenarse; Claude Code compacta solo. Hay que vigilarlo en las pruebas de humo.
5. **Nombres de modelos** ("Gemini 4 Hydrogen", etc.): se validan contra el listado de cada CLI (`agy models`, etc.) en `weaver doctor`, no se fijan en el código.

## 8. Quién decidió qué
**Decidido por el usuario:** Go; TUI de chat; uso por suscripción mediante los CLIs, no por API; orquestación jerárquica entre proveedores; SDD + RDD (Receipt-Driven Review) con la definición de §1; enfoque A (MCP); un worktree por subagente con integración por el orquestador; recibos resumidos y no transcripciones; el usuario no habla con los subagentes; subagentes con shell libre + guardia de refs (opción "a"); uso personal, riesgo de términos de Antigravity asumido e incluir `agy` en v1.

**Propuesto por mí (se puede corregir):** no usar Agent SDK; solo Claude como orquestador en v1; dos niveles de jerarquía; los subagentes sin MCP y con el recibo en el mensaje final; worktrees fuera del repo; rama de integración por ejecución y merge solo fast-forward tras la aprobación; auto-commit de cambios pendientes; `spawn_agent` bloqueado hasta que el plan esté aprobado; `accept_task` obligatorio y aprobación final bloqueada hasta que todas las tareas estén aceptadas y pasen los `global_checks`; el orquestador no escribe código (§5.3.1); alcance de ejecución y sesión (§5.3.2); restaurar refs ajenas en vez de solo avisar; v1 sin `agy` si el spike falla; reasignación por cuota decidida por el orquestador; Bubble Tea; TOML; timeouts y límites por defecto.

**Queda al constructor:** el protocolo exacto de cada adaptador dentro de lo de §5.2 (incluido el spike de `agy`); el mecanismo de permisos del orquestador (§5.7); el uso de flags de esquema nativos para el recibo; la librería MCP de Go (preferible el SDK oficial `modelcontextprotocol/go-sdk`); la estructura de paquetes; el texto de los prompts embebidos.
