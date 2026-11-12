# Intentional divergences from Pi

| Area             | Pi                      | Koine                                                                                                              | Why                                             |
| ---------------- | ----------------------- | ------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------- |
| Project code     | Runs after trust prompt | Off until `/trust`, `-trust` or `KOINE_TRUST=1`; harmless scalars still apply                                      | Safe default for a multi-ecosystem loader       |
| Unsupported APIs | Always present          | Stand-ins that never throw; reported by `/compat`; some props are `undefined` as upstream would be without a model | An extension should degrade, not crash the host |
| Skills           | Loaded as found         | Skills containing hidden Unicode control characters are refused                                                    | Prompt-injection defence                        |
| Providers        | Built-in registry       | Anthropic, OpenAI-compatible, and prefix-routed custom providers; unknown prefixes rejected                        | Avoid sending a prompt to the wrong vendor      |
| Config dir       | `.pi`                   | `.koine`, with `.pi`, `.opencode`, `.claude`, `.agents` also discovered                                            | Multi-ecosystem                                 |

## Directories and environment

- Pi's agent directory `~/.pi/agent` is Koine's `~/.koine`; `PI_CODING_AGENT_DIR` is `KOINE_AGENT_DIR` and `PI_CODING_AGENT_SESSION_DIR` is `KOINE_SESSION_DIR`. Koine also reads `~/.pi/agent` and a project's `.pi/` for skills, prompts, extensions, agents, `SYSTEM.md` and `APPEND_SYSTEM.md`, so a Pi setup keeps working.
- `sessionDir` from a project that is not trusted is not applied (Pi reads it before trust).

## Sessions

- `/tree` summarises the branch it leaves only when asked (`--summary`) or when a frontend answers its question (`App.Choose`); in line mode without a flag it prints a hint instead of showing Pi's three-way prompt. `branchSummary.skipPrompt` removes the question and the hint.
- Moving to a user message prints its text instead of putting it in an editor, because line mode has none. `doubleEscapeAction` is read and validated but nothing in line mode presses Escape.
- `/label`, `/fork <id>`, `/export` and the numbered `-r` picker are text commands standing in for Pi's selectors. `/label` is not documented by Pi (its tree selector sets labels), so it is Koine's own.
- Opening a v1 or v2 file rewrites it as v3 after saving the original as `<file>.v<N>.bak`; Pi's backup behaviour was not checked. The branch summary prompt is the compaction one with extra instructions, not Pi's branch prompt.

## Models and usage

- Pi reads model metadata from its generated registry and a user `models.json`. Koine ships a small embedded catalog (`core/llm/models.json`), reads `models.json` in Pi's schema for providers and models (declared models join the catalog) and keeps `~/.koine/model-catalog.json` for prices and windows, which it applies last. The embedded catalog's context windows and prices for the Claude models are copied from Pi's own catalog (https://pi.dev/models, checked 2026-11-12) and the file records its source and check date. Pi's page lists flat prices, so the Haiku long-context multiplier an earlier version carried (from third-party pages) was removed. Koine lists only the Claude models of Pi's `anthropic` provider; other vendors come from `models.json`.
- Model catalog refresh is opt-in: `koine update --models` (Pi: `pi update --models`) merges a server's catalog into `~/.koine/model-catalog.json` after validation, and never runs at startup. Pi overlays the pi.dev catalog and caches it; Koine has no default address (the setting `modelCatalogUrl` or `KOINE_MODEL_CATALOG_URL` or `--url` names one), expects Koine's own `{"models":[...]}` shape, and merges into the file the user may also edit, keeping a `.bak`. `KOINE_OFFLINE=1` is the counterpart of `PI_OFFLINE` and only stops this command.
- The `usage` object written on assistant messages follows Pi's field names (`input`, `output`, `cacheRead`, `cacheWrite`, `totalTokens`, `cost`) as best known; it has not been compared with Pi's source. Reasoning tokens are not tracked yet.

## Thinking, caching and retry

