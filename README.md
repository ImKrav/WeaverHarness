# WeaverHarness

CLI en Go con interfaz de chat en la terminal para orquestar agentes de distintas suscripciones de IA (Claude Pro/Max, ChatGPT/Codex, Google Antigravity, OpenCode Go) desde un solo lugar.

Hablas con un **orquestador** (por defecto Claude Code con Opus) que diseña con **SDD** (Spec-Driven Development) y reparte tareas a **subagentes** de otros proveedores. Cada subagente trabaja en su propio git worktree y entrega un **recibo** con evidencia verificable (**RDD**, Receipt-Driven Review). WeaverHarness re-ejecuta esa verificación antes de que nada se integre.

## Principio

WeaverHarness **no usa APIs ni toca credenciales**. Solo lanza los CLIs oficiales y sin modificar de cada proveedor como procesos hijos, con la sesión que tú iniciaste en cada uno. Es la frontera que marcan los términos de los proveedores.

## Estado

Implementación v1 lista y compatible con **Linux, macOS y Windows**.

- Diseño: [docs/superpowers/specs/2026-11-26-weaverharness-design.md](docs/superpowers/specs/2026-11-26-weaverharness-design.md)
- Plan v1: [docs/superpowers/plans/2026-11-26-weaverharness-v1.md](docs/superpowers/plans/2026-11-26-weaverharness-v1.md)

## Requisitos

- Go 1.27+
- `git`
- Los CLIs de los proveedores que uses, instalados y con sesión iniciada: `claude`, `codex`, `agy`, `opencode`

## Uso

Compilar o instalar el binario:

```bash
go build ./cmd/weaver
```

O compilar para Linux, macOS o Windows con `CGO_ENABLED=0` (también publicado en releases `v*` vía GoReleaser).

### Comandos

- `weaver`: abre la TUI de chat en el repositorio git actual. Conversas con el orquestador; cuando apruebas spec, plan e integración (`[a]probar` / `[r]echazar`), el orquestador delega tareas en subagentes dentro de worktrees aislados y `weaver` verifica sus recibos antes de integrar.
- `weaver doctor`: comprueba qué CLIs están instalados, su versión, estado de sesión, rutas estándar fuera del `PATH`, si los modelos configurados aparecen en el CLI y (en Windows) si `core.longpaths` está activo. Devuelve código de salida `1` si al proveedor del rol orquestador le falta algo.
- `weaver resume [run-id]`: reanuda la última ejecución guardada en `.weaver/runs/` (o la indicada por `run-id`), marcando como `interrupted` los agentes que seguían en curso y reabriendo la sesión del orquestador.
- `weaver runs`: lista las ejecuciones guardadas con su fase, rama de integración, tareas, agentes y duración activa.

### Atajos TUI y compatibilidad de terminal

- `Enter`: enviar mensaje (o abrir detalle del agente si el panel lateral está enfocado).
- `Ctrl+J` (universal en Linux/macOS/Windows) o `Alt+Enter`: insertar salto de línea en el editor.
- `F2`: alternar en vivo la captura de ratón (`F2 ratón: on/off`). Cuando la desactivas (o arrancas con `--no-mouse`), puedes seleccionar y copiar texto directamente sin mantener `Shift`.

### Notas por sistema operativo (Linux / macOS / Windows)

- **Descubrimiento de CLIs fuera del `PATH`:** si lanzas `weaver` desde un entorno no interactivo, busca automáticamente en rutas habituales cuando el binario no aparece en `PATH`:
  - **macOS:** `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `~/.opencode/bin`, `~/.npm-global/bin` y `/Applications/Antigravity.app/Contents/Resources/app/bin/agy`.
  - **Linux:** `$XDG_BIN_HOME`, `~/.local/bin`, `~/.opencode/bin`, `~/.npm-global/bin`, `/usr/local/bin` y `/home/linuxbrew/.linuxbrew/bin`.
  - **Windows:** `%LOCALAPPDATA%\Programs\...`, `%APPDATA%\npm\*.cmd`, `%USERPROFILE%\.local\bin`, `%USERPROFILE%\.opencode\bin` y `%USERPROFILE%\scoop\shims`.
  - Si un binario está en otra ruta, configúralo explícitamente bajo `[providers.<nombre>]` con `bin = "/ruta/al/binario"`.
- **macOS (teclado en terminal):** Terminal.app y algunos perfiles de iTerm2 no envían `Esc` para la tecla `Option` por defecto; activa **Use Option as Meta key / Esc+** si quieres usar `Option+Enter` (o usa `Ctrl+J`, que funciona en todas las terminales sin configuración).
- **Windows (`core.longpaths` y symlinks):** se recomienda activar rutas largas de Git para evitar errores en worktrees profundos:
  ```powershell
  git config --global core.longpaths true
  ```
- **Señales de apagado:** en Linux/macOS `weaver` maneja `SIGINT` y `SIGTERM` para cerrar los procesos hijos y el estado del engine limpiamente (`Ctrl+C` pide confirmación dentro de la TUI o puedes usar la señal de terminación).

### Configuración opcional

Por defecto usa Claude (`opus`) como `architect`, Antigravity (`gemini-4-hydrogen`) como `implementer` y Codex (`gpt-5.6-sol`) como `reviewer`. Puedes sobrescribir roles o binarios en la configuración global (`<UserConfigDir>/weaver/config.toml`) o por proyecto (`.weaver/config.toml`).

### Pruebas, calidad y seguridad

Tests unitarios (con detector de condiciones de carrera cuando CGO/`gcc` esté disponible):

```bash
go test -race ./...
```

> En Windows sin `gcc` / `CGO_ENABLED=1`, ejecuta `go test -count=1 ./...`.

Verificación estática, formato, linter y vulnerabilidades:

```bash
go vet ./...
gofmt -l ./cmd ./internal
golangci-lint run
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
