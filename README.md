# WeaverHarness

CLI en Go con interfaz de chat en la terminal para orquestar agentes de distintas suscripciones de IA (Claude Pro/Max, ChatGPT/Codex, Google Antigravity, OpenCode Go) desde un solo lugar.

Hablas con un **orquestador** (por defecto Claude Code con Opus) que diseña con **SDD** (Spec-Driven Development) y reparte tareas a **subagentes** de otros proveedores. Cada subagente trabaja en su propio git worktree y entrega un **recibo** con evidencia verificable (**RDD**, Receipt-Driven Review). WeaverHarness re-ejecuta esa verificación antes de que nada se integre.

## Principio

WeaverHarness **no usa APIs ni toca credenciales**. Solo lanza los CLIs oficiales y sin modificar de cada proveedor como procesos hijos, con la sesión que tú iniciaste en cada uno. Es la frontera que marcan los términos de los proveedores.

## Estado

Implementación v1 lista.

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

### Comandos

- `weaver`: abre la TUI de chat en el repositorio git actual. Conversas con el orquestador; cuando apruebas spec, plan e integración (`[a]probar` / `[r]echazar`), el orquestador delega tareas en subagentes dentro de worktrees aislados y `weaver` verifica sus recibos antes de integrar.
- `weaver doctor`: comprueba qué CLIs están instalados, su versión, estado de sesión y si los modelos configurados aparecen en el CLI. Devuelve código de salida `1` si al proveedor del rol orquestador le falta algo.
- `weaver resume [run-id]`: reanuda la última ejecución guardada en `.weaver/runs/` (o la indicada por `run-id`), marcando como `interrupted` los agentes que seguían en curso y reabriendo la sesión del orquestador.
- `weaver runs`: lista las ejecuciones guardadas con su fase, rama de integración, tareas y agentes.

### Configuración opcional

Por defecto usa Claude (`opus`) como `architect`, Antigravity (`gemini-4-hydrogen`) como `implementer` y Codex (`gpt-5.6-sol`) como `reviewer`. Puedes sobrescribir roles o binarios en la configuración global (`<UserConfigDir>/weaver/config.toml`) o por proyecto (`.weaver/config.toml`).

### Pruebas

Tests unitarios:

```bash
go test ./...
```

Pruebas de humo con CLIs reales instalados y con sesión iniciada:

```powershell
$env:WEAVER_SMOKE_PROVIDERS="claude,codex,opencode,antigravity"
go test -tags smoke -run TestSmoke -v ./internal/provider/
```
