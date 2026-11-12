# ADR 0013 — Separate-module frontends drive the core over RPC mode

- Status: accepted
- Date: 2026-11-12
- Requirements: REQ-FE-002; REQ-FE-003; REQ-RPC-001

## Context

ADR 0004 keeps `core/` and `cmd/` on the standard library and puts Bubble Tea and Wails in separate modules (`tui-bubbletea/`, `desktop/`). Until now those modules imported `koine/core/app` and `koine/core/agent` directly through a `replace koine => ../` directive. That ties them to internals that change often (every core change could break a module nothing compiles), and it runs the agent inside the GUI process. Now that `koine --mode rpc` exists (REQ-RPC-001), there is a documented, versioned way to drive the agent from another process.

## Decision

Both frontends start `koine --mode rpc` as a child process and speak its protocol through `core/rpcclient`, a client written on the standard library that imports nothing else from Koine. The client, and `Transcript` (events folded into what a screen shows), live in the core module so they are linted, race-tested and held to the 85% coverage gate by the existing Go job. The frontend modules keep their own third-party dependencies (Bubble Tea, Bubbles, Lip Gloss; Wails and what it pulls in) and their own `go.mod`; they reach the core module only through `replace koine => ../` and import only `koine/core/rpcclient`. Their GUI-free logic is separated from the GUI glue (`tui-bubbletea/model.go`, `desktop/bridge/`) so it can be tested without a terminal or a display.

Dependency policy for the two modules: third-party dependencies are allowed there, are kept current by Dependabot and `task deps:update`, and are scanned with govulncheck and Trivy like the core. A transitive advisory with no patched release in the dependency's line (Echo v4 under Wails, see CHANGELOG) is recorded rather than worked around by forking.

## Consequences

The core can change internals freely; only the RPC protocol is a contract, and it already tracks Pi's. A frontend crash cannot corrupt the agent, and the agent runs with the same trust prompts and settings as the CLI. The cost is a process boundary: the `koine` binary must be found (`KOINE_BIN`, next to the frontend, or on `PATH`), slash commands that Koine implements only in its own terminal UI (`/model`, `/tree` ...) are not available because RPC mode, like Pi's, exposes extension commands, prompt templates and skills only, and notices arrive on stderr, which `Start` forwards as `stderr` events.

The authoring environment could not reach a Go module proxy, so the modules were first compiled by the CI job `frontends`, which is now blocking; its `go.sum` files are committed and `go mod tidy -diff` guards them. govulncheck runs only on the core module; Trivy's filesystem scan covers the whole repository.
