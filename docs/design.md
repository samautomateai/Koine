# Design

Living document. Each design section has an ID, the requirements it satisfies, and the code that implements it.
`scripts/docscheck.mjs` verifies that the requirement IDs exist and the code paths exist. Decisions with trade-offs get an ADR in
`docs/adr/`. The wire protocol and user-visible behaviour are specified in `docs/SPEC.md`.

## DES-001 — Layered core, standard library only

- Satisfies: REQ-DISC-001; REQ-AGENT-001
- Code: core/app; core/agent; core/skills; core/agents; core/prompts
- Notes: `core/app` wires config, discovery, extensions, hooks, provider and agent. Frontends call `app.HandleInput` and render events. The Go core has no third-party dependencies, which keeps the supply chain small and the audit trivial (ADR 0004).

## DES-002 — Events and decisions

- Satisfies: REQ-EXT-001; REQ-HOOK-001
- Code: core/events; core/hooks
- Notes: Every interception point emits an event. Handlers return a `Decision` (block, rewrite arguments, replace output, append to the system prompt). `Multi` merges decisions from hooks and extensions; the first block wins.

## DES-003 — Extension host and wire protocol

- Satisfies: REQ-EXT-001
- Code: core/ext/host.go
- Notes: One child process per extension, speaking JSON-RPC 2.0 over stdio. Host-to-extension calls need a reply; extension-to-host registrations are notifications. Errors and crashes are contained to the extension.

## DES-004 — JS sidecar for Pi and opencode

- Satisfies: REQ-EXT-002; REQ-EXT-003; REQ-EXT-004
- Code: core/ext/sidecar.go; core/ext/sidecar/runner.mjs; core/ext/sidecar/shims
- Notes: Pi and opencode plugins are JavaScript, so a Node or Bun process hosts them. Imports of `@earendil-works/*`, `@opencode-ai/*`, zod and typebox resolve to shims. Unsupported properties and methods return stand-ins and are reported (ADR 0001).

## DES-005 — Trust boundary

- Satisfies: REQ-TRUST-001; REQ-TRUST-002; REQ-SKILL-001
- Code: core/trust; core/app/discover.go; core/app/config.go; core/skills
- Notes: Config is loaded in two scopes, user and project. Only user scope may declare code or credential routing unless the folder is trusted. Trust is recorded per directory, inherited by descendants, and stored with owner-only permissions (ADR 0002).

## DES-006 — Compatibility manifest and drift pipeline

- Satisfies: REQ-PI-001; REQ-PI-002; REQ-PI-003; REQ-EXT-004
- Code: compat/compat.go; compat/pi.json; scripts/upstream-check.mjs; core/ext/jstest/conformance.test.mjs
- Notes: The manifest is the single source of truth for what Pi surface exists and how far Koine goes. The sidecar's behaviour for unsupported properties is generated from it. A conformance test executes the manifest against the real runner (ADR 0003).

## DES-007 — Provider router

- Satisfies: REQ-LLM-001; REQ-LLM-002
- Code: core/llm
- Notes: A `Provider` interface with Anthropic and OpenAI-compatible implementations. `Router.Resolve` maps a model id to a provider from built-in names, `prefix/model`, and user-declared providers. Shared HTTP helper owns retry policy (DES-016).

## DES-008 — Frontend contract

- Satisfies: REQ-FE-001; REQ-FE-002
- Code: core/server; core/tui; cmd/koine
- Notes: Frontends receive a stream of events (`text`, `tool_use`, `tool_result`, `notice`, `error`, `done`). The HTTP server allows one run at a time. The line-mode TUI is dependency-free; Bubble Tea and Wails are separate modules.

## DES-009 — Quality, documentation and agent workflow

- Satisfies: REQ-QA-001; REQ-DOC-001; REQ-AGENTDEV-001
- Code: scripts/sizecheck.mjs; scripts/docscheck.mjs; scripts/covergate.sh; .github/workflows/ci.yml; Taskfile.yml; AGENTS.md
- Notes: `task check` is the local mirror of CI. Rules live in `docs/ENGINEERING.md`; agents read `AGENTS.md`, which points to them. Docs are checked, not trusted (ADR 0005).

## DES-010 — Command line: parse, build one prompt, run print mode

- Satisfies: REQ-CLI-001
- Code: cmd/koine/args.go; cmd/koine/print.go; cmd/koine/main.go; core/app/runopts.go
- Notes: `parseArgs` is a small hand-written parser (the standard `flag` package stops at the first positional and has no `--name=value` plus `@file` handling). `run` takes injected `streams` so the whole command line is testable without touching the process's stdio. `buildPrompt` reads stdin only when it is not a terminal. Per-run overrides travel in `app.RunOptions` inside `app.Options`, and are re-applied by `load`, so `/reload` does not drop them. Text mode keeps only the text after the last tool call as the answer, which makes `koine ... | tool` safe to pipe; JSON mode forwards `agent.Event` unchanged.

