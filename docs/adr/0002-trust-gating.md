# ADR 0002 — Gate project-local code behind folder trust

- Status: accepted
- Date: 2026-11-10
- Requirements: REQ-TRUST-001; REQ-TRUST-002; REQ-SKILL-001

## Context

Koine loads extensions, hooks and provider routes from many ecosystems' directories. A cloned repository could otherwise execute code or redirect API keys on first open.

## Decision

Project scope is untrusted by default. Code and credential routing from the project load only after `/trust`, `-trust` or `KOINE_TRUST=1`. Skills are screened for invisible text regardless of trust.

## Consequences

First run in a new repository needs one explicit step, and the user is told what was skipped. This is a deliberate divergence from Pi (see `compat/DIVERGENCES.md`).
