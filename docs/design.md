# Design

Living document. Each design section has an ID, the requirements it satisfies, and the code that implements it.
`scripts/docscheck.mjs` verifies that the requirement IDs exist and the code paths exist. Decisions with trade-offs get an ADR in
`docs/adr/`. The wire protocol and user-visible behaviour are specified in `docs/SPEC.md`.

## DES-001 — Layered core, standard library only

- Satisfies: REQ-DISC-001; REQ-AGENT-001
- Code: core/app; core/agent; core/skills; core/agents; core/prompts
- Notes: `core/app` wires config, discovery, extensions, hooks, provider and agent. Frontends call `app.HandleInput` and render events. The Go core has no third-party dependencies, which keeps the supply chain small and the audit trivial (ADR 0004).

## DES-002 — Events and decisions

- Satisfies: REQ-EXT-001; REQ-HOOK-001
- Code: core/events; core/hooks
- Notes: Every interception point emits an event. Handlers return a `Decision` (block, rewrite arguments, replace output, append to the system prompt). `Multi` merges decisions from hooks and extensions; the first block wins.

## DES-003 — Extension host and wire protocol

- Satisfies: REQ-EXT-001
- Code: core/ext/host.go
- Notes: One child process per extension, speaking JSON-RPC 2.0 over stdio. Host-to-extension calls need a reply; extension-to-host registrations are notifications. Errors and crashes are contained to the extension.

## DES-004 — JS sidecar for Pi and opencode

- Satisfies: REQ-EXT-002; REQ-EXT-003; REQ-EXT-004
- Code: core/ext/sidecar.go; core/ext/sidecar/runner.mjs; core/ext/sidecar/shims
- Notes: Pi and opencode plugins are JavaScript, so a Node or Bun process hosts them. Imports of `@earendil-works/*`, `@opencode-ai/*`, zod and typebox resolve to shims. Unsupported properties and methods return stand-ins and are reported (ADR 0001).

## DES-005 — Trust boundary

- Satisfies: REQ-TRUST-001; REQ-TRUST-002; REQ-SKILL-001
- Code: core/trust; core/app/discover.go; core/app/config.go; core/skills
- Notes: Config is loaded in two scopes, user and project. Only user scope may declare code or credential routing unless the folder is trusted. Trust is recorded per directory, inherited by descendants, and stored with owner-only permissions (ADR 0002).

## DES-006 — Compatibility manifest and drift pipeline

- Satisfies: REQ-PI-001; REQ-PI-002; REQ-PI-003; REQ-EXT-004
- Code: compat/compat.go; compat/pi.json; scripts/upstream-check.mjs; core/ext/jstest/conformance.test.mjs
- Notes: The manifest is the single source of truth for what Pi surface exists and how far Koine goes. The sidecar's behaviour for unsupported properties is generated from it. A conformance test executes the manifest against the real runner (ADR 0003).

## DES-007 — Provider router

- Satisfies: REQ-LLM-001; REQ-LLM-002
- Code: core/llm
- Notes: A `Provider` interface with Anthropic and OpenAI-compatible implementations. `Router.Resolve` maps a model id to a provider from built-in names, `prefix/model`, and user-declared providers. Shared HTTP helper owns retry policy.

## DES-008 — Frontend contract

- Satisfies: REQ-FE-001; REQ-FE-002
- Code: core/server; core/tui; cmd/koine
- Notes: Frontends receive a stream of events (`text`, `tool_use`, `tool_result`, `notice`, `error`, `done`). The HTTP server allows one run at a time. The line-mode TUI is dependency-free; Bubble Tea and Wails are separate modules.

## DES-009 — Quality, documentation and agent workflow

- Satisfies: REQ-QA-001; REQ-DOC-001; REQ-AGENTDEV-001
- Code: scripts/sizecheck.mjs; scripts/docscheck.mjs; scripts/covergate.sh; .github/workflows/ci.yml; Taskfile.yml; AGENTS.md
- Notes: `task check` is the local mirror of CI. Rules live in `docs/ENGINEERING.md`; agents read `AGENTS.md`, which points to them. Docs are checked, not trusted (ADR 0005).

## DES-010 — Command line: parse, build one prompt, run print mode

- Satisfies: REQ-CLI-001
- Code: cmd/koine/args.go; cmd/koine/print.go; cmd/koine/main.go; core/app/runopts.go
- Notes: `parseArgs` is a small hand-written parser (the standard `flag` package stops at the first positional and has no `--name=value` plus `@file` handling). `run` takes injected `streams` so the whole command line is testable without touching the process's stdio. `buildPrompt` reads stdin only when it is not a terminal. Per-run overrides travel in `app.RunOptions` inside `app.Options`, and are re-applied by `load`, so `/reload` does not drop them. Text mode keeps only the text after the last tool call as the answer, which makes `koine ... | tool` safe to pipe; JSON mode forwards `agent.Event` unchanged.
