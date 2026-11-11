# Intentional divergences from Pi

| Area             | Pi                      | Koine                                                                                                              | Why                                             |
| ---------------- | ----------------------- | ------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------- |
| Project code     | Runs after trust prompt | Off until `/trust`, `-trust` or `KOINE_TRUST=1`; harmless scalars still apply                                      | Safe default for a multi-ecosystem loader       |
| Unsupported APIs | Always present          | Stand-ins that never throw; reported by `/compat`; some props are `undefined` as upstream would be without a model | An extension should degrade, not crash the host |
| Skills           | Loaded as found         | Skills containing hidden Unicode control characters are refused                                                    | Prompt-injection defence                        |
| Providers        | Built-in registry       | Anthropic, OpenAI-compatible, and prefix-routed custom providers; unknown prefixes rejected                        | Avoid sending a prompt to the wrong vendor      |
| Config dir       | `.pi`                   | `.koine`, with `.pi`, `.opencode`, `.claude`, `.agents` also discovered                                            | Multi-ecosystem                                 |

## Settings

- `sessionShareDestination`, `collapseChangelog`, `enableInstallTelemetry`, `enableAnalytics`, `selfUpdateCommand` are `by-design` in `compat/settings.json`: Koine has no gist/Radius sharing, update check, telemetry or self-update. They are reported as ignored if set.
- Files live under `.koine/`, not `.pi/`. Koine-only keys (`model`, `maxTurns`, `hooks`, `plugin`, `providers`, `compaction.contextWindow`) are listed in the `koine` section of `compat/settings.json`.
