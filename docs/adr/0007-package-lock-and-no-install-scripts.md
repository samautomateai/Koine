# ADR 0007 — Packages are locked by content hash and installed without scripts

- Status: accepted
- Date: 2026-11-12
- Requirements: REQ-PKG-001; REQ-PKG-002; REQ-TRUST-001

## Context

Pi packages are third-party code that Koine would load as extensions. An npm `postinstall` script, a moved git tag or a swapped registry tarball runs or ships code the user never looked at. Pi installs missing packages when it starts.

## Decision

Packages are installed only by an explicit command. npm runs with `--ignore-scripts` unless the user passes `--allow-scripts` or sets `allowInstallScripts` in the user settings (a project cannot enable it). Git clones refuse remote helpers. Each install records the version, commit and a sha256 over the package's files in `packages.lock.json`; installing the same version or commit with different content is refused, and a package whose files no longer match is not loaded. Project packages need a trusted folder to install, update, remove and load (ADR 0002).

## Consequences

Packages whose build needs lifecycle scripts need an opt-in. Missing packages are a warning at startup, not a download. The hash is Koine's own, not npm's, so it also covers git and detects edits made after install. Pi's lock behaviour was not compared; see `compat/DIVERGENCES.md`.
