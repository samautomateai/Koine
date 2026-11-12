# ADR 0010 — Sessions stay Pi-compatible JSONL (version 3), not SQLite

- Status: accepted
- Date: 2026-11-12
- Requirements: REQ-SESS-001; REQ-SESS-002

## Context

A coding agent keeps a conversation tree per project. Two storage designs were considered. SQLite (as some agents use) gives indexed queries, transactional writes and one file for everything. Pi's session format is a JSONL file per session: a header line, then one entry per line, each with an `id` and a `parentId` that make the tree (`version` 3). Koine's stated goal is to run Pi's sessions, extensions and tools unchanged, and extensions read and append to those files.

## Decision

Koine keeps Pi's JSONL v3 as its only session store (`~/.koine/sessions/--<cwd>--/<timestamp>_<uuid>.jsonl`). Older Pi versions (1 and 2) are migrated on open after saving a `.v<N>.bak` copy. No SQLite database is added, and no separate index is kept.

Reasons, in order of weight:

1. Compatibility is the product. A Pi user can point Koine at their sessions, and a Pi extension or script that reads `ctx.sessionManager` data or the file itself keeps working. A second store would need a converter in both directions and would drift.
2. The format fits the data. Sessions are append-only trees; a line per entry is a log, a crash can damage at most the line being appended, and earlier entries are never rewritten.
3. It stays inspectable and portable: `grep`, `jq`, `git`, `cp` and a text editor work; export to HTML or JSONL is a copy; there is no schema migration tool to ship.
4. ADR 0004: SQLite in Go needs either cgo or a third-party pure-Go driver, a new dependency in the core.

## Consequences

Listing and searching many large sessions reads files rather than querying an index; this has been enough so far. Concurrent writers to one session are not coordinated beyond appending whole lines. If search over history or multi-process access becomes a requirement, add an index beside the files (a rebuildable cache), and write a new ADR that supersedes this one rather than replacing the files.
