---

name: upstream-pi-sync
description: Use when asked to check, update or triage Koine's compatibility with upstream Pi (earendil-works/pi). Covers drift detection, rebasing the manifest and triage.
---

# Sync with upstream Pi

Only upstream Pi (TypeScript) is tracked. Process and rules: `docs/UPSTREAM.md`.

1. `task upstream:check` (or `SRC=<dir>` for a checkout). Exit 1 = drift, 3 = extraction failed (upstream renamed an interface: fix the extractor, do not ignore).
2. `task upstream:rebase`: moves the baseline in `compat/pi.json`; new surface lands as `unsupported`.
3. Triage each new item in `compat/pi.json`: `verified` (needs a named test that exists), `inert`, `unsupported`, or `todo`. Set `behavior` for unsupported properties.
4. `task compat:sync` regenerates `core/ext/sidecar/surface.json`; commit both.
5. Bump `piBaseline` in `cmd/koine/main.go` (a test fails if it disagrees with the manifest).
6. Add a dated entry to `compat/SYNC.md`; record deliberate non-support in `compat/DIVERGENCES.md`.
7. `task check`.

Version evidence must come from the package or checkout, never from web search snapshots.