- Pi's built-in `thinkingBudgets` and its provider-specific level maps (`thinkingLevelMap`, OpenRouter and zai formats) are not documented well enough to copy, so Koine uses its own budgets (1024, 2048, 8192, 16384) and one mapping per API; the Claude catalog entries use Anthropic's adaptive thinking and effort as Anthropic's effort page describes it. These have been tested against fake servers only.
- Koine has one retry loop. Pi's separate provider-level retries (`retry.provider.maxRetries`) are accepted and ignored. There is no jitter. `httpIdleTimeoutMs` (default 300000, as in Pi) bounds the silence in a streamed response; a response that is not streamed (a server that answers with plain JSON) has no default limit, so it is not cut off unless the user sets one. Retries happen only before the first byte of a stream: a stream that fails midway is an error, not a retry.
- A model the catalog does not know is sent thinking parameters only when the user chose a level for it (`--thinking`, a suffix, `/thinking` or `modelThinkingLevels`); Pi's default level applies to models it knows think. Anthropic cache breakpoints use the default 5-minute lifetime; Pi's long retention and OpenAI `prompt_cache_key` are not implemented.

## Settings

- `sessionShareDestination`, `collapseChangelog`, `enableInstallTelemetry`, `enableAnalytics`, `selfUpdateCommand` are `by-design` in `compat/settings.json`: Koine has no gist/Radius sharing, update check, telemetry or self-update. They are reported as ignored if set.
- Files live under `.koine/`, not `.pi/`. Koine-only keys (`model`, `maxTurns`, `hooks`, `plugin`, `providers`, `compaction.contextWindow`) are listed in the `koine` section of `compat/settings.json`.

## Command line

