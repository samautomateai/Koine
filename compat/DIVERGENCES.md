# Intentional divergences from Pi

| Area | Pi | Koine | Why |
|---|---|---|---|
| Project code | Runs after trust prompt | Off until `/trust`, `-trust` or `KOINE_TRUST=1`; harmless scalars still apply | Safe default for a multi-ecosystem loader |
| Unsupported APIs | Always present | Stand-ins that never throw; reported by `/compat`; some props are `undefined` as upstream would be without a model | An extension should degrade, not crash the host |
| Skills | Loaded as found | Skills containing hidden Unicode control characters are refused | Prompt-injection defence |
| Providers | Built-in registry | Anthropic, OpenAI-compatible, and prefix-routed custom providers; unknown prefixes rejected | Avoid sending a prompt to the wrong vendor |
| Config dir | `.pi` | `.koine`, with `.pi`, `.opencode`, `.claude`, `.agents` also discovered | Multi-ecosystem |
