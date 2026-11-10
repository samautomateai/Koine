# koine

A Pi-style coding agent in Go, with a terminal UI and a desktop UI, whose
extensibility is **language-agnostic** and **drop-in compatible** with existing
Pi extensions, opencode plugins, Claude-style hooks and `SKILL.md` skills.

- **Core** (`core/`): agent loop, tools, skills/prompts/sub-agents loader, event model, extension host. Standard library only.
- **Frontends**: line-mode TUI (`cmd/koine`, no deps) · Bubble Tea TUI (`tui-bubbletea/`) · Wails v2 desktop (`desktop/`) · HTTP/SSE server (`koine -serve`) for Electron or the browser.
- **Extensions**: JS/TS (Pi + opencode) via a Bun/Node sidecar, any other language via a JSON-RPC-over-stdio protocol, shell commands via Claude-compatible hooks.

Full details: **[docs/SPEC.md](docs/SPEC.md)**.

## Quick start

```bash
export ANTHROPIC_API_KEY=sk-ant-...
task build            # or: go build -o bin/koine ./cmd/koine
bin/koine              # interactive
bin/koine -p "summarise this repo"
bin/koine -serve 127.0.0.1:7777   # HTTP/SSE API (Electron / browser)
```

Drop things where you already keep them and they are picked up — nothing to convert:

| You have                      | Put it in                                                                         |
| ----------------------------- | --------------------------------------------------------------------------------- |
| Pi extension (`.ts`)          | `.pi/extensions/` or `~/.pi/agent/extensions/`                                    |
| opencode plugin               | `.opencode/plugin/` or `"plugin": [...]` in `opencode.json`                       |
| Skill (`SKILL.md`)            | `.pi/skills`, `.agents/skills`, `.opencode/skill`, `.claude/skills`               |
| Claude hooks                  | `.claude/settings.json`                                                           |
| Slash commands / sub-agents   | `.claude/commands`, `.opencode/command`, `.claude/agents`, `.opencode/agent`      |
| Extension in **any language** | `.koine/extensions/<name>/extension.json` → `{"command": ["python3","./ext.py"]}` |

A complete Python extension is ~25 lines — see
`core/app/testdata/proj/.koine/extensions/pyext/`.

## Development

[Task](https://taskfile.dev) orchestrates everything (`task` lists all targets):

| Task                                                | What it does                                                                                 |
| --------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `task check`                                        | format check, vet, staticcheck, JS tests, Go tests **with the coverage gate** — what CI runs |
| `task ci`                                           | `check` + race detector                                                                      |
| `task test` / `test:js` / `test:all` / `test:race`  | Go tests · sidecar tests (Node and Bun) · both · with `-race`                                |
| `task cover`                                        | coverage profile + `coverage.html`                                                           |
| `task cover:gate`                                   | fail if any package is under `COVER_MIN` (default 85%; `cmd/koine` 75%)                      |
| `task build` / `run` / `serve`                      | CLI · run in place · HTTP/SSE server                                                         |
| `task tui`                                          | Bubble Tea TUI                                                                               |
| `task desktop:dev` / `desktop:build`                | Wails v2 app (needs the `wails` CLI)                                                         |
| `task fmt` / `fmt:check` / `vet` / `lint` / `clean` | housekeeping                                                                                 |

Without Task installed, `scripts/covergate.sh`, `go test ./...` and
`node --test "core/ext/jstest/*.test.mjs"` are the underlying commands.

### Status

| Area                                                     | State                                                                                                     |
| -------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| Core, line TUI, HTTP server, JS sidecar, hooks, protocol | built and tested — Go packages gated by `task cover:gate`, 29 JS tests on Node and Bun, race-clean        |
| `tui-bubbletea/`, `desktop/` (Wails v2)                  | **written, not compiled** — they need `go mod tidy` with network access (`task tui`, `task desktop:dev`)  |
| Providers                                                | Anthropic, OpenAI-compatible and custom providers via prefix routing (`core/llm`)                         |
| Pi tracking                                              | pinned to Pi 1.5.2; `koine -compat`, `task upstream:check` — see `docs/UPSTREAM.md`                       |
| Not yet                                                  | streaming, context compaction, session persistence, permission prompts, Pi/opencode UI APIs (inert shims) |

## Layout

```
cmd/koine/            CLI entry (+ end-to-end tests against a fake Anthropic API)
core/events          event + decision vocabulary
core/agent           model ↔ tools loop, sub-agents (`task` tool)
core/tools           registry + built-in tools
core/skills|prompts|agents|frontmatter   ecosystem loaders
core/hooks           Claude-compatible command hooks
core/ext             extension host (JSON-RPC) + embedded JS sidecar (sidecar/, jstest/)
core/app             wiring: config, discovery, slash commands
core/server|tui      HTTP/SSE and line-mode frontends
tui-bubbletea/       Bubble Tea frontend (separate module)
desktop/             Wails v2 frontend (separate module)
docs/SPEC.md         normative specification
```

## Quality gates and documentation

CI (`.github/workflows/ci.yml`, `codeql.yml`) runs, for Go and JS: format, lint, line/file length, tests with coverage gates, dependency audit,
vulnerability scan (govulncheck, Trivy), static security scan (gosec, CodeQL) and secret scan (gitleaks). `task check` runs the local equivalents.

Requirements, design and decisions are documented and machine-checked: start at [docs/README.md](docs/README.md). Rules for people and agents:
[docs/ENGINEERING.md](docs/ENGINEERING.md).

## Working on Koine with a coding agent

[AGENTS.md](AGENTS.md) is the entry point for Claude Code (through `CLAUDE.md`), opencode, Pi, Codex and Koine. Shared skills are in `.agents/skills/`,
sub-agents in `.claude/agents/`, commands in `.claude/commands/`.