- `--mode json` writes every event of Pi's documented schema with Pi's field names (including `queue_update`, `entry_appended`, `session_info_changed`, `thinking_level_changed`, `compaction_*`, `auto_retry_*`, `summarization_retry_*` and `tool_execution_update`), but Koine does not stream from providers: each content block is one `text_delta` or one tool-call triple, and `message_update.usage` is the final usage. Pi's JSON page names fields without giving the inner shape of some of them; for these Koine follows the session-file format and does not claim parity: `entry` (the session line as Koine writes it), `compaction_end.result` (`summary`, `firstKeptEntryId`, `tokensBefore`, `estimatedTokensAfter`, no `details`; the token count after is Koine's estimate), `partialResult` and the message objects. `api` is derived from the provider (`anthropic-messages` for Anthropic, `openai-completions` for the rest). `agent_end.willRetry` is always false: retries and overflow recovery happen inside one run. `entry_appended` needs a session (none with `--no-session`). Queues have one source, `pi.sendUserMessage(text, {deliverAs})` (default follow-up); `queue_update` is reported when the agent loop next looks at the queues, and a message queued while idle waits for the next run. None of these shapes were compared with Pi's source.
- `-e` runs code, so it obeys the trust gate (ADR 0002): in an untrusted folder it is skipped with a warning, and an option such as `--plan` that only that extension defines is then an unknown option (the error says why). `--skill` and `--prompt-template` obey the same gate (a skill or template steers the model with text from the path you typed, and the folder may be hostile), so they too are skipped with a warning until the folder is trusted; the hidden-text audit still applies once loaded. `builtin:*` extensions (mcp, codemode, tool-search, llama.cpp) do not exist; `-e builtin:x` only warns.
- `--tui-mode`, `--no-mcp`, `--offline`, `--verbose`, `--export`, `--session-id` and `-na` are not options here (other rows).
- `--model` fuzzy matching only knows the model catalog (`core/llm/models.json` plus the user's `model-catalog.json`); an id it does not list is sent as written if its provider is known. Ties between equal matches of one provider take the undated, then the latest id; Pi's own tie-break was not checked. `--models` and `enabledModels` choose the startup model; there is no model cycling.
- Extension options are read after the extensions load, so a typo in an option name is reported only then (exit 2) and the extensions have started by that time.

## Terminal UI

- Koine's default `tuiMode` is `regular` (scrollback); Pi's is `fullscreen`. Fullscreen is opt-in so existing terminals keep their scrollback.
  Fullscreen mode does not implement `fullscreenCopyOnSelect` (mouse reporting is on, so select with Shift held), the prompt-jump keys
  (`tui.altScreen.previousPrompt`/`nextPrompt`) or transcript search (`tui.altScreen.search*`); they stay accepted in `keybindings.json`.
  `fullscreenScrollbar: "auto"` shows the bar whenever the transcript is longer than the view (Pi's timing is not documented). `terminal.clearOnShrink`
  stays `inert`. In regular mode a resize or Ctrl+O reprints the whole transcript.
- Inline images are displayed, not pasted: tool-result images and the history of a resumed session. Pasting (Ctrl+V, `app.clipboard.pasteImage`) and
  dragging images into the editor are not done. Detection for `terminal.images: "auto"` is Koine's own list (Pi's is not documented): kitty and
  Ghostty use the kitty protocol, iTerm2 and WezTerm the iTerm2 one, tmux and screen get text. Cells are assumed to be 8 by 16 pixels, so the row
  count of a picture is a guess. Sixel is not supported. `markdown.mermaid` is not drawn.
- Not verified on a real terminal: only the escape sequences are tested, against fake terminals.
- Pi's theme docs do not list the colour tokens; the 51 tokens (and 5 optional ones) are Koine's reading of them. okhsl colours are rejected.
  User themes are reloaded by `/reload`, not watched as Pi does.
- Key defaults come from Pi's keybindings page. Not verified against Pi: the double Ctrl+C exit rule, the number of lines Pi shows for collapsed
  tool output, how Pi ranks `@` file matches, and how `terminal.hyperlinks: "auto"` detects terminals (Koine uses its own list).
- Rendering is covered only by unit tests of pure functions (plus one pseudo-terminal test of the raw-mode shim). No test drives a real terminal
  emulator, so layout in a particular terminal was not seen.
- The `@` file index skips `.git`, `node_modules` and names listed plainly in `.gitignore`; it does not implement gitignore patterns.

## Environment

The page https://pi.dev/docs/latest/environment-variables was read on 2026-11-11. Every `PI_*` variable on it, and what Koine does:

- Process markers: `AI_AGENT=pi` is `AI_AGENT=koine`; `PI_CODING_AGENT=true` is `KOINE_CODING_AGENT=true`. Set by the CLI for itself and its children, not when `core/app` is embedded. The `PI_` names are not set.
- Shell tool session variables, given to the model's bash tool only: `PI_SESSION_ID` is `KOINE_SESSION_ID`; `PI_SESSION_FILE` is `KOINE_SESSION_FILE` (unset without a session file); `PI_PROVIDER` is `KOINE_SESSION_PROVIDER`; `PI_MODEL` is `KOINE_SESSION_MODEL` (not `KOINE_MODEL`, which already means "start with this model"); `PI_REASONING_LEVEL` is `KOINE_SESSION_REASONING_LEVEL`, set while the model is asked to think.
- Directories: `PI_CODING_AGENT_DIR` is `KOINE_AGENT_DIR`; `PI_CODING_AGENT_SESSION_DIR` is `KOINE_SESSION_DIR`.
- Not applicable, no `KOINE_` counterpart because the feature does not exist: `PI_PACKAGE_DIR` (no packages, a single binary), `PI_SKIP_VERSION_CHECK` (Koine does no version check), `PI_SELF_UPDATE_COMMAND` (no self-update), `PI_TELEMETRY` (no telemetry), `PI_CODEMODE_MAX_WORKERS` (no codemode), `PI_SHARE_VIEWER_URL` (no `/share`), `PI_RADIUS_GATEWAY` (no `/bug`).
- `PI_CACHE_RETENTION` is read as `KOINE_CACHE_RETENTION` (`long`; REQ-LLM-013). Reserved for later, not read yet: the terminal variables `PI_CLEAR_ON_SHRINK`, `PI_HARDWARE_CURSOR`, `PI_CLIPBOARD_OSC52`, `PI_HYPERLINKS`, `PI_PROGRAM_STATUS`, `PI_IMAGE_PROTOCOL`, `PI_TRUE_COLOR`, `PI_TUI_ESC_TIMEOUT` as the same names with `KOINE_` (the line-mode TUI has no use for them).
- Not `PI_` variables: `HTTP_PROXY` and `HTTPS_PROXY` are honoured by Go's default HTTP transport (not tested here); `VISUAL` and `EDITOR` start the external editor (Ctrl+G) when `externalEditor` is not set.

## Images

- Pi documents `images.autoResize` as "at most 2000 by 2000 pixels" and names `images.resize.maxBytes`, `images.maxPerMessage` and `inputLimits.images` without defaults on the pages Koine was built from. Koine applies the 2000 by 2000 limit and its own 4.5 MB cap on the encoded image (under Anthropic's 5 MB); it has no per-message or per-request image counts. Pi's resampling filter and re-encoding settings were not checked: Koine uses a box filter and JPEG quality 85.
- Resizing uses only the Go standard library (ADR 0004), which decodes PNG, JPEG and GIF. WebP and BMP images are sent unchanged with a note, and refused only when over the byte cap. A GIF that needs resizing becomes a PNG of its first frame; a small GIF is sent as it is.
- A catalog model without an `input` list allows images (Pi's default for `input` is `["text"]`); the built-in Claude models list `["text","image"]`. Unknown models are allowed so that a local vision model works without a catalog entry.
- The `read` tool answers an image with the text `Read image file [type]` (and a bracketed note when resized); Pi's exact wording was not checked. OpenAI-compatible providers cannot take images in tool messages, so Koine sends them as a following user message headed `Attached image(s) from tool result:`.
- The HTTP server (`/prompt`), `custom_message` entries with images and the extension `input` event's `images` (EXT-15) take or keep text only.
## Built-in tools

- `bash` has a default timeout of 120 seconds (Pi has none); the model's `timeout` argument or the user's interrupt still override it. The user's `!cmd` has no timeout.
- `edit` fuzzy matching does not apply Unicode NFKC, and `read`/`edit` path variants do not try NFD, because the Go standard library has no normaliser (ADR 0004). Smart quotes, dashes, unusual spaces and trailing whitespace are folded as in Pi.
- `read` refuses a file with a NUL byte in its first 8000 bytes as binary; Pi's behaviour for binary files was not found in its docs. Images are not read at all yet (TOOL-03).
- `find` walks the tree with Koine's own `.gitignore` matcher instead of calling `fd`, and `grep` falls back to Go's RE2 regular expressions when `rg` is missing, so exotic patterns can differ between machines. Ignore files are honoured even outside a git repository.
- The `edit` result carries the unified diff and the first changed line in its text instead of Pi's separate `details` object, because Koine's tools return text. The truncation hints, error messages and prompt snippets follow Pi's source as remembered; Pi's published docs only state the 2000-line and 50KB limits and the `promptSnippet`/`promptGuidelines` rules, so the exact wording is unverified.
- `grep`, `find` and `ls` are registered but off by default, as in Pi; Koine hides them instead of removing them, so sub-agents (which Pi does not have) can still be given them.
- `!cmd` is disabled in print, JSON and server runs, where text from a pipe or the network must not become a shell command. `!!cmd` is not saved in the session; Pi keeps it for display.

## Agent loop

- Pi's agent-loop source and `prepareNextTurn` are not documented on pi.dev, and the documented `afterToolCall` overrides are `details` and `terminate` only. Koine's `tool_result` decision also carries `isError`, because Pi's extension docs say `tool_result` handlers may return it. Where Pi is silent, Koine's choice is stated in `docs/SPEC.md` and marked unverified.
- Koine does not preflight later calls while earlier ones already run: all calls of a message are checked in order, then the allowed ones run together. Events and result order match Pi's description (starts in source order, ends in completion order, results in source order).
- An aborted assistant message is stored with `stopReason: "aborted"` and is never sent to a provider again; Pi's own handling of it is not documented, so this follows Pi's field name only.
- The extension `context` event carries Koine's message shape (`role`, `content` blocks with `tool_use` and `tool_result`), not Pi's. The JS sidecar does not subscribe to it, so Pi `context` handlers never run; Go and out-of-process extensions can use it.

## Extension API and RPC mode

- `ctx.ui.editor` is a single line in the terminal UI (the prefill is not shown), and a terminal dialog cannot be closed when its `timeout` ends: the dialog resolves as cancelled for the extension and a late answer is dropped. `setWidget` takes string lines only; component factories, `custom`, footers, headers, themes, `registerMessageRenderer` and entry renderers stay inert.
- A custom message sent with `pi.sendMessage` reaches the model as an ordinary user message, because Koine's history has no custom role. Sent during a run, it is a steer or follow-up user message.
- `session_before_*` gates honour only `cancel`; a custom compaction, summary or `skipConversationRestore` in the answer is ignored. `tool_execution_*` events have no `parentToolCallId`; tool calls made through `ctx.executeTool` raise no `tool_result` event and are not added to the transcript.
- `systemPromptOptions` is a read-only description; returning a changed one does nothing. `ctx.sessionManager` is a stub, `ctx.abort` and `ctx.getSystemPrompt` are inert.
- A throwing `tool_call` handler blocks the call (Pi lets the error propagate); every other handler error is reported and the extension carries on. Host-level failures (a timeout, a process that exited) stay fail-open.
- RPC mode: `set_auto_retry` and `abort_retry` answer `success: false` because Koine always retries transient provider errors. The ids of `extension_ui_request` lines are `ui-N`. Shapes Pi does not document (stats, tree, state fields) follow Koine's session format.
- `withFileMutationQueue` queues within the extension process only; it does not wait for Koine's built-in `edit` and `write`.

## MCP

- Pi's default exposure for MCP tools is `codemode` (callable only from codemode scripts). Koine has no codemode sandbox (LLM-48), so the default is `direct`, and `codemode` / `codemode-deferred` in a config are read as `deferred` (found with `tool_search`). `tool_search` ranks by keyword hits in the name and description, not BM25, and a tool it loads is offered from the next model call; under a `-t` selection a deferred tool is not matched.
- Config files live in Koine's directories: `<agent dir>/mcp.json` (`~/.koine/mcp.json`; Pi's `~/.pi/agent/mcp.json` is read too) and `.koine/mcp.json` or `.pi/mcp.json` in the project, and the token file is `<agent dir>/mcp-auth.json`. Koine also reads Claude's `.mcp.json` and opencode's `mcp` blocks; the VS Code `servers` key and Codex TOML are not read (Pi documents them as migration steps only).
- Client ID Metadata Documents (`oauth.clientRegistration: "cimd"`) are not supported: Pi publishes a client document at a pi.dev URL, and Koine has no hosted equivalent. Sign-in uses `oauth.clientId` or dynamic registration, and `clientName` defaults to `koine`.
- `--no-mcp`, `"extensions": ["-builtin:mcp"]`, `pi.registerMcpServer`, the `mcp_servers_change` event, `/mcp` inspect, exposure edits and login from inside a session (`koine mcp login` does it), the `autoEnableCodemode` and `codemode.*` settings are not implemented. Tool annotations are kept on the tool but not yet sent in `tool_call` events.
- Server stderr goes to `<agent dir>/mcp.log`, rotated to `mcp.log.1` past 5 MB, as in Pi. The flags of `koine mcp add` beyond `--url`, `--bearer-token-env-var` and `--local/-l` (which Pi documents) are Koine's own.
- Provider `compat` keys that Koine acts on are `supportsReasoningEffort`, `supportsUsageInStreaming`, `maxTokensField`, `requiresToolResultName`, `requiresNonNullAssistantContent` and `requiresToolsWithToolHistory`; the others (`thinkingFormat`, `thinkingLevelMap`, `inputLimits`, `promptCache`, `cost.tiers`, `oauth`) are read and reported as not acted on.
- Pi's docs say `models.json` commands are not cached. Koine reuses a command's output for 5 seconds because choosing a model asks every provider for its key at once; `auth.json` commands run once per process, as in Pi.
- A project `.koine/models.json` is read only in a trusted folder (ADR 0002), because it can run `!command` secrets and choose where keys are sent. Pi's trust handling of this file was not checked.
- `koine auth check --json` prints `status`, `provider`, `source`, `reason` and `credentials`: Pi documents the three states and exit codes 0, 1 and 2 only, so this shape is Koine's. `print-bearer-token --min-expiry` is accepted and not enforced.
- OpenAI stays on Chat Completions unless a provider or model sets `api: "openai-responses"`. The Responses and Azure providers do not replay reasoning items, and Gemini replays thought signatures (Pi's `thoughtSignature` and `textSignature` names) but sends unsigned thinking nowhere, and none of the new wire formats has been checked against a live API. Built-in endpoints and the key variable names are the authors' knowledge of the vendors' public addresses and of Pi's names, not a comparison with Pi's registry.
- Not implemented: OAuth logins (LLM-17 to LLM-20), AWS `credential_process`, MFA and instance credentials, the Google metadata server and workload identity (LLM-21). AWS SSO, assumed roles and web identity are implemented without the AWS SDK (ADR 0012); an SSO login is never started or refreshed by Koine, and an explicit `AWS_PROFILE` naming a role or SSO profile beats `AWS_ACCESS_KEY_ID` in the environment.
- Amazon Bedrock and Vertex AI (LLM-06, LLM-07) keep AWS's and Google's own variable names. Bedrock acts on `AWS_BEARER_TOKEN_BEDROCK`, `AWS_BEDROCK_SKIP_AUTH`, `AWS_BEDROCK_FORCE_CACHE` and the standard `AWS_*` variables, not on `AWS_BEDROCK_FORCE_HTTP1`. Neither provider was checked against a live API, and Pi's Bedrock and Vertex sources were not read line by line.
- `cacheWarming` follows Pi's three values and default. Pi documents only the $0.05 threshold, so the savings formula and the refresh timing (cache lifetime less 30 seconds) are Koine's. Koine's model-level `promptCache` (`{"short","long"}` seconds) is a Koine key and is not Pi's provider `compat.promptCache`.
- Message conversion for another model (LLM-24) was written from what each API rejects; Pi's `transform-messages.ts` was not compared rule by rule.

## Packages

- Koine never installs packages at startup. Pi installs missing configured packages when it starts; Koine warns ("not installed ... run `koine update`") and installs only on `koine install` or `koine update`. A package is third-party code, so the download and the hash check are an explicit step.
- `npm install` runs with `--ignore-scripts` unless `--allow-scripts` is given or the user-level setting `allowInstallScripts` is true (a project's settings cannot turn it on). Packages that need a build script in `postinstall` need that opt-in.
- Koine writes `packages.lock.json` (user and project scope) with a sha256 of each package's files and refuses to load a package whose files changed; Pi's lock behaviour was not compared. Local paths are not locked.
- Packages live in `~/.koine/packages` and `.koine/packages` and are declared in `.koine/settings.json`; the `packages` array of `.pi/settings.json` is not read. `npmCommand`, `allowInstallScripts` and `collapseChangelog` are read from the user settings file only.
- The changelog of an updated package is printed by `koine update`, not on the next launch.
- `koine config` is a list with `enable`/`disable <source> <resource>` and, on a terminal, a numbered toggle loop; it is not Pi's full-screen selector. `-e npm:...` one-shot packages are not implemented, and themes from packages are listed but not applied.
- Filter precedence (`!`, `+`, `-`), `autoload: false`, falling back to the conventional directories when `pi` has only `changelogPath`, and how an unpinned package updates are Koine's reading of Pi's guide; Pi's docs do not specify them.
- `SourceInfo.source` values `auto`, `local`, `cli` and `resources_discover` are Koine's; Pi documents the shape and the built-in example only. Extension objects from the `extensions` setting report scope `user`.
- `resources_discover` handlers run after `session_start` and on reload with `{cwd, reason}`; theme paths are reported, not applied.

## Frontends

- Pi's interactive mode is one program. Koine's Bubble Tea and Wails frontends are separate processes that drive `koine --mode rpc` (ADR 0013), so they offer what Pi's RPC protocol offers: extension commands, prompt templates and skills as slash commands, and no built-in `/model`, `/tree` or `/settings` selectors. Notices that Koine prints to stderr arrive as `stderr` events.
- Sessions are Pi's JSONL version 3 only; there is no SQLite store (ADR 0014).
- Echo v4 advisories under Wails v2 (X-Real-IP in the proxy middleware, X-Forwarded-For in the rate limiter) have no patched v4 release; see the Security entry in `CHANGELOG.md`. Koine's own code uses neither middleware.
