# Parity gap assessment: Koine vs upstream Pi 1.5.2

Status: re-audited 2026-11-12 (first pass 2026-11-10). Living document: when a gap closes, change its row in the detail files, update the counts
here, and link the requirement that now covers it.

**Scope.** Only upstream Pi (TypeScript) is the yardstick. The 197 extension _API_ items are tracked separately in `compat/pi.json`
(`koine -compat`). This assessment covers everything else: behaviour, CLI, tools, models, packages. It asks "what would a Pi user miss?", not
"what exists in Pi", so Pi features we should not copy are listed too.

**Method.** Every row of the four detail files was re-checked against Koine's code and tests on `main` and against Pi's published documentation
(pi.dev/docs/latest). Pi's TypeScript source was not reachable from the audit environment, so rows that only the source could settle say "not
compared". A row is HAVE only where the evidence column names code and an existing test; the audit itself ran no Go tests. The priority column
means how much the _remaining_ gap matters: HAVE and by-design rows carry none, and a PARTIAL row whose missing part is minor is P2 or P3.
Priorities and efforts are judgement calls.

## Scoreboard (262 behaviours assessed)

| Area                                     | Detail                   | Have    | Partial | Missing | By design | Open P0 | Open P1 |
| ---------------------------------------- | ------------------------ | ------- | ------- | ------- | --------- | ------- | ------- |
| CLI, modes, sessions, settings           | [cli.md](gap/cli.md)     | 31      | 19      | 8       | 7         | 0       | 2       |
| Tools, agent loop, context, prompts, TUI | [tools.md](gap/tools.md) | 44      | 30      | 7       | 2         | 0       | 5       |
| Providers, models, auth, MCP             | [llm.md](gap/llm.md)     | 11      | 36      | 6       | 1         | 0       | 6       |
| Extension behaviour, Pi packages         | [ext.md](gap/ext.md)     | 25      | 21      | 11      | 3         | 1       | 7       |
| **Total**                                |                          | **111** | **106** | **32**  | **13**    | **1**   | **20**  |

Read this honestly: at the first pass 57 of 222 behaviours matched. The foundations have since been built, and 111 of 262 now match, 13 more are
deliberate differences, and the only open P0 is the extension export surface. About 42% match fully. The 106 partial rows are mostly small or rare
corners; the open P1 rows below are what a daily user of Pi would hit first.

## The open P0 and P1 gaps

| Area       | Rows                                                                                                                                     |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Extensions | EXT-07 (P0): named imports from the shim modules do not all resolve, so 26 of 78 upstream example extensions fail to load                |
| Extensions | EXT-06, 17, 18, 19, 31, 48, 49: virtual modules, session events, tool registration, `registerProvider`, session control                  |
| Providers  | LLM-03, 04, 11, 22, 31, 36: OpenAI Chat and Responses corners, a catalog of Anthropic models only, thinking, overflow                    |
| Tools      | CTX-01 and CLI-44 (context files from parent directories), AGT-02 and AGT-03 (steering and follow-up in the terminal UI), SKL-03, TPL-03 |

Cheap and high-value on their own: completing the shim export lists (EXT-07), the parent-directory walk for context files, steering and follow-up
in the terminal UI, and the template quoting and argument fixes (TPL-02, TPL-03). The audits also found wrong behaviour in Koine that is not a gap
against Pi; those bugs are listed in the audit pull requests (#35 to #38).

## Deliberately not copying

Consolidated from all four reviews; each has a reason in the detail files.

- Phone-home features: `/share`, `/bug` upload, telemetry, version check, self-update.
- `codemode` and default-on deferral of MCP tools behind it: hides calls from the visible tool pipeline and adds a sandbox surface. MCP tools default to direct.
- Cache warming, virtual-model routers, classifier and image-generation model types, vendor-specific provider variants (token-plan regions, Radius).
- The pi-tui component API as part of the extension contract. Offer text-level UI methods (status, widget, notify) that any frontend can render.
- Package installs at startup. Koine installs only on an explicit command, with a lock and no install scripts.
- In-process extension execution. Per-extension processes stay.
- `bash` without a default timeout, and `AGENTS.override.md`.
- Pi's fail-open/fail-closed inconsistency for event handlers. Rule for Koine: gates fail closed, observers fail open.

## Where Koine already exceeds Pi

Any-language extension protocol, opencode plugin and Claude hook compatibility, project trust covering hooks and provider routes, hidden-text skill
audit, ancestor-aware `.agents/skills`, default `bash` timeout, per-extension process isolation, sub-agents through a `task` tool, and a tested
upstream-tracking pipeline. Keep these when closing gaps.

## Requirements this adds

Requirements for the foundations are in [requirements.md](requirements.md): `REQ-CLI-001`, `REQ-LLM-004`, `REQ-SESS-002`, `REQ-CMP-001`,
`REQ-MCP-001`, `REQ-PKG-001`, `REQ-EXT-011`, `REQ-TOOL-001`. They now cite tests as well as gap IDs; see each requirement for its status.
