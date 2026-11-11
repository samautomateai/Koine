# koine specification

Status: v1 (protocol version `1`). Everything here is covered by tests; the
test that proves each section is named in **Verified by**.

koine is a Go coding agent whose extensibility is language-agnostic by design:
the core knows one wire protocol (§3) and one event vocabulary (§2). Pi
extensions, opencode plugins, Claude-style command hooks and native extensions
in any language are all adapters onto those two things.

```
 frontends                 core (Go, in-process)                         extensions
┌────────────┐   ┌──────────────────────────────────────────┐   ┌──────────────────────────┐
│ line TUI   │   │ app ── agent loop ── llm.Provider        │   │ JS sidecar (bun / node)  │
│ Bubble Tea │──▶│        │      │                          │   │  ├ Pi extensions (.ts/.js)│
│ Wails v2   │   │        │      └ tools.Registry ◀──────────┼──▶│  └ opencode plugins       │
│ HTTP + SSE │   │        └ events.Dispatcher ──┬───────────┼──▶│ any-language process      │
└────────────┘   │   skills · prompts · agents  │ hooks.Runner ▶ shell commands (Claude hooks)│
                 └──────────────────────────────────────────┘   └──────────────────────────┘
```

## 1. Discovery

All roots are optional. Earlier entries win on name clashes; project-level
roots beat user-level roots.

| Kind                                 | Project roots (`<cwd>/…`)                                                               | User roots (`~/…`)                                                                                   |
| ------------------------------------ | --------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Skills (`SKILL.md`)                  | `.koine/skills`, `.pi/skills`, `.agents/skills`, `.opencode/skill(s)`, `.claude/skills` | `.koine/skills`, `.pi/agent/skills`, `.agents/skills`, `.config/opencode/skill(s)`, `.claude/skills` |
| Prompt templates / commands (`*.md`) | `.koine/prompts`, `.pi/prompts`, `.opencode/command(s)`, `.claude/commands`             | `.koine/prompts`, `.pi/agent/prompts`, `.config/opencode/command(s)`, `.claude/commands`             |
| Sub-agents (`*.md`)                  | `.koine/agents`, `.pi/agents`, `.opencode/agent(s)`, `.claude/agents`                   | `.koine/agents`, `.pi/agent/agents`, `.config/opencode/agent(s)`, `.claude/agents`                   |
| Extensions                           | `.koine/extensions`, `.pi/extensions`, `.opencode/plugin(s)`                            | `.koine/extensions`, `.pi/agent/extensions`, `.config/opencode/plugin(s)`                            |
| Context files                        | `AGENTS.md`, `CLAUDE.md`, `.koine/AGENTS.md`                                            | `.koine/AGENTS.md`, `.pi/agent/AGENTS.md`, `.config/opencode/AGENTS.md`, `.claude/CLAUDE.md`         |
| Config                               | `.koine/config.json`, `opencode.json`, `.claude/settings.json` (hooks only)             | `.koine/config.json`, `.config/opencode/opencode.json`, `.claude/settings.json` (hooks only)         |

**Skills.** A skill is `SKILL.md` with frontmatter `name` (defaults to the
directory name) and a required `description`; skills without a description are
ignored. Pi roots additionally accept loose `*.md` files directly in the root.
Search depth is 3 levels; `node_modules` is skipped. Skills are advertised in
the system prompt as `<available_skills>` and loaded on demand with the `skill`
tool or `/skill:<name> [args]`.

**Prompt templates.** `$ARGUMENTS` and `$@` expand to the full argument string,
`$1`…`$9` to whitespace-split positional arguments (missing → empty).

