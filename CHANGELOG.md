# Changelog

All notable changes. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow semver once released.

## Unreleased

### Added

- Core agent with TUI, HTTP/SSE server, language-agnostic extension protocol, JS sidecar for Pi and opencode plugins, Claude-compatible hooks.
- Project trust gate, skill hidden-text audit, multi-provider routing.
- Pi compatibility manifest pinned to Pi 1.5.2, `koine -compat`, drift checker.
- CI quality gates: format, lint, size, coverage, dependency audit, vulnerability and security scanning for Go and JS.
- Requirements, design and ADR documents with automated consistency checks; agent configuration for Claude Code, opencode, Pi and Koine.
- Biome replaces Prettier and ESLint for JS; CI pins Go 1.27.4 and current action versions (ADR 0006).
