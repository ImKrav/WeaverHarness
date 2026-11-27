# WeaverHarness

CLI en Go con interfaz TUI en la terminal para orquestar agentes de distintas suscripciones de IA (Claude Pro/Max, ChatGPT/Codex, Google Antigravity, OpenCode Go) desde un solo lugar.

Hablas con un **orquestador** (por defecto Claude Code con Opus) que diseña con **SDD** (*Spec-Driven Development*) y reparte tareas a **subagentes** en worktrees Git aislados. Cada subagente entrega un **recibo** con evidencia verificable (**RDD**, *Receipt-Driven Review*), y WeaverHarness re-ejecuta los checks con el shell nativo del sistema operativo y protege las referencias Git antes de integrar nada.

## Principio

WeaverHarness **no usa APIs ni toca credenciales**. Solo lanza los CLIs oficiales y sin modificar de cada proveedor como procesos hijos (`claude`, `codex`, `agy`, `opencode`), usando la sesión que tú ya iniciaste en cada herramienta.

## Documentación de diseño y planes

- Especificación base: [docs/superpowers/specs/2026-11-26-weaverharness-design.md](docs/superpowers/specs/2026-11-26-weaverharness-design.md)
- Plan v1 (arquitectura inicial): [docs/superpowers/plans/2026-11-26-weaverharness-v1.md](docs/superpowers/plans/2026-11-26-weaverharness-v1.md)
- Plan 1 (UX de la TUI): [docs/superpowers/plans/2026-11-27-tui-ux.md](docs/superpowers/plans/2026-11-27-tui-ux.md)
- Plan 2 (MCP y motor de ejecución): [docs/superpowers/plans/2026-11-27-mcp-and-engine.md](docs/superpowers/plans/2026-11-27-mcp-and-engine.md)
- Plan 3 (utilidades CLI y presets): [docs/superpowers/plans/2026-11-27-cli-utilities.md](docs/superpowers/plans/2026-11-27-cli-utilities.md)
- Plan 4 (observabilidad, robustez y CI): [docs/superpowers/plans/2026-11-27-observability-robustness-ci.md](docs/superpowers/plans/2026-11-27-observability-robustness-ci.md)
- Plan 5 (compatibilidad Linux/macOS/Windows y releases): [docs/superpowers/plans/2026-11-27-cross-platform-and-releases.md](docs/superpowers/plans/2026-11-27-cross-platform-and-releases.md)

## Requisitos

- Go `1.27+` (para compilar desde código fuente)
- `git`
- Los CLIs de los proveedores que vayas a utilizar, instalados y con sesión iniciada: `claude` (requerido para el orquestador en v1), `codex`, `agy`, `opencode`

## Instalación y compilación

Desde el código fuente:

```bash
go build ./cmd/weaver
```

O instalar en `$GOPATH/bin`:

```bash
go install github.com/ImKrav/weaverharness/cmd/weaver@latest
```

También se publican binarios precompilados (`CGO_ENABLED=0`) para `linux`, `darwin` y `windows` (`amd64` y `arm64`) junto con `checksums.txt` cuando se etiqueta un release `v*` con GoReleaser.

## Comandos y flags CLI

### Opciones globales / de sesión

- `--preset <nombre>`: aplica un preset de roles (`default`, `claude-codex`, `claude-opencode`, `claude-only`).
- `--skip-check`: omite la verificación previa (`Check`) del CLI orquestador al iniciar sesión.
- `--no-mouse`: desactiva la captura de ratón en la TUI al arrancar para seleccionar texto directamente.
- `-v`, `--version`: muestra versión, commit y fecha de compilación.
- `-h`, `--help`: muestra la ayuda del comando.

### Subcomandos

