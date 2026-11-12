# koine

A Pi-style coding agent in Go, with a terminal UI and a desktop UI, whose
extensibility is **language-agnostic** and **drop-in compatible** with existing
Pi extensions, opencode plugins, Claude-style hooks and `SKILL.md` skills.

- **Core** (`core/`): agent loop, tools, skills/prompts/sub-agents loader, event model, extension host. Standard library only.
- **Frontends**: line-mode TUI (`cmd/koine`, no deps) · Bubble Tea TUI (`tui-bubbletea/`) · Wails v3 desktop (`desktop/`) · HTTP/SSE server (`koine -serve`) for Electron or the browser.
- **Extensions**: JS/TS (Pi + opencode) via a Bun/Node sidecar, any other language via a JSON-RPC-over-stdio protocol, shell commands via Claude-compatible hooks.
- **Pi packages**: `koine install npm:name@1.2.3`, `git:host/user/repo@v1` or a local path (`-l` for the project); `remove`, `update`, `list`, `config`. Hash-locked, `npm --ignore-scripts` by default, project packages only in a trusted folder ([SPEC §11](docs/SPEC.md)).

Full details: **[docs/SPEC.md](docs/SPEC.md)**.

## Quick start

```bash
export ANTHROPIC_API_KEY=sk-ant-...
task build            # or: go build -o bin/koine ./cmd/koine
bin/koine              # interactive
bin/koine -p "summarise this repo"
git diff | bin/koine "review this change" > review.txt   # piped stdin + message; answer on stdout, activity on stderr
bin/koine --mode json @spec.md "list the TODOs" | jq .   # one JSON event per line
bin/koine --mode rpc               # Pi's RPC protocol: JSON commands on stdin, events on stdout (docs/SPEC.md section 12)
bin/koine --serve 127.0.0.1:7777   # HTTP/SSE API (Electron / browser)
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
| MCP server (stdio or HTTP)    | `~/.koine/mcp.json` or `.koine/mcp.json`, `.mcp.json`; `koine mcp add` writes it  |

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
| `task desktop:dev` / `desktop:build`                | Wails v3 app (`go run` / `go build`; Linux needs GTK 4 and WebKitGTK 6.0)                    |
| `task fmt` / `fmt:check` / `vet` / `lint` / `clean` | housekeeping                                                                                 |

Without Task installed, `scripts/covergate.sh`, `go test ./...` and
`node --test "core/ext/jstest/*.test.mjs"` are the underlying commands.

### Status

| Area                                                     | State                                                                                              |
| -------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| Core, line TUI, HTTP server, JS sidecar, hooks, protocol | built and tested — Go packages gated by `task cover:gate`, 29 JS tests on Node and Bun, race-clean |
| `tui-bubbletea/`, `desktop/` (Wails v3)                  | RPC clients of `koine --mode rpc`; built and tested in CI (job `frontends`, `task frontends:test`) |
| Providers                                                | Anthropic, OpenAI-compatible and custom providers via prefix routing, streamed (`core/llm`)        |
| Pi tracking                                              | pinned to Pi 1.5.2; `koine -compat`, `task upstream:check` — see `docs/UPSTREAM.md`                |
| Not yet                                                  | frontends that render streamed text, permission prompts, Pi/opencode UI APIs (inert shims)         |

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
core/rpcclient       client for `koine --mode rpc`, used by the two frontends below
tui-bubbletea/       Bubble Tea frontend (separate module, RPC client)
desktop/             Wails v3 frontend (separate module, RPC client; desktop/bridge holds its logic)
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