## DES-011 — Sessions: Pi v3 files, written as the conversation happens

- Satisfies: REQ-SESS-001
- Code: core/session; core/app/session.go; core/agent/agent.go
- Notes: `core/session` reads and writes Pi's tree format: a header line, then entries linked by `parentId`; the active branch is found from the last entry. `agent.Agent.Persist` is called for every message added to the history, so a crash loses at most the call in flight. A user message of tool results is stored as one `toolResult` entry per result (as Pi does) and merged back on load. The file is created on the first entry, so runs that say nothing leave nothing behind. Thinking blocks are stored in Pi's shape and restored (DES-016); unknown entry types and blocks (images, custom, labels) stay in the file but are not sent to the model. Files go to `~/.koine/sessions`, not Pi's directory, so the two tools never write the same file by accident; `--session PATH` opens a Pi file directly. Sub-agents get no `Persist`, so their messages stay out of the parent's file.

## DES-012 — Compaction: estimate, cut at a safe message, summarise, record

- Satisfies: REQ-CMP-001
- Code: core/compact; core/agent/compact.go; core/agent/agent.go; core/session/messages.go; core/app/session.go
- Notes: Koine does not yet get token counts from providers (LLM-11), so `core/compact` estimates about four characters per token. `CutPoint` walks back from the newest message until it has kept enough tokens, and only cuts where the first kept message is not a tool-result message; the summary is a user message, so a kept assistant message may follow it. `Agent.Run` checks the budget before every turn and, on a provider overflow error, compacts once and retries once per prompt. `Agent.OnCompact` receives the old history index of the first kept message; the app maps it to a `compaction` entry whose `firstKeptEntryId` comes from `Session.messagesWithIDs`, and refuses to write it if the file and the live history have different lengths. Sub-agents run with no `ContextWindow`, so they never auto-compact.

## DES-013 — Settings: Pi's schema, one manifest, a mapper at the edge

- Satisfies: REQ-CFG-001
- Code: compat/settings.json; compat/settings.go; core/app/config.go; core/app/runopts.go; core/app/session.go; scripts/settings-check.mjs
- Notes: The settings files use Pi's key names so Pi users and Pi docs carry over; only the file location (`.koine`) and a few Koine-only keys differ. `compat/settings.json` is the single place that says, key by key, what Koine does with a Pi setting; `compat.SettingsManifest.Check` is the validator and `Config.normalize` is the mapper from Pi spellings to Koine's fields (`defaultProvider`+`defaultModel` → `model`; string entries of `extensions` → `plugin`, object entries → extension specs). Verified keys must name an existing test, as in `compat/pi.json`. `scripts/settings-check.mjs` reads the markdown tables of Pi's `docs/settings.md` from the published package, so upstream additions show up as drift and are recorded by `task upstream:rebase` as `todo`. Untrusted projects still apply only harmless scalars (model, turns, compaction); `sessionDir`, `defaultTools` and everything that can run code need trust. Precedence for the session directory: `--session-dir`, `$KOINE_SESSION_DIR`, the `sessionDir` setting.

## DES-014 — Model catalog and usage accounting