| Comando | Uso | Descripción |
| :--- | :--- | :--- |
| `(por defecto)` | `weaver [--preset N] [--skip-check] [--no-mouse]` | Abre la sesión TUI interactiva en el repositorio Git actual (o pregunta si hay cambios sin commit). |
| `resume` | `weaver resume [--preset N] [--skip-check] [--no-mouse] [run-id]` | Reanuda el último run guardado en `.weaver/runs/` (o `run-id`), marca agentes en curso como `interrupted` y restaura el historial reciente de chat. |
| `runs` | `weaver runs` | Lista todas las ejecuciones guardadas con su fase, rama de integración, progreso de tareas, agentes y duración activa. |
| `inspect` | `weaver inspect [--json] [run-id]` | Muestra estado detallado del run, tareas, agentes, recibos y verificación en tabla o JSON. |
| `logs` | `weaver logs [--tail N] [--only-receipt] [run-id] <agent-id>` | Muestra recibo, resultado de verificación y las últimas `N` líneas de `transcript.jsonl` (por defecto `100`). |
| `clean` | `weaver clean [--dry-run] [--all] [--force]` | Limpia worktrees, ramas `weaver/*` y runs finalizados (`--all` incluye runs abiertos previa confirmación o `--force`). |
| `config show` | `weaver config show [--preset <nombre>]` | Muestra la configuración efectiva resultante en TOML tras aplicar capas y preset opcional. |
| `config init` | `weaver config init [--preset <nombre>] [--global] [--force]` | Crea `.weaver/config.toml` (o la config global con `--global`). |
| `doctor` | `weaver doctor` | Comprueba instalación (incluyendo rutas fuera del `PATH`), versión, sesión, modelos configurados y, en Windows, `git config core.longpaths`. Devuelve `1` si el orquestador no está listo. |
| `version` | `weaver version` | Imprime versión, commit y fecha de compilación. |

## Atajos de teclado en la TUI

- **Chat y editor:**
  - `Enter`: envía el mensaje al orquestador.
  - `Ctrl+J` (universal en Linux/macOS/Windows) o `Alt+Enter`: inserta un salto de línea sin enviar.
  - `PgUp` / `PgDn` / `Home` / `End`: desplaza el historial de chat (con auto-scroll inteligente e indicador `↓ N nuevos`).
- **Aprobaciones (`spec`, `plan`, `integration`):** (con el editor vacío)
  - `a`: aprobar.
  - `r`: rechazar (abre modo comentario; `Enter` envía el rechazo, `Esc` vuelve).
  - `v`: previsualizar el contenido completo de la spec, plan o `git log`/`diff --stat` de integración en modal.
- **Panel lateral de tareas y agentes:**
  - `Tab` o `↑` / `↓` (con editor vacío): enfoca el panel lateral y selecciona agente.
  - `Enter` (con panel enfocado): abre el modal de detalle del agente (recibo, verificación y log con refresco en vivo mientras corre).
  - `x` (con panel enfocado): solicita cancelación de un agente activo (`[s/N]`).
  - `Esc` (o `q` en modal): cierra el modal o devuelve el foco al chat.
- **Ratón y salida:**
  - `F2`: alterna en vivo la captura de ratón (`F2 ratón: on/off`) para seleccionar/copiar texto directamente sin mantener `Shift`.
  - `Ctrl+C`: pide confirmación (`[s/N]`) antes de cerrar la sesión y los procesos hijos.

## Flujo SDD + RDD y herramientas MCP

1. **Spec:** el orquestador redacta requisitos (`REQ-n`) bajo `docs/` y solicita aprobación (`request_approval`).
2. **Plan:** define tareas con `spec_refs`, `role`, `depends_on`, `checks` y `global_checks`, y pide aprobación.
3. **Ejecución aislada:** lanza subagentes en paralelo (`spawn_agent`) respetando `max_parallel` por proveedor y cada worktree en `.weaver/runs/` (excluido automáticamente vía `.git/info/exclude`).
4. **Supervisión y revisión:** espera finalizaciones (`wait_agents`), inspecciona parches (`agent_diff`, `agent_status`, `run_state`), reintenta o envía correcciones (`follow_up`), cancela (`cancel_agent`) o limpia (`cleanup_agent`), y acepta solo tareas con `weaver-receipt` válido y verificación objetiva aprobada (`accept_task`).
5. **Integración y guardia Git:** antes y después de cada turno del orquestador, `gitx.Guard` verifica que no se hayan movido o eliminado ramas protegidas ni manipulado worktrees de forma indebida.

## Configuración, presets y prompts

### Precedencia de configuración

Las capas se fusionan en orden ascendente:

1. Valores por defecto integrados (`default.toml`)
2. Global de usuario: `<UserConfigDir>/weaver/config.toml`
3. Por proyecto: `.weaver/config.toml`

Valores principales en TOML:

```toml
orchestrator = "architect"
review_rounds = 2
check_timeout = "10m"
task_timeout = "30m"

[roles.architect]
provider = "claude"
model = "opus"

[roles.implementer]
provider = "antigravity"
model = "gemini-4-hydrogen"

[roles.reviewer]
provider = "codex"
model = "gpt-5.6-sol"

[providers.claude]
max_parallel = 3
# bin = "/ruta/personalizada/claude"
```

