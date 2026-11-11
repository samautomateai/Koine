---

name: spec-driven-change
description: Use for any behaviour change, new feature or bug fix in Koine. Walks requirement → design → test → code → docs and keeps docs/requirements.md, docs/design.md and tests linked.
---

# Spec-driven change

Docs here are checked by `task docs:check`; a mismatch fails CI.

1. **Find or add the requirement.** Search `docs/requirements.md` for the behaviour. If none fits, add `### REQ-<AREA>-NNN — Title` with `Status: planned`, a one-sentence `Statement`, and `Design: none yet`.
2. **Design if it is new structure.** Add or extend a `## DES-NNN` section in `docs/design.md` (`Satisfies:`, `Code:` paths). If you are choosing between real alternatives, copy `docs/adr/0000-template.md` to the next number and fill it in.
3. **Write the failing test first.** Go: a `TestXxx` in the package. JS: a `test("…")` under `core/ext/jstest` or `compat/test`. Run it and watch it fail for the right reason.
4. **Implement the smallest change** that passes. Keep to `docs/ENGINEERING.md` (stdlib only in core, 160 columns, 600 lines).
5. **Flip the requirement.** Set `Status: implemented`, add `Design:` and `Tests:` (Go names or `js:<title prefix>`, separated by `;`). Make the DES section list the REQ under `Satisfies`.
6. **Update every document and tracker the change touches.** Open `docs/CHECKLIST.md`, identify what you implemented or modified, and update each place it lists: `docs/SPEC.md`, `README.md`, `CHANGELOG.md` under "Unreleased", the parity tracker rows in `docs/gap/` (status and notes must match the code), `compat/pi.json` / `compat/settings.json`, ADRs. This is not optional and not for later.
7. **Run `task check`.** Fix everything it reports. Do not lower thresholds or add exclusions to get green.

If the change genuinely needs no doc change (internal refactor, test-only), say `Docs-Impact: none — <specific reason>` in the PR description; a bare `none` is rejected. In your final message, list the docs and trackers you updated.
