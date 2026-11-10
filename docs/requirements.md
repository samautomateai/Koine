# Requirements

Living document. Every requirement has a stable ID, a status, and, once implemented, the tests that prove it.
`scripts/docscheck.mjs` (run by `task docs:check` and CI) rejects: unknown statuses, an `implemented` requirement with no
tests, a named test that does not exist, a missing design reference, and duplicate IDs.

**Status values:** `planned` (agreed, not built) · `implemented` (built, tests listed) · `deprecated` (kept for history).
**Test references:** a Go test function name, or `js:<text>` matching the start of a JS test title. Separate with `;`.
**Changing behaviour?** Update the requirement first, or add a new one. See `docs/ENGINEERING.md` §1.

## Extension and plugin compatibility

### REQ-DISC-001 — Discover skills, agents and commands across ecosystems

- Status: implemented
- Statement: Koine finds skills, sub-agents and prompt/commands in Koine, Pi, opencode, Claude and `.agents` locations, so existing assets work unchanged.
- Design: DES-001
- Tests: TestDiscoverAcrossEcosystems; TestDiscover; TestEverythingWorksTogether; TestAgentsSkillsWalkUpToHome

### REQ-EXT-001 — Language-agnostic extension protocol

- Status: implemented
- Statement: An extension in any language that speaks newline-delimited JSON-RPC 2.0 can register tools and commands and receive events. A crashing or broken extension is reported, never fatal.
- Design: DES-002; DES-003
- Tests: TestProtocolRoundTrip; TestExtensionCrashSurfacesAsError; TestBrokenExtensionIsReportedNotFatal; TestContextCancelAbortsCall

### REQ-EXT-002 — Run Pi extensions

- Status: implemented
- Statement: Pi TypeScript/JavaScript extensions load through the JS sidecar and receive Pi events and the `pi.*` API.
- Design: DES-004
- Tests: js:runner: Pi extension; js:conformance: verified events reach Pi handlers

### REQ-EXT-003 — Run opencode plugins

- Status: implemented
- Statement: opencode plugins load through the same sidecar, including tool definitions and `tool.execute.before/after` hooks.
- Design: DES-004
- Tests: js:runner: opencode plugin

### REQ-EXT-004 — Degrade gracefully on unsupported APIs

- Status: implemented
- Statement: Pi APIs Koine does not implement resolve to harmless stand-ins and are listed by `/compat`; an extension using a future API never crashes the host.
- Design: DES-004; DES-006
- Tests: js:conformance: unknown future API never crashes; TestReloadModelAndCompat

### REQ-HOOK-001 — Claude-compatible command hooks

- Status: implemented
- Statement: Hooks declared in Claude-format settings run as commands; exit code 2 blocks the action with stderr as the reason; JSON on stdout can rewrite arguments.
- Design: DES-002
- Tests: TestExit2Blocks; TestJSONDecisions; TestNestedClaudeFormAndStdin; TestClaudeSettingsOnlyContributeHooks

## Safety

### REQ-TRUST-001 — Project code is off until the folder is trusted

- Status: implemented
- Statement: Extensions, hooks and providers that come from the project directory do not run until the folder is trusted (`/trust`, `-trust`, or `KOINE_TRUST=1`). Harmless scalars still apply. The user is told what was skipped.
- Design: DES-005
- Tests: TestUntrustedProjectCodeDoesNotRun; TestDiscoverExtensionsUntrustedSkipsProjectCode; TestScopedConfigAndCodeCount; TestStoreTrustAndAncestors

### REQ-TRUST-002 — Trust persists and takes effect immediately

- Status: implemented
- Statement: `/trust` records the folder in a user-private file and reloads without restarting.
- Design: DES-005
- Tests: TestTrustCommandPersistsAndReloads; TestFromEnv

### REQ-SKILL-001 — Refuse skills hiding instructions in invisible text

- Status: implemented
- Statement: Skills containing hidden Unicode control or tag characters are not loaded, and the user sees why.
- Design: DES-005
- Tests: TestSkillWithHiddenTextIsRefused; TestAudit; TestDiscoverReportSkipsHiddenText

## Models

### REQ-LLM-001 — Route models to providers without guessing

- Status: implemented
- Statement: A model id resolves to a provider by known name or explicit `prefix/model`; an unrecognised id is an error, never a silent send to the wrong vendor.
- Design: DES-007
- Tests: TestRouterResolve; TestRouterRejectsInsteadOfGuessing; TestOpenAIRoundTrip

### REQ-LLM-002 — Retry transient provider failures only

- Status: implemented
- Statement: Network errors, 429 and 5xx are retried up to four attempts; other 4xx are not.
- Design: DES-007
- Tests: TestRetriesOn429ThenSucceeds; TestClientErrorIsNotRetried; TestOpenAIErrors

### REQ-LLM-003 — Streaming responses

- Status: planned
- Statement: Provider responses stream token by token to every frontend.
- Design: none yet

## Agent and frontends

### REQ-AGENT-001 — Tool-using agent loop with sub-agents

- Status: implemented
- Statement: The agent alternates model turns and tool calls until done, honours a turn limit and cancellation, and can delegate to named sub-agents through a `task` tool.
- Design: DES-001
- Tests: TestLoopRunsToolsAndStops; TestTaskToolSubAgents; TestMaxTurnsProviderErrorAndCancel

### REQ-FE-001 — One core, many frontends over HTTP/SSE

- Status: implemented
- Statement: The core serves prompts as server-sent events so a desktop shell or browser can drive the same agent as the terminal, one run at a time.
- Design: DES-008
- Tests: TestPromptStreamsSSE; TestConcurrentRunRejected; TestServeMode; TestPromptRejectsBadBody

