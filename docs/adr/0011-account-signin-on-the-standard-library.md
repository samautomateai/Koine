# ADR 0011 — Account sign-in on the standard library, with Pi's client ids and a refreshing store

- Status: accepted
- Date: 2026-11-12
- Requirements: REQ-AUTH-003

## Context

Pi lets users sign in with a Claude, ChatGPT or GitHub Copilot subscription instead of an API key. The flows are OAuth authorization code with PKCE and a loopback redirect, or device codes. They use public client ids that belong to the vendors' own tools; there is no registration Koine could do. Libraries such as golang.org/x/oauth2 exist but ADR 0004 keeps the core on the standard library, and the flows are small.

## Decision

Implement the flows with `net/http`, `crypto/rand` and `crypto/sha256`, using the client ids, endpoints, scopes and headers Pi uses, fixed in code. Store logins in `auth.json` in Pi's shape so Pi and Koine can read each other's entries. Renew on demand, inside the credential lookup, holding the store lock across the renewal. Keep the precedence list unchanged. Treat OpenRouter's login as a way to obtain an API key and store it as one. Support the headless case with `--no-browser` and with a pasted redirect URL, not with a second port or tunnel. Show a terms-of-service notice for the Claude login.

## Consequences

No dependency and every request is readable in `core/auth/oauth`. We depend on client ids and behaviour that vendors may change or restrict; Anthropic in particular may refuse subscription tokens outside its own tools, and a break needs a code change (the endpoints are not configurable on purpose, so that a project file cannot redirect a login). Holding the lock across a renewal blocks other credential lookups in the process for the duration of one HTTP call (bounded by a 45 second timeout). There is no cross-process lock, so two Koine processes renewing together can invalidate a single-use refresh token; the user signs in again. Nothing here runs project code, so ADR 0002 is not involved.
