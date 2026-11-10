# ADR 0001 — Run Pi and opencode plugins in a JS sidecar
- Status: accepted
- Date: 2026-11-10
- Requirements: REQ-EXT-002; REQ-EXT-003; REQ-EXT-004

## Context
Pi extensions and opencode plugins are TypeScript or JavaScript that import host libraries. Reimplementing a JS runtime in Go, or porting each plugin, would break "existing extensions keep working".

## Decision
Host them in a Node (type stripping) or Bun child process speaking the same JSON-RPC protocol as every other extension. Host libraries are supplied as shims. Unsupported API surface degrades to stand-ins and is reported.

## Consequences
Node 22+ or Bun is needed only if a JS extension is used. Fidelity is bounded by the shims, so the compatibility manifest (ADR 0003) states exactly how far we go.
