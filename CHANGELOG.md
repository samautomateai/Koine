# Changelog

All notable changes. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow semver once released.

## Unreleased

### Added

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