### REQ-FE-002 — Bubble Tea TUI and Wails desktop app build and are tested in CI

- Status: planned
- Statement: The two frontends compile and have tests; today they are written but unbuilt.
- Design: DES-008

### REQ-SESS-001 — Persist and resume sessions

- Status: planned
- Statement: Conversations are saved to disk and can be resumed.
- Design: none yet

### REQ-SEC-001 — Sandboxed shell execution

- Status: planned
- Statement: The bash tool can run inside real isolation. Path allow-lists are not accepted as a substitute.
- Design: none yet

## Parity with upstream Pi (from the gap assessment, see `docs/gap-assessment.md`)

### REQ-CLI-001 — Scriptable command line
- Status: planned
- Statement: `koine [options] [@files] [message]` runs one prompt when stdin or stdout is not a terminal, prepends piped stdin, writes only the final answer to stdout and diagnostics to stderr, supports `--mode json`, and selects model, tools and resources by flags. Gap rows CLI-01..06, 12, 29..31, 48.
- Design: none yet

### REQ-LLM-004 — Model catalog, usage and cost, thinking, prompt caching
- Status: planned
- Statement: Each model has known context window, limits and price; usage and cost are tracked per session; thinking levels map to each provider with block replay; prompt caching is on for Anthropic. Gap rows LLM-11, 22, 23, 25, 28.
- Design: none yet

### REQ-SESS-002 — Pi-compatible session files
- Status: planned
- Statement: Sessions persist as JSONL in Pi's v3 tree format so they can be continued, resumed by id, forked and read by Pi. Extends REQ-SESS-001. Gap rows CLI-15..21, SESS-01.
- Design: none yet

### REQ-CMP-001 — Compaction and overflow recovery
- Status: planned
- Statement: Context is compacted automatically and on demand with a structured summary, never separating a tool result from its call; a provider overflow triggers one compaction and retry. Gap rows CMP-01..05, LLM-31.
- Design: none yet

### REQ-MCP-001 — MCP client
- Status: planned
- Statement: Koine connects to MCP servers over stdio and streamable HTTP from `mcp.json` (also reading `.mcp.json` and opencode config), exposes their tools as `mcp__server__tool`, and treats project-scoped servers as untrusted code. Gap rows LLM-42..47.
- Design: none yet

### REQ-PKG-001 — Install Pi packages
- Status: planned
- Statement: `koine install|remove|update|list` handles npm, git and local Pi packages with a `pi` manifest, feeds their extensions, skills and prompts to the loaders, pins by hash and never runs install scripts. Gap rows EXT-34..40.
- Design: none yet

### REQ-EXT-005 — Extensions can use the user interface
- Status: planned
- Statement: Extension dialogs (`select`, `confirm`, `input`) and text status surfaces reach the active frontend; `ctx.mode` and `ctx.hasUI` are truthful; shim exports match upstream so extensions never fail to link. Gap rows EXT-07, 24..26, 28.
- Design: none yet

### REQ-TOOL-001 — Built-in tool parity
- Status: planned
- Statement: `read`, `edit`, `bash`, `grep`, `find`, `ls` match Pi's behaviour: tail-truncated bash output with a saved full log, multi-edit with fuzzy matching and line-ending preservation, `.gitignore`-aware search, `find` by that name. Gap rows TOOL-01..22.
- Design: none yet

## Tracking upstream Pi

### REQ-PI-001 — Pinned baseline and an honest manifest

- Status: implemented
- Statement: Koine is pinned to one upstream Pi release. Every Pi surface item has a status; `verified` requires a named, existing test; the baseline in the binary equals the manifest's.
- Design: DES-006
- Tests: TestManifestIsValid; TestValidateRules; TestVerifiedItemsNameRealTests; TestPiBaselineMatchesManifest; js:committed surface.json equals

### REQ-PI-002 — Detect upstream drift

- Status: implemented
- Statement: A script diffs Pi's published extension types against the manifest, reports additions and removals, fails loudly when it cannot extract, and can rebase the manifest without losing history.
- Design: DES-006
- Tests: js:extract + diff against the real manifest; js:rebase adds unsupported items; js:CLI: fixture package drifts; js:CLI: extraction failure exits 3

### REQ-PI-003 — Report compatibility to users

- Status: implemented
- Statement: `koine -compat` and `/compat` tell users what is verified, inert or unsupported, and which APIs their extensions actually hit.
- Design: DES-006
- Tests: TestCompatFlagPrintsReport; TestReport; TestVersionAndUntrustedByDefault

## Engineering process

### REQ-QA-001 — Quality gates are enforced by automation

- Status: implemented
- Statement: Format, lint, line and file length, coverage, dependency audit, vulnerability scan and security scan run in CI for Go and JS, and a single check blocks merging.
- Design: DES-009
- Tests: js:flags long lines; js:flags long files; js:main exits 1 on a violation

### REQ-DOC-001 — Requirements, design and tests stay linked

- Status: implemented
- Statement: Requirement IDs, design IDs, ADR status and doc links are machine-checked; a change to product code without a doc change fails review unless it says `Docs-Impact: none`.
- Design: DES-009
- Tests: js:docscheck validates; js:docscheck rejects; js:docscheck living-docs rule

### REQ-AGENTDEV-001 — The repository is ready for coding agents

- Status: implemented
- Statement: One `AGENTS.md`, shared skills, sub-agents and commands work in Claude Code, opencode, Pi and Koine, and Koine itself can load them.
- Design: DES-009
- Tests: TestRepoAgentConfigLoads
