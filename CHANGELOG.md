# Changelog

All notable changes. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow semver once released.

## Unreleased

### Added

- Session tree (REQ-SESS-002): `/tree [filter|id]` lists the entry tree and moves the conversation to an earlier entry, optionally summarising the branch it leaves (`--summary`; `branchSummary.reserveTokens`, `branchSummary.skipPrompt` and `treeFilterMode` now take effect); `/label` bookmarks entries; `/clone` and `/fork <id>` copy a branch to a new session; `/export [file]` writes JSONL or a self-contained HTML page; `-r` and `/resume` offer a numbered picker. v1 and v2 session files are migrated to v3 on load (original kept as `<file>.v<N>.bak`). Appends never alter existing lines of a Pi file, add a missing final newline, and copies keep entry lines (custom entries, unknown fields) as read. `/session` shows entry, branch and label counts.
- `$KOINE_AGENT_DIR` moves Koine's user directory (default `~/.koine`), like Pi's `PI_CODING_AGENT_DIR`. `SYSTEM.md` replaces and `APPEND_SYSTEM.md` extends the system prompt, from a trusted project (`.koine/` or `.pi/`) or else the user directory; `--system-prompt` and `--append-system-prompt` still win. (REQ-CFG-002)
- Model catalog and token accounting: built-in Claude context windows, output limits and prices (override or extend in `~/.koine/model-catalog.json`); usage from Anthropic and OpenAI-compatible responses is saved on each assistant message in Pi's `usage` shape, a `usage` event reports each response, and `/session` shows tokens, cost (or "unknown") and context use. Compaction now works from the provider's reported context size and takes the window from the catalog unless `compaction.contextWindow` is set. Prices come from third-party pages and are not yet verified against Anthropic. (REQ-LLM-004)
- Settings use Pi's key names in `~/.koine/settings.json` and `.koine/settings.json` (`config.json` still loads). `defaultProvider`, `defaultModel`, `defaultTools`, `sessionDir`, `extensions` and `compaction.*` take effect; every other Pi key is listed in `compat/settings.json` with its status and reported at startup if set. `task upstream:check` now also diffs Pi's settings. `$KOINE_SESSION_DIR` is now the session directory itself, like Pi's. (REQ-CFG-001)
- `docs/CHECKLIST.md`: documentation and parity-tracker updates are part of every change; CI now requires a `CHANGELOG.md` line plus a docs/tracker update, and a real reason for `Docs-Impact: none`.
- Compaction: automatic summarising before the context window fills, `/compact [focus]`, and one compact-and-retry on a provider overflow; saved in the session file (REQ-CMP-001). Settings under `compaction` in config.
- Sessions: conversations are saved in Pi v3 JSONL; `-c`, `--session`, `--fork`, `-n`, `--no-session`, `--session-dir`, `-r`, and `/new /session /name /resume /fork` (REQ-SESS-001). `/clear` is now an alias of `/new`.
- Scriptable command line: positional message, `@file`, piped stdin, `-p`, `--mode json`, `--model`, `--system-prompt`, `--append-system-prompt`, `-t`, `-nt`, `-h`, `-v`; answer on stdout, activity on stderr (REQ-CLI-001).
- `task deps:check` / `task deps:update` and a weekly `dependency-updates` workflow keep the Go toolchain, tool pins, action refs, Go modules and npm packages current (`scripts/deps.mjs`).
- Core agent with TUI, HTTP/SSE server, language-agnostic extension protocol, JS sidecar for Pi and opencode plugins, Claude-compatible hooks.
- Project trust gate, skill hidden-text audit, multi-provider routing.
- Pi compatibility manifest pinned to Pi 1.5.2, `koine -compat`, drift checker.
- CI quality gates: format, lint, size, coverage, dependency audit, vulnerability and security scanning for Go and JS.
- Requirements, design and ADR documents with automated consistency checks; agent configuration for Claude Code, opencode, Pi and Koine.
- Biome replaces Prettier and ESLint for JS; CI pins Go 1.27.4 and current action versions (ADR 0006).
- Desktop module: Wails v2.13.0 (fixes CVE-2026-34157, found by the Trivy gate).
- Parity gap assessment against Pi 1.5.2 (222 behaviours) with prioritised phases and eight planned requirements.
- UI modules pin fixed floors for vulnerable transitive dependencies (x/net, x/text).
- Dependabot opens one grouped PR per ecosystem per month (majors stay separate).
- Go 1.27.4 is the toolchain in all modules; CI reads it from go.mod.