### Presets disponibles (`--preset`)

- `default`: `architect=claude/opus`, `implementer=antigravity/gemini-4-hydrogen`, `reviewer=codex/gpt-5.6-sol`
- `claude-codex`: `architect=claude/opus`, `implementer=codex/gpt-5.6-sol`, `reviewer=codex/gpt-5.6-sol`
- `claude-opencode`: `architect=claude/opus`, `implementer=opencode/opencode/big-pickle`, `reviewer=codex/gpt-5.6-sol`
- `claude-only`: `architect=claude/opus`, `implementer=claude/sonnet`, `reviewer=claude/sonnet`

### Prompts por proyecto

Puedes sobrescribir los prompts embebidos creando archivos en `.weaver/prompts/`:

- `.weaver/prompts/orchestrator.md`
- `.weaver/prompts/worker.md`
- `.weaver/prompts/receipt_retry.md`

En el prompt por defecto se inyectan automáticamente el sistema operativo y shell de verificación reales (`cmd /s /c` en Windows, `sh -c` POSIX en Linux/macOS).

## Notas por sistema operativo (Linux / macOS / Windows)

- **Descubrimiento de CLIs fuera del `PATH`:** cuando lanzas `weaver` desde un entorno no interactivo y el binario por defecto no aparece en `PATH`, inspecciona rutas habituales:
  - **macOS:** `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `~/.opencode/bin`, `~/.npm-global/bin` y `/Applications/Antigravity.app/Contents/Resources/app/bin/agy`.
  - **Linux:** `$XDG_BIN_HOME`, `~/.local/bin`, `~/.opencode/bin`, `~/.npm-global/bin`, `/usr/local/bin` y `/home/linuxbrew/.linuxbrew/bin`.
  - **Windows:** `%LOCALAPPDATA%\Programs\...`, `%APPDATA%\npm\*.cmd`, `%USERPROFILE%\.local\bin`, `%USERPROFILE%\.opencode\bin` y `%USERPROFILE%\scoop\shims`.
  - Si un CLI está en otra ubicación, configúralo en `[providers.<nombre>] bin = "/ruta/al/binario"`.
- **macOS (teclado en terminal):** Terminal.app y algunos perfiles de iTerm2 no envían `Esc` para la tecla `Option` por defecto; activa **Use Option as Meta key / Esc+** para usar `Option+Enter`, o utiliza `Ctrl+J` (universal).
- **Windows (`core.longpaths` y rutas):** se recomienda activar rutas largas de Git para evitar errores en worktrees profundos (`weaver doctor` avisa si no está habilitado):
  ```powershell
  git config --global core.longpaths true
  ```
- **Señales de apagado:** en Linux/macOS `weaver` maneja `SIGINT` y `SIGTERM` para cerrar los procesos hijos y persistir limpiamente el estado de la sesión.

## Pruebas, calidad y seguridad

Tests unitarios (con detector de condiciones de carrera cuando CGO/`gcc` esté disponible):

```bash
go test -race -shuffle=on -count=1 ./...
```

> En Windows sin `gcc` / `CGO_ENABLED=1`, ejecuta `go test -count=1 ./...`.

Verificación estática, formato, linter y vulnerabilidades:

```bash
go vet ./...
gofmt -l ./cmd ./internal
golangci-lint run ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go mod tidy && git diff --exit-code go.mod go.sum
```

Pruebas de humo con CLIs reales instalados y con sesión iniciada:

En **Linux / macOS (`bash` / `zsh`)**:

```bash
WEAVER_SMOKE_PROVIDERS="claude,codex,opencode,antigravity" go test -tags smoke -run TestSmoke -v ./internal/provider/
```

En **Windows (`PowerShell`)**:

```powershell
$env:WEAVER_SMOKE_PROVIDERS="claude,codex,opencode,antigravity"
go test -tags smoke -run TestSmoke -v ./internal/provider/
```

## Releases y CI

- **CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)):** ejecuta tests en `ubuntu-latest`, `macos-latest` y `windows-latest`, cross-build `CGO_ENABLED=0` (`linux/amd64`, `linux/arm64`, `windows/amd64`, `darwin/arm64`), `go vet` / `golangci-lint` y `govulncheck`.
- **Releases ([`.github/workflows/release.yml`](.github/workflows/release.yml) + [`.goreleaser.yml`](.goreleaser.yml)):** al publicar un tag `v*`, GoReleaser v2 compila y adjunta archivos `.tar.gz` (`.zip` en Windows) junto con `checksums.txt`.
