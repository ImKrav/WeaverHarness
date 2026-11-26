# WeaverHarness

CLI en Go con interfaz de chat en la terminal para orquestar agentes de distintas suscripciones de IA (Claude Pro/Max, ChatGPT/Codex, Google Antigravity, OpenCode Go) desde un solo lugar.

Hablas con un **orquestador** (por defecto Claude Code con Opus) que diseña con **SDD** (Spec-Driven Development) y reparte tareas a **subagentes** de otros proveedores. Cada subagente trabaja en su propio git worktree y entrega un **recibo** con evidencia verificable (**RDD**, Receipt-Driven Review). WeaverHarness re-ejecuta esa verificación antes de que nada se integre.

## Principio

WeaverHarness **no usa APIs ni toca credenciales**. Solo lanza los CLIs oficiales y sin modificar de cada proveedor como procesos hijos, con la sesión que tú iniciaste en cada uno. Es la frontera que marcan los términos de los proveedores.

## Estado

En diseño. Aún no hay código.

- Diseño: [docs/superpowers/specs/2026-11-26-weaverharness-design.md](docs/superpowers/specs/2026-11-26-weaverharness-design.md)

## Requisitos (previstos)

- Go 1.27+
- git
- Los CLIs de los proveedores que uses, instalados y con sesión iniciada: `claude`, `codex`, `agy`, `opencode`
