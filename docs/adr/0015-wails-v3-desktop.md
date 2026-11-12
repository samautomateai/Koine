# ADR 0015 — The desktop app moves from Wails v2 to Wails v3

- Status: accepted
- Date: 2026-11-12
- Requirements: REQ-FE-002; REQ-FE-003

## Context

ADR 0013 kept the desktop app on Wails v2. Wails v2 pulls in `github.com/labstack/echo/v4` for its development web server, and two Dependabot alerts on Echo v4 (#1: the proxy middleware forwards an attacker-supplied `X-Real-IP`; #2: the rate limiter can be bypassed with `X-Forwarded-For`) list every v4 release as affected. The fix is in Echo v5, a different module path that Wails v2 will not adopt. Koine's own code uses neither middleware, so the alerts were recorded, not fixed.

Wails v3.0.0 is released. Its `v3/go.mod` and `go.sum` (tag `v3.0.0`) contain no `labstack/echo`; the only Echo imports in that repository are under `v2/`. v3 needs Go 1.26 or newer (the repository is on 1.27.4), has an explicit application and window API, registers Go types as services instead of binding them through a context, and on Linux defaults to GTK 4 and WebKitGTK 6.0 (GTK 3 stays available with the build tag `gtk3` until v3.1).

## Decision

`desktop/` is ported to Wails v3 (`github.com/wailsapp/wails/v3`). `desktop/bridge` is unchanged: it imports no GUI code and keeps its 85% coverage gate. The glue in `desktop/main.go` becomes a `Service` type that wraps `bridge.Backend`, exposes only the calls the page needs (`Info`, `Send`, `Cancel`, `Answer`, `Dismiss`) and starts and stops the `koine --mode rpc` child in `ServiceStartup` and `ServiceShutdown`. A test lists the service's exported methods so that nothing else (the channel and function parameters of `Attach`, `SetEmit` ...) reaches the page.

The page stays a single HTML file with no build step. It imports the runtime that the application serves at `/wails/runtime.js` and calls the service by name (`Call.ByName("main.Service.Send", text)`), so there are no generated bindings to commit or to generate in CI, and no `wails3` command line tool is needed to build: `go build` is enough. If the page ever uses generated bindings, `wails3 generate bindings` belongs in the build and its output in `.gitignore`.

On Linux CI uses the default GTK 4 stack (`libgtk-4-dev`, `libwebkitgtk-6.0-dev`) and no build tag, which is the path Wails v3 supports going forward; the `webkit2_41` tag of v2 is gone.

## Consequences

The Echo v4 advisories no longer apply to Koine because the module is gone from `desktop/go.mod` and `go.sum`. This supersedes the Wails v2 choice and the Echo exception in ADR 0013; the rest of ADR 0013 (separate modules, RPC, dependency policy) stands. Wails v3.0.0 pulls a larger module graph than v2 (its command line tooling shares the module), which Dependabot and Trivy now scan; `go.sum` is larger. Users of the desktop app on Linux need GTK 4 and WebKitGTK 6.0 at run time. The window and the page are still not rendered by any test; the service wrapper's method set is.
