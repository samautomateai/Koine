# Tracking upstream Pi

Koine promises to run Pi extensions. That promise only means something if we
know, at all times, **which Pi release we were measured against** and **what
has changed since**. This document is the process; the data lives in
`compat/pi.json` and the ledger in `compat/SYNC.md`.

**Scope: only upstream Pi (TypeScript, earendil-works/pi) is tracked.** PiG, pi-go, the sky-valley port and Zero are reference reading only (`docs/REFERENCES.md`); we do not follow their releases or maintain parity with them.

## The three layers

| Layer | What it answers | Where |
|---|---|---|
| Manifest | Every upstream extension surface item (events, `pi.*`, `ctx.*`, `ctx.ui.*`, tool fields, slash commands) and our status for it: `verified` (names a passing test) · `inert` (accepted, no effect) · `unsupported` (stand-in, reported by `/compat`) · `todo` | `compat/pi.json`, validated by `compat/compat.go` |
| Conformance | Does the runner actually behave as the manifest claims? The test reads the manifest and executes each item against the real sidecar (Node and Bun) | `core/ext/jstest/conformance.test.mjs` |
| Drift check | What did upstream add or remove since the pinned baseline? Extracted from upstream's own `types.ts` / `slash-commands.ts` | `scripts/upstream-check.mjs`, tests in `compat/test/` |

`koine -version` prints `koine <ver>+pi<baseline>`; `koine -compat` prints the
status report; `/compat` inside a session lists what the *loaded extensions*
tried to use that we do not implement.

## Routine (weekly, or when Pi ships)

```
task upstream:check                 # npm pack the latest Pi, diff its surface (exit 1 = drift)
task upstream:check SRC=../pi       # or against a checkout / unpacked package
task upstream:rebase                # move the baseline; new surface lands as `unsupported`
# triage each new item in compat/pi.json: verified (write a test) / inert / unsupported / todo
task compat:sync                    # regenerate core/ext/sidecar/surface.json
task check                          # all gates; the baseline-vs-manifest test must pass
```

Then add a dated entry to `compat/SYNC.md` and, if we chose not to follow
something, to `compat/DIVERGENCES.md`.

## Rules

1. **`verified` needs a named test.** The manifest validator rejects it otherwise.
2. **New upstream surface is never silently accepted.** Rebase adds it as `unsupported`; a human triages it.
3. **Removed upstream surface is flagged (`removedUpstream`), not deleted**, so old extensions keep working and history is preserved.
4. **If the extractor cannot find an interface it exits 3**, instead of reporting "no drift" because it looked at nothing.
5. **Version evidence comes from the package or checkout, never from web snapshots.** Search-engine and package-index pages for Pi were found to be months stale; the manifest records this under `upstream.observed`.
6. `surface.json` is generated from the manifest and a test fails if they disagree.

## Why not port Pi file-by-file?

Koine's contract is narrower: the *extension surface* of Pi is tracked exactly, the rest of the agent is ours.