**Sub-agents.** Frontmatter: `description`, `name` (defaults to file name),
`model` (`provider/model` → `model`; `inherit` → parent's), `mode`
(`primary` agents are not delegates), `tools`:
Claude form `Read, Grep` / `[Read, Bash]` / YAML list = allow-list;
opencode form `write: false` = deny-list. Tool names are canonicalised
(`MultiEdit`→`edit`, case-insensitive). Sub-agents run in a fresh context, never
get the `task` tool, and nesting is capped at depth 2.

**Extension entry points** inside an extension directory:
a loose `*.ts|js|mjs|cjs|mts|cts` file (`.d.ts` ignored); a sub-directory with
`extension.json` (§3.1); or a sub-directory with `index.ts|js|mjs|mts`.
Additionally: `opencode.json` `"plugin": [...]` (relative paths, absolute
paths, or npm names resolved from `<cwd>/node_modules`, `@version` stripped);
Pi `settings.json` `"extensions": [...]`; and `.koine/config.json`
`"extensions": [Spec…]`.

_Verified by:_ `skills.TestDiscoverAcrossEcosystems`, `prompts.TestDiscoverAndExpand`,
`agents.TestDiscover`, `agents.TestParseTools`, `app.TestDiscoverExtensions`,
`app.TestLoadConfigMergesAllSources`.

## 2. Event model

Eight canonical events. A handler may return a **Decision** (all fields optional;
the zero value means "no opinion").

| Event                     | Payload                                      | Decision fields honoured                                                                               |
| ------------------------- | -------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `session_start`           | `{cwd}`                                      | –                                                                                                      |
| `input`                   | `{text}`                                     | `block` (swallow the prompt), `text` (rewrite it)                                                      |
| `system_prompt`           | `{system}`                                   | `systemAppend`                                                                                         |
| `turn_start` / `turn_end` | `{turn}`                                     | –                                                                                                      |
| `tool_call`               | `{toolName, callId, input}`                  | `block`+`reason` (tool is not run; model sees `Blocked: <reason>` as an error), `args` (replace input) |
| `tool_result`             | `{toolName, callId, input, output, isError}` | `output` (replace what the model sees)                                                                 |
| `agent_end`               | `{}`                                         | –                                                                                                      |

```jsonc
// Decision
{ "block": true, "reason": "…", "args": {…}, "output": "…", "text": "…", "systemAppend": "…" }
```

Dispatchers run in order **hooks → extensions**. Decisions merge: `block`
short-circuits; the last `args`/`output`/`text` wins; `systemAppend` values are
newline-joined. A handler that errors or times out (30 s extensions, 60 s
hooks) **fails open** (no decision) and is logged.

_Verified by:_ `events.TestMerge`, `events.TestMultiStopsAtBlock`,
`agent.TestToolCallHooks`, `agent.TestInputHook`, `agent.TestSystemPromptContents`.

## 3. Extension wire protocol (language-agnostic)

Transport: the extension is a child process; the host speaks **newline-delimited
JSON-RPC 2.0** on its stdin/stdout. stderr lines are logged. Non-JSON lines on
stdout are logged and ignored (so stray `print`s don't break the channel).
Working directory is the extension's `dir` (default: project root); env gets
`KOINE_CWD`.

### 3.1 Manifest — `extension.json`

```json
{ "name": "my-ext", "command": ["python3", "./ext.py"], "env": { "K": "V" }, "dir": "optional" }
```

A `command[0]` beginning with `./` and the default `dir` resolve against the
extension's folder.

### 3.2 Host → extension (requests; reply required)

| Method            | Params                              | Result                                                                                 |
| ----------------- | ----------------------------------- | -------------------------------------------------------------------------------------- |
| `initialize`      | `{protocol: 1, host: "koine", cwd}` | `{name?, events?: [string]}` — `events` is the subscription list; omit to receive all. |
| `event`           | `{name, payload}`                   | a Decision (§2) or `{}`                                                                |
| `tool.execute`    | `{name, args, callId}`              | `{content: string, isError?: bool}`                                                    |
| `command.execute` | `{name, args}`                      | `{output: string}`                                                                     |

Host → extension **notification**: `shutdown` (exit promptly; killed after 2 s).
Unknown methods must be answered with error `-32601`.

### 3.3 Extension → host (notifications; no reply)

| Method              | Params                        | Effect                                                                              |
| ------------------- | ----------------------------- | ----------------------------------------------------------------------------------- |
| `register_tool`     | `{name, description, schema}` | adds a tool to the model's tool list (`schema` = JSON Schema, default empty object) |
| `register_command`  | `{name, description}`         | adds `/name`                                                                        |
| `log`               | `{level, message}`            | shown with `KOINE_DEBUG=1`                                                          |
| `send_user_message` | `{text}`                      | injects a user message                                                              |

`initialize` must complete within 30 s. If the process exits mid-call, pending
calls fail with `extension exited` (no hangs). A 25-line Python reference
implementation is in `core/app/testdata/proj/.koine/extensions/pyext/ext.py`.

_Verified by:_ `ext.TestProtocolRoundTrip`, `ext.TestExtensionCrashSurfacesAsError`,
`ext.TestContextCancelAbortsCall`, `ext.TestStartErrors`, `ext.TestSpecDirAndJSSpec`,
`app.TestEverythingWorksTogether` (Python extension).

## 4. Command hooks (Claude-compatible)

For shell-based, any-language policy hooks with zero protocol work. Configured
under `"hooks"` in `.koine/config.json` or `.claude/settings.json`; both the
flat `{matcher, command}` and nested `{matcher, hooks:[{type:"command", command}]}`
forms are accepted.

| koine event     | Hook event         | Stdin JSON (besides `hook_event_name`, `session_id`, `cwd`)                                     |
| --------------- | ------------------ | ----------------------------------------------------------------------------------------------- |
| `tool_call`     | `PreToolUse`       | `tool_name` (Claude names: `Bash`, `Read`, `Write`, `Edit`, `Grep`, `Glob`, `LS`), `tool_input` |
| `tool_result`   | `PostToolUse`      | `tool_name`, `tool_input`, `tool_response`                                                      |
| `input`         | `UserPromptSubmit` | `prompt`                                                                                        |
| `session_start` | `SessionStart`     | –                                                                                               |
| `agent_end`     | `Stop`             | –                                                                                               |

`matcher` is a case-insensitive regex, anchored, tested against both the koine
and the Claude tool name (`""`/`*` = all). **Exit code 2 blocks** (stderr is the
reason). Exit 0 may print JSON: `{"decision":"block","reason":…}`,
`{"continue":false,"stopReason":…}`,
`{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":…,"updatedInput":{…}}}`.
Any other failure or timeout fails open. Env: `CLAUDE_PROJECT_DIR`, `KOINE_PROJECT_DIR`.

_Verified by:_ the `hooks` package tests; `app.TestEverythingWorksTogether` (real `.claude/settings.json`).

## 5. JS sidecar: Pi and opencode compatibility

JS/TS extensions run in a sidecar (`runner.mjs`, embedded in the binary and
extracted to the user cache dir, content-addressed). Runtime: **Bun** if on
`PATH`, else **Node ≥ 22.6** with type-stripping; `KOINE_JS_RUNTIME=node|bun`
forces one. Imports of the host-provided packages are redirected to bundled
shims (Bun: virtual modules; Node: resolve hook). `typebox`/`zod` shims are used
**only** when the project hasn't installed the real package.

Every exported function of a module is called with one object that satisfies
Pi's `ExtensionAPI` **and** opencode's `PluginInput`; a returned object is
treated as opencode hooks.

### Pi (`@mariozechner/pi-coding-agent`)

| API                                                                                                                  | Status                                                                                       |
| -------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `pi.registerTool({name, description, parameters, execute})`                                                          | ✅ `execute(callId, params, signal, onUpdate, ctx)`; result `{content:[{type:"text",text}]}` |
| `pi.registerCommand(name, {description, handler})`                                                                   | ✅ `ctx.ui.notify` output is returned to the user                                            |
| `pi.on("tool_call")` → `{block, reason}`                                                                             | ✅                                                                                           |
| `pi.on("tool_result")` → `{content}`                                                                                 | ✅ (text content)                                                                            |
| `pi.on("input")` → `{action: "handled" \| "transform", text}`                                                        | ✅                                                                                           |
| `pi.on("before_agent_start")` → `{systemPrompt}`                                                                     | ✅ (diffed into `systemAppend`)                                                              |
| `pi.on("session_start" \| "turn_start" \| "turn_end" \| "agent_end")`                                                | ✅ observe-only                                                                              |
| `pi.sendUserMessage`, `pi.exec`, `pi.getActiveTools`                                                                 | ✅                                                                                           |
| `ctx.ui.*` (select/confirm/input/widgets/status)                                                                     | ⚠️ accepted, inert; `confirm`→`false`, others→`undefined`                                    |
| `registerProvider`, `registerShortcut`, `registerFlag`, `registerMessageRenderer`, `@mariozechner/pi-tui` components | ⚠️ accepted, inert                                                                           |
| Pi events not in §2 (e.g. `session_before_compact`, `model_select`)                                                  | ❌ never fired                                                                               |
| `@mariozechner/pi-ai`: `Type`, `StringEnum`                                                                          | ✅                                                                                           |

### opencode (`@opencode-ai/plugin`)

| API                                                                             | Status                                                                         |
| ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `tool({description, args, execute})`, `tool.schema.*`                           | ✅ args → JSON Schema (zod-like builder, or real `zod` if installed)           |
| `"tool.execute.before"` (`throw` blocks; mutate `output.args`)                  | ✅                                                                             |
| `"tool.execute.after"` (mutate `output.output`)                                 | ✅                                                                             |
| `"chat.message"` (mutate `output.parts[0].text`)                                | ✅                                                                             |
| `"experimental.chat.system.transform"` (`output.system.push`)                   | ✅                                                                             |
| `event` (`session.created`, `session.idle`)                                     | ✅ mapped from `session_start`, `agent_end`                                    |
| Plugin input: `directory`, `worktree`, `project`, `$` (shell tag), `client`     | ✅ `$` is real; `client` calls resolve to `{data:{}}` (`app.log` is forwarded) |
| `auth`, `config`, `permission.ask`, `chat.params`, `chat.headers`, other events | ❌ ignored                                                                     |

Tool-name mapping: koine built-ins are lower-case (`bash`, `read`, `write`, `edit`,
`ls`, `glob`, `grep`) matching Pi/opencode; hooks see Claude names (§4).

_Verified by:_ `jstest/shims.test.mjs` (8 tests, run under Node **and** Bun),
`app.TestEverythingWorksTogether`, `cmd/koine.TestEndToEndPrompt`,
`app.TestBrokenExtensionIsReportedNotFatal`.

## 6. Built-in tools

`read`(path, offset, limit) · `write`(path, content) · `edit`(path, old, new,
replaceAll) · `bash`(command, timeout) · `ls` · `glob`(pattern, path) ·
`grep`(pattern, path, glob) · `skill`(name) · `task`(agent, prompt).
Output over 30 000 bytes is middle-truncated. `edit` requires `old` to match
exactly once unless `replaceAll`. `bash` defaults to a 120 s timeout. `glob` and
`grep` skip `.git` and `node_modules`; `grep` skips binary files and caps at 200 hits.

## 7. Slash commands

`/help /clear /exit /quit /tools /skills /agents /commands /model [id[:level]] /thinking [level]`,
`/skill:<name> [args]`, extension commands (§3.3), then prompt templates (§1).
Anything else is a prompt to the model. Slash commands never reach the model
unless they expand to a prompt or skill.

## 8. Frontend contract

Every frontend is a thin consumer of `app.App.HandleInput(ctx, line, emit)`
and `Describe()`. `emit` receives `agent.Event`:

```json
{ "type": "text|thinking|tool_start|tool_end|notice|error|usage|done", "text": "", "tool": "", "callId": "", "args": {}, "isError": false }
```

- **Command line** (`koine [options] [--] [@files] [message]`, REQ-CLI-001): with a message, piped stdin, `-p` or `--mode json` it runs one
  prompt. The prompt is piped stdin, then `@file` contents as `<file name="…">` blocks, then the message, separated by blank lines.
  Text mode prints only the text after the last tool call to stdout; tool names, thinking, notices and errors go to stderr. `--mode json` prints one
  record per line on stdout in Pi's JSON event stream (below). Exit codes: 0 ok, 1 failed (provider, tool-loop or unknown `--model`), 2 usage (bad option, missing
  `@file`, nothing to do). Without a prompt it starts the interactive session. `koine --help` lists the options, and those that extensions registered.
- **Tool, resource and model options** (REQ-CLI-002): `-t a,b|pat*` allows only those tools; `-t +name,-name` changes the default selection (plain
  names and `+`/`-` cannot be mixed); `-xt list` removes tools by name or pattern after everything else; `-nbt` drops the built-in tools and keeps
  extension tools; `-nt` offers none. `-e PATH`, `--skill PATH`, `--prompt-template PATH` add resources (repeatable; a file, or a directory);
  `-ne`, `-ns`, `-np`, `-nc` switch off discovered extensions, skills, prompt templates and `AGENTS.md`/`CLAUDE.md`; explicit paths still load.
  `--theme`, `--use-theme`, `--no-themes` are accepted and ignored. `--model` takes an id, `provider/id` or part of an id or name; `--provider` limits
  it to one provider; `--models p1,p2` or `enabledModels` give the scope, whose first model starts the session when no model is set (`--model`,
  `$KOINE_MODEL`, `defaultModel`); `--api-key` replaces the key for this run; `--list-models [search]` lists the catalog. Options registered by an
  extension (`pi.registerFlag`) are `--name` (boolean) or `--name VALUE` / `--name=VALUE` (string); other unknown options exit 2.
- **JSON event stream** (REQ-CLI-003): `--mode json` writes one record per line: `{"type":"session","version":3,"id","timestamp","cwd"}`, then
  `agent_start`, `turn_start`, `message_start`/`message_end` (user), the assistant `message_start`, `message_update` records
  (`assistantMessageEvent` `text_start|text_delta|text_end|toolcall_start|toolcall_delta|toolcall_end`), `message_end`, `tool_execution_start`
  `{toolCallId,toolName,args}`, `tool_execution_end` `{toolCallId,toolName,result,isError,durationMs}`, the `toolResult` messages, `turn_end`
  `{message,toolResults}`, and finally `agent_end` `{messages,willRetry}` and `agent_settled` `{aborted}`. Each block is one delta. Not written:
  `queue_update`, `entry_appended`, `session_info_changed`, `thinking_level_changed`, compaction and retry events.
- **Environment** (REQ-CLI-004): the CLI sets `AI_AGENT=koine` and `KOINE_CODING_AGENT=true`; the bash tool also gets `KOINE_SESSION_ID`,
  `KOINE_SESSION_FILE` (unset with `--no-session`), `KOINE_SESSION_PROVIDER` and `KOINE_SESSION_MODEL`. The mapping of Pi's variables is in
  `compat/DIVERGENCES.md`.
- **Sessions** (REQ-SESS-001): every run writes `~/.koine/sessions/--<cwd with / as ->--/<timestamp>_<uuid>.jsonl` in Pi's v3 format.
  `-c` continues the latest, `--session ID|PATH|NAME` resumes, `--fork` copies, `-n NAME` names, `--no-session` writes nothing, `-r` lists.
  Slash commands: `/new` (also `/clear`), `/session`, `/name [text]`, `/resume [number|ref]`, `/fork`.
- **Session tree** (REQ-SESS-002): sessions are trees of entries; new entries attach to the current position (the leaf). `/tree [filter]` lists
  entries (`id`, `*` on the active branch, `[label]`, `<- current`); filters are `default`, `no-tools`, `user-only`, `labeled-only`, `all`, starting
  from `treeFilterMode`. `/tree <id|label> [--summary|--no-summary] [focus]` moves there: a user message moves to its parent and its text is
  printed so it can be sent again as a new branch. With `--summary` (or "Summarise" from a frontend's `Choose`) the abandoned branch is summarised
  into a `branch_summary` entry (`branchSummary.reserveTokens`, default 16384, is kept free of the context window); `branchSummary.skipPrompt` removes
  the question and the hint. `/label [@id] <text>` labels an entry (default: the current position; `-` clears; no argument lists labels), `/clone`
  copies the current branch to a new session whose header names the old one as `parentSession`, `/fork <id>` does so from before an earlier user
  message, `/export [file]` writes `.jsonl` (the file as stored) or HTML (default `koine-session-<id>.html`; never overwrites; may contain prompts, tool
  output and file contents). `-r` on a terminal shows the sessions numbered and reads a choice (piped, it only lists). Files with `version` 1 or 2 are
  migrated on open (ids and parents added, `firstKeptEntryIndex` becomes `firstKeptEntryId`, role `hookMessage` becomes `custom`) after saving
  `<file>.v<N>.bak`. `branch_summary` and `custom_message` entries reach the model as user messages; `custom`, `label` and unknown entries do not.
- **Settings** (REQ-CFG-001): `~/.koine/settings.json` and `<project>/.koine/settings.json`, Pi's key names. The status of every Pi key is
  in `compat/settings.json`. Acted on: `defaultProvider`, `defaultModel`, `defaultTools`, `enabledModels`, `sessionDir`, `extensions`, `compaction.*`, `branchSummary.*`, `treeFilterMode`, the thinking settings, `retry.*`, `httpIdleTimeoutMs` (see below). Koine-only:
  `model`, `maxTurns`, `hooks`, `plugin`, `providers`, `compaction.contextWindow`. Project values override user values; global-only keys are
  ignored in a project; an untrusted project applies only `model`, `maxTurns`, `enabledModels`, `compaction`, the thinking settings and the retry settings (not the tree settings). Problems are listed at startup (`/reload` re-checks).
- **Agent directory and prompt files** (REQ-CFG-002): the user directory is `~/.koine`, or `$KOINE_AGENT_DIR`. `SYSTEM.md` (replaces the system prompt) and `APPEND_SYSTEM.md` (added after the context files) are read from a trusted project's `.koine/` or `.pi/`, else from the user directory or `~/.pi/agent`; the first non-blank file wins and files are never combined. `--system-prompt` and `--append-system-prompt` override them.
- **Compaction** (REQ-CMP-001): `/compact [focus]`, automatic before a turn would overflow, and once on a provider "prompt too long" error.
  Config (`settings.json`): `compaction.enabled`, `contextWindow`, `reserveTokens`, `keepRecentTokens`.
- **Usage and cost** (REQ-LLM-004): the model catalog is built in; `~/.koine/model-catalog.json` (`{"models":[{"id","contextWindow","maxOutput","cost":{"input","output","cacheRead","cacheWrite"},"longContext":{"threshold","multiplier"}}]}`, USD per million tokens) adds or replaces entries by id. Each response emits an event `{"type":"usage","usage":{input,output,cacheRead,cacheWrite,cost?,contextTokens,contextWindow,contextPercent}}`; `cost` is absent when the model has no known price. Assistant messages in the session file carry Pi's `usage` object. `/session` prints totals, cost and context use. `compaction.contextWindow` overrides the catalog's window.
- **Thinking** (REQ-LLM-005): levels `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`. The level for a request is, first match wins:
  `--thinking LEVEL`, a `:LEVEL` suffix on `--model` or `/model` (only a trailing level name counts, so `llama3:8b` is a model id), `/thinking LEVEL`,
  `modelThinkingLevels["provider/modelId"]`, `defaultThinkingLevel`, then `medium`. `--thinking` with a bad level exits 2. Only models the catalog
  marks `reasoning`, or any model when a level was chosen for it explicitly, are sent thinking parameters; others are sent none. A level above the
  model's maximum is lowered. Anthropic: catalog mode `adaptive` sends `thinking: {"type":"adaptive"}` and `output_config.effort` (`minimal` as `low`);
  `budget` sends `thinking: {"type":"enabled","budget_tokens":N}` with N from `thinkingBudgets` (`minimal`, `low`, `medium`, `high`; defaults 1024, 2048,
  8192, 16384; levels above `high` use `high`'s) and raises `max_tokens` to fit. `off` sends `thinking: {"type":"disabled"}` unless the catalog entry's
  `reasoning.off` says otherwise (`omit`, or another `thinking.type`). OpenAI-compatible: `reasoning_effort` (`max` is sent as `xhigh`, `off` sends nothing)
  and `max_completion_tokens` in place of `max_tokens`; `reasoning_content` or `reasoning` in the answer becomes a thinking block. Thinking blocks
  are events `{"type":"thinking","text":…}` (not emitted when `hideThinkingBlock` is true, nor for redacted blocks), stay in the history, and are
  saved in the session file as `{"type":"thinking","thinking":…,"thinkingSignature":…,"redacted":true}` with `thinkingLevel` on the assistant
  message, as Pi documents them. A catalog file may say `"reasoning": true` or `{"mode":"adaptive|budget|effort","max":"<level>","off":"<type>"}`.
- **Prompt caching** (REQ-LLM-005): Anthropic requests mark `cache_control: {"type":"ephemeral"}` on the system prompt, the last tool and the last
  block of the last user message. Cache reads and writes arrive in `Usage` (`cacheRead`, `cacheWrite`). No long retention and no cache warming.
- **Retry and timeouts** (REQ-LLM-002, REQ-LLM-005): network errors, timeouts, 429 and 5xx are retried. `retry.maxRetries` (default 3, so four attempts;
  0 or `retry.enabled: false` turns retries off), `retry.baseDelayMs` (2000, doubled each time) and `retry.maxAgentDelayMs` (60000, the longest backoff).
  A `Retry-After` header is waited for, but one longer than `retry.provider.maxRetryDelayMs` (60000; 0 = no limit) ends the retries with an error.
  A 429 that reports an exhausted quota or credit is not retried. `retry.provider.timeoutMs`, else `httpIdleTimeoutMs` (a number, 0 or `"disabled"`),
  bounds one response; with neither set there is no timeout. `retry.provider.maxRetries` is accepted and ignored. Each retry emits a `notice`.
  A provider answer that does not fit the context window (HTTP 413, or the provider's own wording) triggers one compaction and one retry per prompt.
- **HTTP/SSE** (`koine --serve ADDR`): `GET /api/info` → `app.Info`;
  `POST /api/prompt {"text": …}` → `text/event-stream` of events then `done`;
  `409` if a run is in progress, `400` on a bad body; client disconnect cancels the run.
  This is the integration point for an Electron UI.
- **Wails v2** (`desktop/`): `Backend.Info()`, `Backend.Send(text)`, `Backend.Cancel()`;
  events on `koine:event`, completion on `koine:done`.
- **Bubble Tea** (`tui-bubbletea/`): same calls, in-process.

_Verified by:_ `server` tests, `tui` tests, `cmd/koine.TestServeMode`.

## 9. Configuration & environment

| Name                           | Meaning                                                                                              |
| ------------------------------ | ---------------------------------------------------------------------------------------------------- |
| `ANTHROPIC_API_KEY`            | required to chat; without it everything else still starts and the first prompt reports a clear error |
| `ANTHROPIC_BASE_URL`           | API endpoint override (also used by the e2e tests)                                                   |
| `KOINE_MODEL` / config `model` | model id, default `claude-sonnet-5-5`                                                                |
| `KOINE_JS_RUNTIME`             | `node` or `bun`                                                                                      |
| `KOINE_DEBUG`                  | print extension logs to stderr                                                                       |

Config merge order (later wins for scalars, hooks/plugins/extensions append):
`~/.claude/settings.json` (hooks only) → `~/.config/opencode/opencode.json` →
`~/.koine/config.json` → project `.claude/settings.json` → `opencode.json` → `.koine/config.json`.
`//` comment lines and trailing commas are tolerated.

## 10. Known limits (v1)

- Providers: Anthropic and OpenAI-compatible Chat Completions (plus custom routes); non-streaming, retried as configured (four attempts by default).
- Upstream tracking: see `docs/UPSTREAM.md`; unsupported Pi surface is listed by `koine -compat`.
- No permission prompts yet.
- The `bubbletea` and `wails` frontends are written against the same API but are
  separate Go modules that need network access for `go mod tidy`; they are not
  built or tested by `task check`.
- Pi/opencode surface not listed as ✅ in §5 is not supported.
