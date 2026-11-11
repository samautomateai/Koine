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

`/help /clear /exit /quit /tools /skills /agents /commands`,
`/skill:<name> [args]`, extension commands (§3.3), then prompt templates (§1).
Anything else is a prompt to the model. Slash commands never reach the model
unless they expand to a prompt or skill.

## 8. Frontend contract

Every frontend is a thin consumer of `app.App.HandleInput(ctx, line, emit)`
and `Describe()`. `emit` receives `agent.Event`:

```json
{ "type": "text|tool_start|tool_end|notice|error|done", "text": "", "tool": "", "callId": "", "args": {}, "isError": false }
```

- **Command line** (`koine [options] [--] [@files] [message]`, REQ-CLI-001): with a message, piped stdin, `-p` or `--mode json` it runs one
  prompt. The prompt is piped stdin, then `@file` contents as `<file name="…">` blocks, then the message, separated by blank lines.
  Text mode prints only the text after the last tool call to stdout; tool names, notices and errors go to stderr. `--mode json` prints one
  `agent.Event` per line on stdout. Exit codes: 0 ok, 1 failed (provider, tool-loop or unknown `--model`), 2 usage (bad option, missing
  `@file`, nothing to do). Without a prompt it starts the interactive session. `koine --help` lists the options.
- **Sessions** (REQ-SESS-001): every run writes `~/.koine/sessions/--<cwd with / as ->--/<timestamp>_<uuid>.jsonl` in Pi's v3 format.
  `-c` continues the latest, `--session ID|PATH|NAME` resumes, `--fork` copies, `-n NAME` names, `--no-session` writes nothing, `-r` lists.
  Slash commands: `/new` (also `/clear`), `/session`, `/name [text]`, `/resume [ref]`, `/fork`.
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

- Providers: Anthropic and OpenAI-compatible Chat Completions (plus custom routes); non-streaming, up to 4 attempts on 429/5xx/network errors.
- Upstream tracking: see `docs/UPSTREAM.md`; unsupported Pi surface is listed by `koine -compat`.
- No context compaction, session persistence, or permission prompts yet.
- The `bubbletea` and `wails` frontends are written against the same API but are
  separate Go modules that need network access for `go mod tidy`; they are not
  built or tested by `task check`.
- Pi/opencode surface not listed as ✅ in §5 is not supported.
