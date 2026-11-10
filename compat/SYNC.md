# Sync ledger

One entry per rebase of `compat/pi.json`. Newest first.

## 1.5.2 — 2026-11-10 (initial baseline)

- Source: upstream source tree supplied by the user (all `@earendil-works/*` packages at 1.5.2).
- Extraction cross-checked against the TypeScript compiler API: exact match, zero drift.
- 197 items: 51 verified, 30 inert, 97 unsupported, 19 todo (see `koine -compat`).
- Earlier drafts of the manifest were based on web snapshots (0.68–0.77) that turned out to be stale; they were discarded rather than migrated.
- Triage of 1.5.2 surface is the first backlog: see `todo` and `unsupported` in the report.
