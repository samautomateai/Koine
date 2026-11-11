# Engineering rules

These rules apply to people and to coding agents. Each one is either enforced by a command (shown) or is a review criterion (marked _review_).
`task check` runs every enforced rule locally; CI runs the same commands.

## 1. Spec first (`task docs:check`)

1. Every behaviour has a requirement ID in `docs/requirements.md`. Change behaviour → change or add the requirement in the same PR.
2. A requirement is `implemented` only with `Tests:` that exist, and a `Design:` section that lists it under `Satisfies:`.
3. Structural choices with real alternatives get an ADR (`docs/adr/`), numbered, with Status and Date. Reversing one means a new ADR that supersedes it.
4. Docs are living: product code under `core/`, `cmd/`, `scripts/`, `compat/` must change together with `docs/`, `CHANGELOG.md` or `compat/*.md`, or the PR states `Docs-Impact: none` with a reason. Broken relative links fail.
5. User-visible changes get a line under "Unreleased" in `CHANGELOG.md`.

## 2. Code

1. Go core and CLI use only the standard library (ADR 0004). _review_: no new module without an ADR.
2. Format: `gofmt -s` for Go, Biome (`biome.json`) for JS/JSON. (`task fmt:check`, `npm run format:check`)
3. Lint: golangci-lint with `errcheck`, `govet`, `staticcheck`, `gosec`, `errorlint`, `bodyclose` and others (`.golangci.yml`); Biome lint (`biome.json`). Exclusions need a written reason in the config. (`task lint`)
4. Size: lines ≤ 160 columns (tabs = 4), files ≤ 600 lines, Go, JS and TS alike. Split, do not raise. (`task size`)
5. Errors are handled or deliberately discarded with `_ =`. Tool and extension failures are reported to the user or model as text and never crash the host.
6. Comments say why, not what. Exported identifiers have doc comments. _review_
7. No invisible Unicode in sources; use escapes. _review_

8. Toolchain: build and CI on the latest stable Go, pinned once in the `go` line of `go.mod` (CI reads it from there; bump it by hand when a new Go ships), golangci-lint built with at least that Go, Biome pinned in `package.json`. TypeScript, if we add first-party TS, is type-checked with `tsc --noEmit`; Biome parses it without depending on the TypeScript package (ADR 0006).

## 3. Tests

1. New behaviour ships with tests in the same change; a bug fix starts with a failing test.
2. Go coverage ≥ 85% per package (`cmd/koine` ≥ 75%, see `scripts/covergate.sh`). JS coverage ≥ 85% lines, 70% branches (`npm run test:cover`). Raise thresholds over time; never lower them to pass.
3. Go tests run with `-race` in CI (`task test:race`). Tests must not touch the real home directory or network; use `isolate(t)` and fake servers.
4. Anything claimed `verified` in `compat/pi.json` names a test that exists (`TestVerifiedItemsNameRealTests`).

## 4. Security

1. Project-local code (extensions, hooks, provider routes) never runs before the folder is trusted (ADR 0002). _review_ any change near `core/app/discover.go`, `config.go`, `core/trust`.
2. Dependency audit: `go mod verify`, `go mod tidy -diff`, `npm audit --audit-level=high`, dependency review on PRs.
3. Vulnerability scan: `govulncheck` and Trivy (high/critical, fixable).
4. Static security scan: `gosec` (through golangci-lint) and CodeQL for Go and JavaScript.
5. Secret scan: gitleaks on full history. Never commit keys; fixtures use obviously fake values.
6. Subprocess and file access in tools is the product (gosec G204/G301/G304/G306/G703 are excluded for that reason); everything else in gosec stays on.

## 5. Upstream Pi

Only upstream Pi is tracked. Follow `docs/UPSTREAM.md`; the baseline in the binary must equal the manifest's.

## 6. Changes and reviews

1. Small PRs, one concern each. Conventional prefix in the title (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`).
2. Merge requires the `all gates passed` check. Run the `reviewer` agent (or a human) against this document before merging.
3. Definition of done: requirement + design + tests + code + docs + changelog, `task check` green.

## 7. Agent workflow

`AGENTS.md` is the entry point for every coding agent. Skills in `.agents/skills/` carry the procedures (`spec-driven-change`, `quality-gates`, `upstream-pi-sync`). An agent must not lower a threshold, add an exclusion, or mark a requirement implemented without tests in order to get green.

## Keeping dependencies current

- `task deps:check` lists outdated pins (Go toolchain, golangci-lint, govulncheck, Biome, GitHub Action refs), Go modules in all three modules and npm packages.
- `task deps:update` upgrades all of them and runs `task check:fast`. The weekly `dependency-updates` workflow does the same and opens a PR; Dependabot still raises security updates.
- Never pin a version by hand in only one place: `scripts/deps.mjs` knows every location. There is no Dockerfile; if one is added, teach `scripts/deps.mjs` its base-image tag.