- Satisfies: REQ-LLM-004
- Code: core/llm/models.json; core/llm/usage.go; core/llm/llm.go; core/llm/openai.go; core/agent/usage.go; core/agent/compact.go; core/session/messages.go; core/app/app.go; core/app/session.go
- Notes: `models.json` is embedded and carries a `source` and `checked` date: its prices come from third-party price pages and have not been verified against the vendor, so users can correct them in `~/.koine/model-catalog.json` (a different file from Pi's `models.json`, which has another schema and is not read). Entries are matched by id, with an optional `provider/` prefix and a dated suffix. Providers fill `Response.Usage` with cached tokens counted apart from input, whatever the vendor's own accounting. `Agent.record` attaches usage and cost to the assistant message, adds to the totals and emits a `usage` event; a model without a price marks the total as a lower bound (`Unpriced`) instead of adding zero. The context size is the provider's count at the last response plus an estimate of later messages, reset on compaction because the old count no longer matches the history. The session file stores usage under the assistant message's `usage` key using Pi's field names; the exact Pi shape is the best known and is not yet checked against upstream.

## DES-015 — Agent directory and prompt files

- Satisfies: REQ-CFG-002
- Code: core/agentdir/agentdir.go; core/app/sysprompt.go; core/app/runopts.go; core/app/config.go; core/app/discover.go; core/app/session.go; core/skills/skills.go; core/agents/agents.go; core/prompts/prompts.go; core/trust/trust.go; core/agent/agent.go
- Notes: `agentdir.Of(home)` is the one place that knows where the user directory is, so every reader of `~/.koine` goes through it and the override cannot be missed by one of them. The name is `KOINE_AGENT_DIR` rather than Pi's variable, as the settings mapper does for key spellings. Pi's own directory (`~/.pi/agent`) is still read as a fallback for the resources Koine already shared with it. `promptFile` searches a trusted project's `.koine/` then `.pi/`, then the user directory, then Pi's, and returns the first non-blank file; it never joins files. It runs before the command-line options so the options win.

## DES-016 — Thinking, prompt caching and retry

- Satisfies: REQ-LLM-005; REQ-LLM-002
- Code: core/llm/thinking.go; core/llm/anthropic.go; core/llm/openai.go; core/llm/retry.go; core/llm/overflow.go; core/llm/models.json; core/llm/usage.go; core/agent/thinking.go; core/agent/agent.go; core/session/messages.go; core/app/llmopts.go; core/app/app.go; core/app/config.go; core/compact/compact.go; cmd/koine/args.go
- Notes: `llm.ThinkingSettings` turns the explicit level (flag, suffix, `/thinking`), the settings and the catalog into one `llm.Thinking` per request: mode (`adaptive`, `budget` or `effort`), level and budget; the agent builds it for each call, so a `/model` switch picks up the new model's level. `nil` means send nothing, which is the case for models the catalog says do not think and for unknown models nobody asked about; this keeps `defaultThinkingLevel: medium` from sending `reasoning_effort` to a local model that rejects it. The catalog's `reasoning` is `true` or `{mode, max, off}`: `max` is the highest level the model accepts, `off` says what `thinking.type` to send for `off` (Claude Opus 5.5 cannot disable thinking, so it sends nothing). The three Claude entries use Anthropic's adaptive thinking with `output_config.effort`, as its effort page describes; `minimal` is sent as `low`. Budget mode (for catalog entries and unknown `claude-*` models) sends `budget_tokens` and raises `max_tokens` so the budget fits. With thinking on and no limit set, the agent asks for the model's output limit up to 32000, because thinking tokens count against it. A thinking block is an `llm.Block` with `Signature`; a redacted block keeps its payload in `Signature`, as Pi does in `thinkingSignature`. Blocks without a signature (from OpenAI-compatible `reasoning_content`) are shown and saved but never sent back. The Anthropic provider drops all thinking blocks when it has sent `thinking: disabled`. Prompt caching puts `cache_control: ephemeral` on the system prompt (sent as a block), the last tool and the last block of the last user message, three of the four breakpoints the API allows. `retry.go` owns the retry loop: `RetryPolicy` (attempts, exponential backoff with a cap, `Retry-After` up to a limit, a per-attempt timeout) is set on the `Router` from the settings and handed to each provider; non-success answers are `*HTTPError`, so `IsQuotaError` and `IsOverflow` can read the provider and body; a context hook reports each retry to the agent, which emits a notice. `retry.provider.maxRetries` has no counterpart because there is one loop. There is no jitter. Unverified: the wire format is tested against fake servers only, and Pi's built-in thinking budgets and overflow patterns were not compared with ours.

## DES-020 — Session tree: leaf moves, labels, copies and exports

- Satisfies: REQ-SESS-002
- Code: core/session/tree.go; core/session/migrate.go; core/session/export.go; core/session/session.go; core/session/messages.go; core/app/tree.go; core/app/session.go; core/app/config.go; core/tui/tui.go; cmd/koine/main.go
- Notes: A session is a tree of entries and a leaf; moving the leaf (`/tree`) writes nothing, as in Pi, and the next appended entry hangs off the new leaf, so a reopened file starts at its last entry again. Moving to a user message sets the leaf to its parent and hands the text back (there is no editor in line mode, so it is printed). A branch summary is a `branch_summary` entry whose parent is the new position and whose `fromId` is the old leaf; it reaches the model as a user message (`entryMessage`, the only change in `messages.go`). The summariser is `compact.Summarize` over the entries between the old leaf and the common ancestor, newest first within `window - branchSummary.reserveTokens`. Line mode cannot ask a question inside `HandleInput`, so the summary question is `App.Choose`, set by frontends that can ask (the line TUI sets it); without it `--summary` decides, and `skipPrompt` removes the hint. Every `Entry` keeps the line it was read from, so `/clone`, `/fork` and `Fork` copy lines unchanged (ids included, as Pi does); only an entry whose parent was a skipped `session_info` is re-encoded. Appends never touch earlier bytes, and add the newline a hand-edited file may lack. Files older than v3 are migrated in `Open` (ids and parents added, `firstKeptEntryIndex` turned into `firstKeptEntryId`, `hookMessage` renamed `custom`) and replaced atomically after the original is saved as `<file>.v<N>.bak`; `List` migrates in memory only. HTML export is one file with inline style and no scripts; all session text is escaped and only png, jpeg, gif and webp base64 images are embedded.
