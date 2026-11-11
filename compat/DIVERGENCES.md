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

- Pi reads model metadata from its generated registry and a user `models.json`. Koine ships a small embedded catalog (`core/llm/models.json`) and reads `~/.koine/model-catalog.json` instead, because Pi's `models.json` has a different schema (providers, API keys, compat flags) that Koine does not implement. Prices in the embedded catalog come from third-party price pages and have not been checked against the vendor; the file records its source and check date.
- The `usage` object written on assistant messages follows Pi's field names (`input`, `output`, `cacheRead`, `cacheWrite`, `totalTokens`, `cost`) as best known; it has not been compared with Pi's source. Reasoning tokens are not tracked yet.

## Thinking, caching and retry

- Pi's built-in `thinkingBudgets` and its provider-specific level maps (`thinkingLevelMap`, OpenRouter and zai formats) are not documented well enough to copy, so Koine uses its own budgets (1024, 2048, 8192, 16384) and one mapping per API; the Claude catalog entries use Anthropic's adaptive thinking and effort as Anthropic's effort page describes it. These have been tested against fake servers only.
- Koine has one retry loop. Pi's separate provider-level retries (`retry.provider.maxRetries`) are accepted and ignored. There is no jitter, and without streaming `httpIdleTimeoutMs` bounds the whole response and has no default (Pi's is 300000), so a long non-streaming answer is not cut off unless the user sets a limit.
- A model the catalog does not know is sent thinking parameters only when the user chose a level for it (`--thinking`, a suffix, `/thinking` or `modelThinkingLevels`); Pi's default level applies to models it knows think. Anthropic cache breakpoints use the default 5-minute lifetime; Pi's long retention and OpenAI `prompt_cache_key` are not implemented.

## Settings

- `sessionShareDestination`, `collapseChangelog`, `enableInstallTelemetry`, `enableAnalytics`, `selfUpdateCommand` are `by-design` in `compat/settings.json`: Koine has no gist/Radius sharing, update check, telemetry or self-update. They are reported as ignored if set.
- Files live under `.koine/`, not `.pi/`. Koine-only keys (`model`, `maxTurns`, `hooks`, `plugin`, `providers`, `compaction.contextWindow`) are listed in the `koine` section of `compat/settings.json`.

## Command line

