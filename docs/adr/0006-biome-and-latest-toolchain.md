# ADR 0006 — Biome for JS/TS tooling; latest stable Go

- Status: accepted
- Date: 2026-11-10
- Requirements: REQ-QA-001

## Context

ESLint with typescript-eslint trails new TypeScript releases, and Prettier plus ESLint is two tools and two configs for a small JS surface. CI also read its Go version from `go.mod`, which pinned it to an old release.

## Decision

Biome formats and lints JS/JSON (and TS if we add it) from one config, installed as a pinned dev dependency. Type checking, if needed, is `tsc --noEmit`. CI pins the latest stable Go patch and a golangci-lint built with at least that Go; `go.mod` keeps the minimum language version.

## Consequences

One fast tool, no peer-dependency coupling to TypeScript. Version bumps are manual until Dependabot covers the pinned CI values. Biome's style differs slightly from Prettier, so the code was reformatted once.
