# ADR 0005 — Documentation is checked, not trusted
- Status: accepted
- Date: 2026-11-10
- Requirements: REQ-DOC-001; REQ-QA-001; REQ-AGENTDEV-001

## Context
Specifications rot when nothing fails as they drift. Coding agents amplify this: they change code quickly and rarely re-read prose.

## Decision
Requirements, design sections and ADRs carry IDs. `scripts/docscheck.mjs` verifies links, IDs, statuses, test references and code paths, and requires a docs change alongside product code changes unless the change states `Docs-Impact: none`. `AGENTS.md` makes the same rule part of every agent's instructions.

## Consequences
Small overhead per change, in exchange for traceable requirements and fewer silent regressions.