- `--mode json` writes every event of Pi's documented schema with Pi's field names (including `queue_update`, `entry_appended`, `session_info_changed`, `thinking_level_changed`, `compaction_*`, `auto_retry_*`, `summarization_retry_*` and `tool_execution_update`), but Koine does not stream from providers: each content block is one `text_delta` or one tool-call triple, and `message_update.usage` is the final usage. Pi's JSON page names fields without giving the inner shape of some of them; for these Koine follows the session-file format and does not claim parity: `entry` (the session line as Koine writes it), `compaction_end.result` (`summary`, `firstKeptEntryId`, `tokensBefore`, `estimatedTokensAfter`, no `details`; the token count after is Koine's estimate), `partialResult` and the message objects. `api` is derived from the provider (`anthropic-messages` for Anthropic, `openai-completions` for the rest). `agent_end.willRetry` is always false: retries and overflow recovery happen inside one run. `entry_appended` needs a session (none with `--no-session`). Queues have one source, `pi.sendUserMessage(text, {deliverAs})` (default follow-up); `queue_update` is reported when the agent loop next looks at the queues, and a message queued while idle waits for the next run. None of these shapes were compared with Pi's source.
- `-e` runs code, so it obeys the trust gate (ADR 0002): in an untrusted folder it is skipped with a warning, and an option such as `--plan` that only that extension defines is then an unknown option (the error says why). `--skill` and `--prompt-template` are data, keep the hidden-text audit, and load in any folder. `builtin:*` extensions (mcp, codemode, tool-search, llama.cpp) do not exist; `-e builtin:x` only warns.
- `--theme`, `--use-theme` and `--no-themes` are accepted and ignored with a warning; Koine has no themes (CLI-50). `--tui-mode`, `--no-mcp`, `--offline`, `--verbose`, `--export`, `--session-id` and `-na` are not options here (other rows).
- `--model` fuzzy matching only knows the model catalog (`core/llm/models.json` plus the user's `model-catalog.json`); an id it does not list is sent as written if its provider is known. Ties between equal matches of one provider take the undated, then the latest id; Pi's own tie-break was not checked. `--models` and `enabledModels` choose the startup model; there is no model cycling.
- Extension options are read after the extensions load, so a typo in an option name is reported only then (exit 2) and the extensions have started by that time.

## Environment

The page https://pi.dev/docs/latest/environment-variables was read on 2026-11-11. Every `PI_*` variable on it, and what Koine does:

- Process markers: `AI_AGENT=pi` is `AI_AGENT=koine`; `PI_CODING_AGENT=true` is `KOINE_CODING_AGENT=true`. Set by the CLI for itself and its children, not when `core/app` is embedded. The `PI_` names are not set.
- Shell tool session variables, given to the model's bash tool only: `PI_SESSION_ID` is `KOINE_SESSION_ID`; `PI_SESSION_FILE` is `KOINE_SESSION_FILE` (unset without a session file); `PI_PROVIDER` is `KOINE_SESSION_PROVIDER`; `PI_MODEL` is `KOINE_SESSION_MODEL` (not `KOINE_MODEL`, which already means "start with this model"); `PI_REASONING_LEVEL` is `KOINE_SESSION_REASONING_LEVEL`, set while the model is asked to think.
- Directories: `PI_CODING_AGENT_DIR` is `KOINE_AGENT_DIR`; `PI_CODING_AGENT_SESSION_DIR` is `KOINE_SESSION_DIR`.
- Not applicable, no `KOINE_` counterpart because the feature does not exist: `PI_PACKAGE_DIR` (no packages, a single binary), `PI_OFFLINE` and `PI_SKIP_VERSION_CHECK` (Koine does no catalog refresh or version check), `PI_SELF_UPDATE_COMMAND` (no self-update), `PI_TELEMETRY` (no telemetry), `PI_CODEMODE_MAX_WORKERS` (no codemode), `PI_SHARE_VIEWER_URL` (no `/share`), `PI_RADIUS_GATEWAY` (no `/bug`).
- Reserved for later, not read yet: `PI_CACHE_RETENTION` as `KOINE_CACHE_RETENTION` (provider caching, LLM area) and the terminal variables `PI_CLEAR_ON_SHRINK`, `PI_HARDWARE_CURSOR`, `PI_CLIPBOARD_OSC52`, `PI_HYPERLINKS`, `PI_PROGRAM_STATUS`, `PI_IMAGE_PROTOCOL`, `PI_TRUE_COLOR`, `PI_TUI_ESC_TIMEOUT` as the same names with `KOINE_` (the line-mode TUI has no use for them).
- Not `PI_` variables: `HTTP_PROXY` and `HTTPS_PROXY` are honoured by Go's default HTTP transport (not tested here); `VISUAL` and `EDITOR` are unused because Koine has no external-editor feature.

## Images

- Pi documents `images.autoResize` as "at most 2000 by 2000 pixels" and names `images.resize.maxBytes`, `images.maxPerMessage` and `inputLimits.images` without defaults on the pages Koine was built from. Koine applies the 2000 by 2000 limit and its own 4.5 MB cap on the encoded image (under Anthropic's 5 MB); it has no per-message or per-request image counts. Pi's resampling filter and re-encoding settings were not checked: Koine uses a box filter and JPEG quality 85.
- Resizing uses only the Go standard library (ADR 0004), which decodes PNG, JPEG and GIF. WebP and BMP images are sent unchanged with a note, and refused only when over the byte cap. A GIF that needs resizing becomes a PNG of its first frame; a small GIF is sent as it is.
- A catalog model without an `input` list allows images (Pi's default for `input` is `["text"]`); the built-in Claude models list `["text","image"]`. Unknown models are allowed so that a local vision model works without a catalog entry.
- The `read` tool answers an image with the text `Read image file [type]` (and a bracketed note when resized); Pi's exact wording was not checked. OpenAI-compatible providers cannot take images in tool messages, so Koine sends them as a following user message headed `Attached image(s) from tool result:`.
- The HTTP server (`/prompt`), `custom_message` entries with images and the extension `input` event's `images` (EXT-15) take or keep text only.
