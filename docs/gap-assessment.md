# Parity gap assessment: Koine vs upstream Pi 1.5.2

Status: first pass, 2026-11-10. Living document: when a gap closes, change its row in the detail files, update the counts here, and link the
requirement that now covers it.

**Scope.** Only upstream Pi (TypeScript) is the yardstick. The 197 extension _API_ items are tracked separately in `compat/pi.json`
(`koine -compat`). This assessment covers everything else: behaviour, CLI, tools, models, packages. It asks "what would a Pi user miss?", not
"what exists in Pi", so Pi features we should not copy are listed too.

**Method.** Four independent read-only reviews compared the upstream source and docs with Koine's code. Each row names its evidence. I spot-checked
the claims that carry most weight (no positional prompt, tool named `glob` not `find`, no image/usage/cache/thinking support in `core/llm`, no parent
walk for `AGENTS.md`, `hasUI` hard-coded to false, no parallel tool execution) against the code and all held. Treat the rest as accurate but unaudited;
priorities and efforts are judgement calls.

## Scoreboard (222 behaviours assessed)

| Area                                     | Detail                   | Have   | Partial | Missing | By design | Open P0 | Open P1 |
| ---------------------------------------- | ------------------------ | ------ | ------- | ------- | --------- | ------- | ------- |
| CLI, modes, sessions, settings           | [cli.md](gap/cli.md)     | 5      | 14      | 27      | 5         | 10      | 17      |
| Tools, agent loop, context, prompts, TUI | [tools.md](gap/tools.md) | 33     | 23      | 21      | 0         | 7       | 19      |
| Providers, models, auth, MCP             | [llm.md](gap/llm.md)     | 7      | 37      | 5       | 0         | 11      | 11      |
| Extension behaviour, Pi packages         | [ext.md](gap/ext.md)     | 12     | 22      | 9       | 2         | 7       | 17      |
| **Total**                                |                          | **57** | **96**  | **62**  | **7**     | **35**  | **64**  |

Read this honestly: Koine is a working agent core with a broad _compatibility surface_ (extensions, skills, hooks, trust), but as a daily-driver
coding agent it is far from Pi. About 26% of behaviours match, and the 35 open P0 items cluster in a few places.

## What the P0 gaps have in common

Most P0 rows trace back to six missing foundations. Fix these in order, because later ones depend on earlier ones.

| #   | Foundation                                         | Unblocks                                                                                                               | Main rows                   |
| --- | -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | --------------------------- |
| 1   | **Streaming provider interface**                   | live output in every frontend, idle timeouts, tool-call deltas, interrupt latency                                      | LLM-02, AGT-01              |
| 2   | **Model catalog + usage/cost accounting**          | context %, compaction triggers, image gating, thinking support, footer                                                 | LLM-11, LLM-28, CMP-07      |
| 3   | **Session persistence** (JSONL, Pi v3 tree format) | resume/continue/fork, compaction, branch summaries, session events for extensions                                      | CLI-15..21, SESS-01, EXT-17 |
| 4   | **Compaction + overflow recovery**                 | long tasks; today history grows until the provider rejects it                                                          | CMP-01..05, LLM-31          |
| 5   | **Scriptable CLI**                                 | positional prompt, stdin, `@file`, auto print mode, clean stdout/stderr, `--mode json`, tool/resource flags, `--model` | CLI-01..06, 12, 29..31, 48  |
| 6   | **Credentials + MCP**                              | OAuth login for Claude, ChatGPT, Copilot, OpenRouter (REQ-AUTH-003); `auth.json` (REQ-AUTH-001) and MCP are done       | LLM-17..19                  |

Cheap and high-value on their own, independent of the foundations: parent-directory `AGENTS.md` (CTX-01), prompt-template argument fixes
(TPL-02..04), `ctx.mode`/`hasUI` passed through to extensions (EXT-25, built), complete shim export lists so extensions do not fail at link time (EXT-07), prompt caching
(LLM-25), thinking levels with block replay (LLM-22/23).

## Proposed phases

Sizes are rough (S ≈ days, M ≈ 1–2 weeks, L ≈ several weeks of focused work).

| Phase                   | Goal                                            | Contents                                                                                                                                                                                                           | Size |
| ----------------------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---- |
| A. Usable in scripts    | `koine -p` behaves like `pi -p`                 | CLI-01..04, 12, 29..31, 48; CTX-01; TPL-02..04; TOOL-12, 13, 17, 18, 20; EXT-25                                                                                                                                    | M    |
| B. Usable for real work | long sessions survive                           | LLM-02 streaming; LLM-11/28 catalog + usage; LLM-25 caching; LLM-22/23 thinking; sessions CLI-15/16/17/19; compaction CMP-01..05, LLM-31                                                                           | L    |
| C. Ecosystem            | Pi/Claude/opencode assets and servers just work | MCP LLM-42/45/46 (+HTTP 43); credentials LLM-14/15/16; extension shim exports EXT-07; extension UI channel EXT-24/26 (built, REQ-EXT-005, REQ-RPC-001); tool fidelity EXT-12..14, 20, 45 (built, REQ-EXT-006..009) | L    |
| D. Packages             | install Pi packages                             | EXT-34..40 (hash-locked, `--ignore-scripts`, explicit install only); `-e/--skill` flags EXT-41                                                                                                                     | L    |
| E. Frontends            | interactive quality                             | Bubble Tea and Wails v3 as RPC clients (REQ-FE-003, done) and built and tested in CI (REQ-FE-002, done)                  ; TUI-06 (per-tool renderers); steering/follow-up AGT-02/03; parallel tools AGT-04        | L    |
| F. Later                |                                                 | Other OAuth (LLM-20), Vertex and Bedrock (LLM-06, 07), more providers (LLM-10), HTML export, tree views, images (TOOL-03, LLM-27)                                                                                  | L    |

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

Planned requirements, one per foundation, are in [requirements.md](requirements.md): `REQ-CLI-001`, `REQ-LLM-004`, `REQ-SESS-002`, `REQ-CMP-001`,
`REQ-MCP-001`, `REQ-PKG-001`, `REQ-EXT-011`, `REQ-TOOL-001`. They cite gap IDs, not test names, because nothing is implemented yet.
