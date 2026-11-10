# ADR 0003 — Track Pi through a tested manifest

- Status: accepted
- Date: 2026-11-10
- Requirements: REQ-PI-001; REQ-PI-002; REQ-PI-003

## Context

"Compatible with Pi" is meaningless without a stated version and a way to see what moved. Web snapshots of Pi's versions proved unreliable.

## Decision

`compat/pi.json` lists every upstream extension surface item with a status. Only `verified` may claim parity and it must name a real test. Version evidence comes from the package or checkout. Only upstream Pi is tracked; other Pi ports are reference reading.

## Consequences

Each Pi release costs a triage pass (`docs/UPSTREAM.md`). In return, drift is detected mechanically and the claim of compatibility is falsifiable.
