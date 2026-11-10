# AGENTS.md — instructions for coding agents

Koine is a Go coding agent that also runs Pi, opencode and Claude-style extensions. This file is read by Claude Code (via `CLAUDE.md`),
opencode, Pi, Codex and Koine itself. Keep it short; the detail is in the linked documents.

## Before you change anything
1. Read `docs/requirements.md` and `docs/design.md`. Find the requirement (`REQ-…`) your change belongs to, or add one.
2. Behaviour change → requirement first, then a failing test, then code, then docs. Process: `.agents/skills/spec-driven-change/SKILL.md`.
3. Rules and thresholds: `docs/ENGINEERING.md`. Wire protocol and user-visible behaviour: `docs/SPEC.md`.

## Commands (go-task; `task` lists all)
| Do | Run |
|---|---|
| Everything CI runs, locally | `task check` |
| Fast loop while editing | `task check:fast` |
| Go tests / JS tests | `task test` / `task test:js` |
| Fix formatting | `task fmt` (Go) and `npm run format` (JS) |
| Docs, requirement and link consistency | `task docs:check` |
| Pi compatibility / upstream drift | `task compat:status` / `task upstream:check` |

## Rules that CI enforces (so do not fight them)
- Go core uses only the standard library (ADR 0004). New dependency = new ADR.
- Lines ≤ 160 columns, files ≤ 600 lines (`task size`). Split files rather than raising limits.
- Per-package Go coverage ≥ 85%; JS lines ≥ 85%. New code ships with tests in the same change.
- A `verified` item in `compat/pi.json` must name a test that exists. Never mark something verified without one.
- Product code under `core/`, `cmd/`, `scripts/`, `compat/` changes together with `docs/` or `CHANGELOG.md`, or the PR says `Docs-Impact: none` and why.
- Project-local code must stay behind the trust gate (ADR 0002). Do not add a path that runs it earlier.

## Things that go wrong
- Do not edit `core/ext/sidecar/surface.json` or `compat/pi.json` by hand for upstream changes: use `task upstream:rebase` and `task compat:sync`.
- Do not paste invisible Unicode (zero-width, BOM, tag characters) into sources; use escapes such as `​` in code. `core/skills` rejects such skills on purpose.
- Only upstream Pi (TypeScript) is tracked. PiG, pi-go, sky-valley/pi and Zero are reference reading (`docs/REFERENCES.md`).
- Tests that start the app must isolate `HOME` and set `KOINE_TRUST` deliberately (see `isolate(t)` in `core/app`).

## Layout
`core/` agent, tools, events, hooks, skills, ext host + JS sidecar, llm, trust, server, tui · `cmd/koine` CLI · `compat/` Pi manifest and drift tests ·
`scripts/` repo tooling · `docs/` requirements, design, ADRs, spec · `tui-bubbletea/`, `desktop/` separate modules.

## Agent setup in this repo
Skills live in `.agents/skills/` (read by Pi, opencode, Koine; `.claude/skills` is a symlink for Claude Code). Sub-agents are in `.claude/agents/`
(also exposed to opencode via `.opencode/agents`). Commands are in `.claude/commands/` (symlinked for opencode and Pi). Claude-specific settings and
hooks are in `.claude/settings.json`; opencode's in `opencode.json`. `TestRepoAgentConfigLoads` fails if any of this stops loading in Koine.
