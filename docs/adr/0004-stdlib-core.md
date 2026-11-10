# ADR 0004 — The Go core depends only on the standard library
- Status: accepted
- Date: 2026-11-10
- Requirements: REQ-QA-001

## Context
Every dependency widens the attack surface and the audit burden of a tool that runs commands on a developer's machine.

## Decision
`core/` and `cmd/` import no third-party modules. UI frameworks (Bubble Tea, Wails) live in separate modules.

## Consequences
We write small things ourselves (frontmatter parsing, retry). A new dependency in the core needs an ADR that supersedes this one.
