# Definition of done: documentation and parity are part of the change

A change is not finished until the documents that describe it say the same thing as the code. CI enforces the minimum
(`scripts/docscheck.mjs`); **you are responsible for the judgement**: work out what you implemented or modified, then update every place that
records it. Do this in the same change, never "later". This rule is not waived for small changes, refactors with visible effects, fixes or
tooling.

## What CI checks

- Product code under `core/`, `cmd/`, `scripts/`, `compat/` changed ⇒ `CHANGELOG.md` has a new line **and** something under `docs/` or the
  compat manifests (`compat/pi.json`, `compat/settings.json`, `compat/*.md`) changed.
- The only opt-out is `Docs-Impact: none — <specific reason>` in the PR description (for example "renames a private helper; no behaviour,
  flag, setting or interface changed"). A bare `none`, a placeholder or "n/a" does not count.
- Requirements ↔ design ↔ tests link up, statuses are valid, relative links resolve.

CI cannot tell whether the right documents were updated. Use the table.

## If you changed… update…

| You changed                                                                    | Update                                                                                                                                       |
| ------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Any behaviour a user or extension can observe                                  | `docs/requirements.md` (status, statement, tests), `docs/design.md` (satisfies, code paths), `CHANGELOG.md`                                  |
| A command-line flag, slash command, exit code or output format                 | `docs/SPEC.md`, the usage text in `cmd/koine/args.go`, `README.md` if it is a common workflow, the matching `docs/gap/cli.md` rows           |
| A setting (a key in `settings.json`)                                           | `compat/settings.json` (status, type, test), `docs/SPEC.md`, `docs/gap/cli.md` (CLI-38..41), and Pi's key name unless Koine-only             |
| Extension API, event, tool or hook behaviour                                   | `compat/pi.json` through `task compat:sync` / `task upstream:rebase` (never by hand for upstream changes), `docs/gap/ext.md`, `docs/SPEC.md` |
| Provider, model, retry, streaming or token handling                            | `docs/gap/llm.md`, `docs/requirements.md` (REQ-LLM-\*), `docs/SPEC.md` if the wire behaviour changed                                         |
| A tool (read, bash, edit …) or its arguments                                   | `docs/gap/tools.md` (TOOL rows), `docs/SPEC.md`                                                                                              |
| Sessions, compaction, context                                                  | `docs/gap/tools.md` (SESS, CMP rows), `docs/design.md` (DES-011/012 notes), `docs/SPEC.md`                                                   |
| A structural choice with real alternatives (storage, a dependency, a protocol) | a new ADR in `docs/adr/` (superseding the old one if you reverse it); a new third-party dependency in the Go core always needs one           |
| A build, CI, lint or dependency rule                                           | `docs/ENGINEERING.md`, `AGENTS.md` if agents must know, `CHANGELOG.md`                                                                       |
| Something you deliberately do not copy from Pi                                 | `compat/DIVERGENCES.md` with the reason, and the gap row marked N/A-by-design                                                                |
| Upstream Pi moved (baseline bump)                                              | `compat/SYNC.md` ledger entry, `docs/UPSTREAM.md` if the process changed                                                                     |

## The parity tracker

`docs/gap/*.md` and `docs/gap-assessment.md` are the parity tracker; `compat/pi.json` and `compat/settings.json` are the machine-readable
part. When a change closes, narrows or opens a gap:

1. Set the row's Koine status (`HAVE`, `PARTIAL`, `MISSING`, `N/A-by-design`) to what the code now does, and write in Evidence/Notes what is
   still missing. Do not mark `HAVE` for partial behaviour.
2. A `verified` item in a manifest names a test that exists. Never mark something verified without one.
3. If the change is bigger than the row (new behaviour with no row), add a row; if it exposes a gap nobody has recorded, record it.
4. Update the requirement that the row maps to, so the tracker, the requirement and the tests agree.

## Before you say "done"

- [ ] `task check` passes.
- [ ] I can name each document I updated and why, or I wrote `Docs-Impact: none — <reason>`.
- [ ] The parity tracker rows for this change match what the code now does.
- [ ] The final message to the user lists the docs and trackers I updated.
