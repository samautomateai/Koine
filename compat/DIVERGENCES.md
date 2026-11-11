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
