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
│ Wails v3   │   │        │      └ tools.Registry ◀──────────┼──▶│  └ opencode plugins       │
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

The canonical events. A handler may return a **Decision** (all fields optional;
the zero value means "no opinion").

| Event                     | Payload                                                                      | Decision fields honoured                                                                                                            |
| ------------------------- | ---------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `session_start`           | `{cwd, reason, previousSessionFile?}`                                        | –                                                                                                                                   |
| `input`                   | `{text, source, images?}`                                                    | `block` (swallow the prompt), `handled` (an extension took it; no run), `text` (rewrite it), `images` (replace them)                |
| `system_prompt`           | `{system}`                                                                   | `systemAppend`                                                                                                                      |
| `before_agent_start`      | `{prompt, images, systemPrompt, systemPromptOptions}`                        | `systemPrompt` (replaces the prompt for this run), `message` (`{customType, content, display, details?}` added to the conversation) |
| `turn_start` / `turn_end` | `{turn}`                                                                     | –                                                                                                                                   |
| `tool_call`               | `{toolName, callId, input}`                                                  | `block`+`reason` (tool is not run; model sees `Blocked: <reason>` as an error), `terminate` (with `block`), `args` (replace input)  |
| `tool_result`             | `{toolName, callId, input, output, isError, content, details}`               | `output`, `content` (Pi blocks: text and image), `details`, `isError`, `terminate`                                                  |
| `context`                 | `{messages}`                                                                 | `messages` (replace the conversation for this request; Koine's message shape, not Pi's)                                             |
| `agent_end`               | `{}`                                                                         | –                                                                                                                                   |
| `tool_execution_end`      | `{toolCallId, toolName, result, isError}`                                    | –                                                                                                                                   |
| `tool_execution_update`   | `{toolCallId, toolName, args, partialResult}`                                | –                                                                                                                                   |
| `tool_execution_start`    | `{toolCallId, toolName, args}`                                               | –                                                                                                                                   |
| `session_tree`            | `{newLeafId, oldLeafId, fromExtension}`                                      | –                                                                                                                                   |
| `session_compact`         | `{reason, willRetry, fromExtension, compactionEntry}`                        | –                                                                                                                                   |
| `session_before_tree`     | `{preparation: {targetId, oldLeafId, userWantsSummary, customInstructions}}` | `cancel` (stop the operation)                                                                                                       |
| `session_before_compact`  | `{reason, customInstructions, willRetry}`                                    | `cancel` (stop the operation)                                                                                                       |
| `session_before_fork`     | `{entryId, position: before                                                  | at}`                                                                                                                                | `cancel` (stop the operation) |
| `session_before_switch`   | `{reason: new                                                                | resume, targetSessionFile?}`                                                                                                        | `cancel` (stop the operation) |
| `session_shutdown`        | `{reason, targetSessionFile?}`                                               | `cancel` (stop the operation)                                                                                                       |

```jsonc
// Decision
{ "block": true, "reason": "…", "args": {…}, "output": "…", "text": "…", "systemAppend": "…",
  "isError": true, "terminate": true, "messages": [ … ],
  "cancel": true, "handled": true, "content": [ … ], "details": {…}, "images": [ … ],
  "systemPrompt": "…", "message": { "customType": "…", "content": "…", "display": true } }
```

Dispatchers run in order **hooks → extensions**. Decisions merge: `block`
short-circuits; the last `args`/`output`/`text` wins; `systemAppend` values are
newline-joined; `terminate` and `isError` are set if any handler sets them and `messages` is the last non-empty one. `cancel` (the
`session_before_*` gates) short-circuits like `block`; `handled` also sets `block`; `content`, `details`, `images`, `systemPrompt` and `message` follow the
last-wins rule. When several extensions handle one event the host passes what each changed (args, output, `isError`, `content`, `details`, `text`,
`images`, `systemPrompt`) to the next one in its payload. A host-level failure (an extension that times out after 30 s, hooks after 60 s, or exits)
**fails open** (no decision) and is logged. Inside the JS sidecar a handler that throws is reported with the `ext_error` notification and the
extension carries on, except a throwing `tool_call` handler, which **blocks** the call: gates fail closed, observers fail open.

_Agent-loop decisions_ (REQ-AGT-001): the tool calls of one assistant message are first checked in order (`tool_call`), then the allowed ones run
at the same time; `tool_result` fires as each finishes. The loop stops after a batch only if **every** result in it asked to `terminate` (a
blocked call with `terminate`, or a `tool_result` with it). `context` fires before every model request; `messages` must be a JSON array of
messages in `llm.Message` form (`role`, `content` blocks of type `text`, `thinking`, `tool_use`, `tool_result`); one that does not decode is
ignored with a notice. The JS sidecar maps Pi's `tool_call` `{block, reason, terminate}` and `tool_result` `{content, isError}`, and never
subscribes Pi handlers to `context`.

_Extension API events_ (REQ-EXT-006..008): `tool_call` handlers may change `event.input` in place; the final input becomes `args`. `tool_result` and `input`
handlers of one extension are chained. `before_agent_start` is sent once per prompt (not per model request). `input.source` is `interactive`,
`rpc` or `extension`. A `session_before_*` gate answering `{cancel: true}` stops the operation (`/new`, `/resume`, `/fork`, `/clone`, `/tree`, `/compact`
and automatic compaction); a `compaction`, `summary` or `skipConversationRestore` field in the answer is ignored. `session_shutdown` and
`session_start` carry `reason`: `quit` (shutdown only), `reload`, `new`, `resume` or `fork`, `startup` (start only), `targetSessionFile` (shutdown) and
`previousSessionFile` (start). `tool_execution_*` are observe-only and have no parent id. Before `tool_call` the arguments are coerced and checked
against the tool's schema (`tools.Validate`); invalid ones give the model `Invalid arguments for tool "x":` with a `- path: problem` line each, and
`tool_call` is not sent. Not sent: `message_*`, `context` (to Pi handlers), the provider events.

_Verified by:_ `events.TestMerge`, `events.TestMultiStopsAtBlock`, `events.TestMergeCarriesTheExtensionAPIFields`, `events.TestMultiStopsAtCancel`,
`agent.TestInputHandledBlockedAndTransformed`, `agent.TestBeforeAgentStartReplacesThePromptForOneRunAndAddsAMessage`,
`agent.TestToolExecutionEventsReachExtensions`, `agent.TestCompactionGateAndEvent`, `app.TestSessionGatesCancelSwitchForkAndTree`,
`app.TestSessionLifecycleEventsReachExtensions`, `tools.TestValidateCoercesLooseValuesTheWayPiDoes`,
`agent.TestToolCallHooks`, `agent.TestInputHook`, `agent.TestSystemPromptContents`,
`agent.TestTerminateStopsTheLoopOnlyWhenEveryResultAsksForIt`, `agent.TestContextEventTransformContextAndPrepareNextTurn`.

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

| Method             | Params                                                    | Result                                                                                 |
| ------------------ | --------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `initialize`       | `{protocol: 1, host: "koine", cwd, trusted, mode, hasUI}` | `{name?, events?: [string]}` — `events` is the subscription list; omit to receive all. |
| `event`            | `{name, payload}`                                         | a Decision (§2) or `{}`                                                                |
| `tool.execute`     | `{name, args, callId}`                                    | `{content: string, isError?: bool}`                                                    |
| `command.execute`  | `{name, args}`                                            | `{output: string}`                                                                     |
| `command.complete` | `{name, prefix}`                                          | `{items: [{value, label}]}`                                                            |

Host → extension **notifications**: `shutdown` (exit promptly; killed after 2 s); `cancel` `{id}` (the host gave up on request `id`: stop
its work; the JS sidecar aborts that request's `AbortSignal`); `bus_event` `{channel, data}` (`pi.events` from another extension); `host_state`
`{commands, tools}` (what `pi.getCommands()` and `ctx.tools` answer from); `flags` `{values}`. `mode` is `tui`, `rpc`, `json` or `print`; `hasUI`
says that a frontend answers dialogs.
Unknown methods must be answered with error `-32601`.

### 3.3 Extension → host (notifications; no reply)

| Method              | Params                                                                                     | Effect                                                                              |
| ------------------- | ------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------- |
| `register_tool`     | `{name, description, schema}`                                                              | adds a tool to the model's tool list (`schema` = JSON Schema, default empty object) |
| `register_command`  | `{name, description, hasCompletions?}`                                                     | adds `/name`; numbered `name:1`, `name:2` if several extensions use the name        |
| `log`               | `{level, message}`                                                                         | shown with `KOINE_DEBUG=1`                                                          |
| `send_user_message` | `{text}`                                                                                   | injects a user message                                                              |
| `ui`                | `{method, …}` (notify, setStatus, setWidget, setTitle, setWorkingMessage, set_editor_text) | fire-and-forget UI request for the frontend (§12)                                   |
| `bus_emit`          | `{channel, data}`                                                                          | relayed to the other extensions as `bus_event`                                      |
| `send_message`      | `{customType, content, display, details?, deliverAs?, triggerTurn?}`                       | `pi.sendMessage`: a custom message in the conversation and the session              |
| `ext_error`         | `{event, error}`                                                                           | a handler threw; the frontends get an `extension_error` event                       |
| `shutdown_request`  | `{}`                                                                                       | `ctx.shutdown()`: the host quits when the work in progress is done                  |

`register_tool` may also carry `promptSnippet` (a string, one line for the system prompt's `<tools>` section) and `promptGuidelines`
(an array of strings, bullets for `<rules>` while the tool is active). Both are optional; `ext.TestRegisteredToolsCarryPromptSnippetAndGuidelines`.

Extension → host **requests** (numbered by the extension; the host answers with a result or an error). They are served on goroutines of their own,
because a dialog waits for a person:

| Method          | Params                                                                                                  | Result                             |
| --------------- | ------------------------------------------------------------------------------------------------------- | ---------------------------------- |
| `ui.dialog`     | `{method: select\|confirm\|input\|editor, title, options?, message?, placeholder?, prefill?, timeout?}` | `{value?, confirmed?, cancelled?}` |
| `tools.execute` | `{name, args}`                                                                                          | `{content: string, isError: bool}` |

A dialog without a frontend, or one that times out (`timeout` in ms), resolves as `{cancelled: true}`. `tools.execute` validates the arguments, runs
the `tool_call` hook and the tool, and does not add to the transcript.

`initialize` must complete within 30 s. If the process exits mid-call, pending
calls fail with `extension exited` (no hangs). A 25-line Python reference
implementation is in `core/app/testdata/proj/.koine/extensions/pyext/ext.py`.

_Verified by:_ `ext.TestProtocolRoundTrip`, `ext.TestExtensionCrashSurfacesAsError`,
`ext.TestContextCancelAbortsCall`, `ext.TestStartErrors`, `ext.TestSpecDirAndJSSpec`,
`app.TestEverythingWorksTogether` (Python extension).

### 3.4 `resources_discover` (extension contributes resources)

After `session_start` (`reason: "startup"`) and after a reload (`reason: "reload"`) the host sends the `event` request `resources_discover` with
payload `{cwd, reason}`. An extension may answer with a Decision carrying `resources`:

```json
{ "resources": { "skillPaths": ["./skills"], "promptPaths": ["./prompts/review.md"], "themePaths": ["./themes/dark.json"] } }
```

Skill paths are skill directories, `SKILL.md` files or directories of skills; prompt paths are `.md` files or directories. Relative paths resolve
against the extension's folder (the JS sidecar does this for Pi handlers, which return `skillPaths`, `promptPaths` and `themePaths`) or, for a
protocol extension, its working directory. Answers from several extensions are added together. Theme paths are reported but not applied. Trust
applies as to any extension: project extensions answer only in a trusted folder.

## 4. Command hooks (Claude-compatible)

For shell-based, any-language policy hooks with zero protocol work. Configured
under `"hooks"` in `.koine/config.json` or `.claude/settings.json`; both the
flat `{matcher, command}` and nested `{matcher, hooks:[{type:"command", command}]}`
forms are accepted.

| koine event     | Hook event         | Stdin JSON (besides `hook_event_name`, `session_id`, `cwd`)                                                                        |
| --------------- | ------------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| `tool_call`     | `PreToolUse`       | `block`+`reason` (tool is not run; model sees `Blocked: <reason>` as an error), `terminate` (with `block`), `args` (replace input) |
| `tool_result`   | `PostToolUse`      | `output` (replace what the model sees), `isError` (override the error flag), `terminate`                                           |
| `context`       | `{messages}`       | `messages` (replace the conversation for this request; Koine's message shape, not Pi's)                                            |
| `input`         | `UserPromptSubmit` | `prompt`                                                                                                                           |
| `session_start` | `SessionStart`     | –                                                                                                                                  |
| `agent_end`     | `Stop`             | –                                                                                                                                  |

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

| API                                                                                                                                                               | Status                                                                                                      |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `pi.registerTool({name, description, parameters, execute})`                                                                                                       | ✅ `execute(callId, params, signal, onUpdate, ctx)`; result `{content:[{type:"text",text}]}`                |
| `pi.registerCommand(name, {description, handler})`                                                                                                                | ✅ `ctx.ui.notify` output is returned to the user                                                           |
| `pi.on("tool_call")` → `{block, reason}`                                                                                                                          | ✅ in-place `event.input` changes apply and chain; a throw blocks the call                                  |
| `pi.on("tool_result")` → `{content, details, isError}`                                                                                                            | ✅ chained; text and image content                                                                          |
| `pi.on("input")` → `{action: "handled" \| "transform", text, images}`                                                                                             | ✅ chained; `source` is real                                                                                |
| `pi.on("before_agent_start")` → `{systemPrompt, message}`                                                                                                         | ✅ once per prompt; the prompt replaces the base prompt for the run; `systemPromptOptions` is read-only     |
| `pi.on("session_start" \| "turn_start" \| "turn_end" \| "agent_end")`                                                                                             | ✅ observe-only                                                                                             |
| `pi.on("session_before_*")` → `{cancel}`; `session_shutdown`, `session_compact`, `session_tree`, `tool_execution_*`                                               | ✅ (§2)                                                                                                     |
| `pi.sendUserMessage(text, {deliverAs})`, `pi.exec`, `pi.getActiveTools`                                                                                           | ✅ `deliverAs` `"steer"` queues for after the current tool calls, else a follow-up                          |
| `pi.appendEntry(customType, data)`                                                                                                                                | ✅ stored as a `custom` session entry; never sent to the model                                              |
| `pi.sendMessage`, `pi.getCommands`, `pi.events`, `ctx.executeTool`, `ctx.tools`                                                                                   | ✅ (REQ-EXT-009, 010)                                                                                       |
| `ctx.ui.select/confirm/input/editor/notify/setStatus/setWidget/setTitle/setWorkingMessage/setEditorText`, `ctx.mode`, `ctx.hasUI`, `ctx.signal`, `ctx.shutdown()` | ✅ dialogs answered by the frontend; `editor` is one line in the terminal UI; component widgets are ignored |
| `registerProvider`, `registerShortcut`, `registerMessageRenderer`, `ctx.ui.custom/setFooter/setHeader`, `@mariozechner/pi-tui` components                         | ⚠️ accepted, inert                                                                                          |
| Pi events not in §2 (e.g. `message_*`, `model_select`)                                                                                                            | ❌ never fired                                                                                              |
| `@mariozechner/pi-ai`: `Type`, `StringEnum`                                                                                                                       | ✅                                                                                                          |

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
`grep`, `find`, `ls`) matching Pi; hooks see Claude names (§4, `find` is `Glob`).

_Verified by:_ `jstest/shims.test.mjs` (8 tests, run under Node **and** Bun),
`app.TestEverythingWorksTogether`, `cmd/koine.TestEndToEndPrompt`,
`app.TestBrokenExtensionIsReportedNotFatal`.

## 6. Built-in tools

Seven tools, as in Pi (REQ-TOOL-002..005). `read`, `bash`, `edit` and `write` are offered by default; `grep`, `find` and `ls` are registered
and offered once `-t`, `+name` or the `defaultTools` setting names them. `skill`(name) and `task`(agent, prompt) are Koine's own.
`read` of a PNG, JPEG, GIF, WebP or BMP file (found by content, not name) attaches the image to the result (REQ-LLM-006).
Paths in every tool accept `~`, a leading `@`, unicode spaces, and (for `read`) macOS screenshot and curly-apostrophe spellings.
Every tool that returns a list or a file stops at **2000 lines or 50KB, whichever comes first**, and says what was cut and how to get the rest.

| Tool    | Arguments                                                              | Behaviour                                                                                                                                      |
| ------- | ---------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `read`  | `path`, `offset` (1-based line), `limit`                               | Plain text. Over the limit: `[Showing lines 1-2000 of N. Use offset=2001 to continue.]`. Errors for missing, binary, offset past the end.      |
| `write` | `path`, `content`                                                      | Creates parent directories. Held in the file's mutation queue.                                                                                 |
| `edit`  | `path`, `edits: [{oldText, newText}]`                                  | Exact, unique, non-overlapping matches against the original; fuzzy fallback; BOM and CRLF kept; all or nothing; the result has a unified diff. |
| `bash`  | `command`, `timeout` (seconds, default 120)                            | Tail of the output; the full output is in a temp file named in the result; the process group is killed on timeout or abort.                    |
| `grep`  | `pattern`, `path`, `glob`, `ignoreCase`, `literal`, `context`, `limit` | `path:line: text` (context `path-line- text`), 100 matches, 500-char lines. ripgrep when installed, otherwise a Go search.                     |
| `find`  | `pattern` (glob), `path`, `limit`                                      | Relative paths, directories end in `/`, 1000 results. `glob` is accepted as an alias for the name.                                             |
| `ls`    | `path`, `limit`                                                        | Sorted without regard to case, directories end in `/`, dotfiles included, 500 entries.                                                         |

`edit` also accepts a single top-level `oldText`/`newText` (or `old`/`new`); `replaceAll` no longer exists. `grep`, `find` and the Go search respect
`.gitignore` files in the tree and in the parent directories up to the repository root, include dotfiles and never enter `.git`.

**`bash` details.** The shell is `shellPath` (default `/bin/bash`, else `bash` or `sh` on the PATH), run as `<shell> -c "<shellCommandPrefix>\n<command>"`.
Both settings follow Pi's names and apply only from the user settings file or a trusted project. Output has terminal escapes stripped. While the
command runs, the tail of its output goes to the tool-update callback (`tools.Updates`, JSON mode `tool_execution_update`).

**Prompt metadata.** A tool may carry `promptSnippet` (one line for the `<tools>` section of the system prompt) and `promptGuidelines`
(bullets for `<rules>`, only while the tool is active). Extension tools registered through the sidecar or the protocol's `register_tool` pass both.
The default prompt is the preamble, `<tools>`, `<rules>`, `<project_context>`, the skills and `<environment>`; a `SYSTEM.md` or sub-agent prompt
replaces the preamble, `<tools>` and `<rules>`.

**User commands.** In an interactive frontend a line starting with `!` runs a shell command with the same shell settings and no timeout:
`!cmd` shows the output and adds it to the conversation as a user message ("Ran `cmd`" and the output), `!!cmd` only shows it. Print, JSON and
server runs do not do this (`App.NoUserBash`): the text is an ordinary prompt.

### MCP servers

Koine is an MCP client (REQ-MCP-001..003, `core/mcp`). Servers are third-party code: a stdio server is a spawned process, so the **project-level**
files below are neither read nor started until the folder is trusted (ADR 0002); the user-level files run.

**Configuration.** `{"mcpServers": {"<name>": {…}}}` in `<agent dir>/mcp.json` (user), `.koine/mcp.json` and `.pi/mcp.json` (project), Claude's
`.mcp.json` (project) and the `mcp` block of opencode's `opencode.json` (`local` becomes stdio and `remote` HTTP, `{env:NAME}` becomes `${NAME}`,
timeouts are milliseconds there). A later file replaces an entry of the same name; an entry with only `enabled`, `exposure` or `toolExposure`
changes just those fields of the earlier one. Names use letters, digits, `_` and `-`; names that differ only in `-` and `_` are the same server.
Invalid entries are reported and skipped.

| Field                      | Meaning                                                                                                                                 |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `type`                     | optional: `stdio`, `http` or `streamable-http`; `command` implies stdio, `url` implies HTTP; `sse` is rejected                          |
| `command`, `args`          | a program and its arguments (no shell string)                                                                                           |
| `env`, `cwd`               | environment added to the process; working directory (`~/` and relative paths resolve against the home and project)                      |
| `url`, `headers`           | streamable HTTP endpoint and extra headers                                                                                              |
| `oauth`                    | `clientId`, `clientSecret`, `callbackPort`, `callbackUrl`, `scope`, `clientName`, `clientRegistration`, `authServerMetadataUrl`         |
| `timeout`                  | seconds without an answer or progress notification (default 60)                                                                         |
| `enabled`                  | `false` keeps the entry without connecting                                                                                              |
| `exposure`, `toolExposure` | `direct` (default), `deferred`, `hidden`; per-tool overrides by name or `*` pattern, exact names first, then the first matching pattern |
| `description`              | one sentence for the system prompt (default: the first line of the server's instructions)                                               |

Values in `command`, `args`, `env`, `cwd`, `url` and `headers` may use `${VAR}` and `${VAR:-default}`; an unset variable without a default fails the
server. A value that starts with `!` is a command run with the platform shell whose output (trailing newlines removed) is the value, e.g.
`"Authorization": "!echo Bearer $(gh auth token)"`. `${…}` is not expanded inside a `!command`; the shell does that.

**Tools.** Each tool is registered as `mcp__<server>__<tool>`; characters other than letters, digits and `_` become `_`, and a collision or a name
over 64 characters gets `_` and six hex digits of a hash. A tool carries a one-line prompt snippet and, when the server has a description, a prompt
guideline. `direct` tools are offered to the model. `deferred` tools are not, until the `tool_search` tool (present while any tool is deferred) finds
them by keyword (`{"query", "limit"}`) and offers them from the next model call. `hidden` tools are not registered. Pi's default exposure
(`codemode`) is read as `deferred`, because Koine has no codemode; Koine's default is `direct`. `-t`, `-xt` and `defaultTools` patterns such as
`mcp__github__*` apply (a deferred tool is not matched until found). Tool output: text, images (sent as images when the caller takes them), a
result with only `structuredContent` shows that JSON, text over 20 KB (20480 characters) keeps its first and last 10240 characters around
`…N chars truncated…` and the full text is saved to a temporary file named at the end. A result with `isError` is a failed tool call.

**Resources.** While a connected server has resources, `list_mcp_resources` and `list_mcp_resource_templates` (`{"server"?}`) and
`read_mcp_resource` (`{"server", "uri"}`) are offered. Text comes back as text, images as images, other binaries as a temporary file path.
`ui://` resources are left out.

**Connections.** Servers connect in the background when a session starts; startup waits up to 10 seconds. HTTP: one `POST` per message (JSON or SSE
answer), `Mcp-Session-Id` and `MCP-Protocol-Version` headers, network errors and 408/429/5xx retried twice, `DELETE` on close. Stdio: closing
stdin, then SIGTERM, then SIGKILL of the process group; server stderr goes to `<agent dir>/mcp.log` (rotated to `mcp.log.1` past 5 MB). A dropped
connection stays visible and reconnects on the next call. Tool calls are never retried; listing and reading are retried once.

**OAuth.** A 401 from an HTTP server without an `Authorization` header starts discovery on `koine mcp login NAME`: protected resource metadata
(RFC 9728, from the challenge's `resource_metadata` or the well-known URL), authorization server metadata (RFC 8414, then OpenID configuration, or
`oauth.authServerMetadataUrl`), a configured `oauth.clientId` or dynamic registration, then the authorization code flow with PKCE S256 and the
`resource` parameter on `http://127.0.0.1:<port>/callback`. Tokens are stored in `<agent dir>/mcp-auth.json` (mode 0600) per server name and URL,
refreshed before they expire and after a 401; if that fails the server is "needs login". Endpoints must be https (http on loopback only).

**Commands.** `koine mcp add|remove|list|login|logout` (REQ-MCP-003; `koine mcp --help`) and, in a session, `/mcp` (state of each server) and
`/mcp reconnect <name>`.

_Verified by:_ `core/mcp` tests (fake stdio and HTTP servers, a fake authorization server), `core/app.TestMCP*`, `cmd/koine.TestMCP*`.

## 7. Slash commands

`/help /clear /exit /quit /tools /skills /agents /commands /mcp [reconnect <name>] /model [id[:level]] /thinking [level] /login [provider] /logout [provider]`, and in the terminal UI `/hotkeys /settings
/scoped-models /copy /tree`,
`/skill:<name> [args]`, extension commands (§3.3), then prompt templates (§1).
`!<command>` and `!!<command>` run a shell command (§6, user commands).
Anything else is a prompt to the model. Slash commands never reach the model
unless they expand to a prompt or skill.

## 8. Frontend contract

Every frontend is a thin consumer of `app.App.HandleInput(ctx, line, emit)`
and `Describe()`. `emit` receives `agent.Event`:

```json
{ "type": "text|thinking|tool_start|tool_end|notice|error|usage|done", "text": "", "tool": "", "callId": "", "args": {}, "isError": false }
```

- **Terminal UI** (REQ-TUI-001, `core/tui`): on a terminal `koine` runs a raw-mode UI in regular (scrollback) mode: finished transcript blocks
  are printed once and a live area (status line, editor or selector, completion popup, footer) is redrawn each frame. Without a terminal, or with
  `KOINE_LINE_MODE=1`, it falls back to the line loop.
  - **Fullscreen mode** (REQ-TUI-002): `tuiMode: "fullscreen"` (default `regular`, unlike Pi) runs on the alternate screen with mouse reporting. The
    transcript is a viewport above the editor and footer that follows new output until you scroll up. Keys: `tui.altScreen.pageUp`/`pageDown`
    (PageUp/PageDown), `halfPageUp`/`halfPageDown`, `lineUp`/`lineDown` (unbound), `top` (Ctrl+Home), `bottom` (Ctrl+End, follows output); an open
    popup or selector keeps the page keys. The wheel moves `fullscreenWheelScrollLines` (1-100; `auto` is one line on a local Mac, else fast spins
    speed up to 6; Alt+wheel is five times as far). `fullscreenScrollbar` is `auto` (shown when the transcript is longer than the view), `always` or
    `hidden`. On exit `fullscreenExitOutput` prints the transcript (`transcript`) or `Resume this session with: koine --session <id>`
    (`resume-hint`). Sending a message returns to the tail. A `dumb` terminal stays in regular mode. Not done: `fullscreenCopyOnSelect` (hold Shift to
    select with the terminal), prompt jumps and transcript search.
  - **Inline images** (REQ-TUI-003): images in tool results (the `read` tool) and in a resumed session's history are drawn with the kitty graphics
    protocol or iTerm2's inline image protocol. `terminal.images` is `kitty`, `iterm2`, `auto` (default; detects from `KITTY_WINDOW_ID`, `TERM`,
    `TERM_PROGRAM`, `LC_TERMINAL`, and never inside tmux or screen) or `false`. `terminal.showImages` (default true) and `terminal.imageWidthCells`
    (default 60) apply. Everything else shows `[image: image/png 640x480 12.3 KB]`. Kitty gets PNG (JPEG and GIF are re-encoded); iTerm2 gets PNG, JPEG
    and GIF; WebP and BMP are placeholders. Pasting or dragging an image into the editor is not done.
  - Editor: multiline (Shift+Enter, or `\` then Enter), word and line movement, kill ring and yank, undo, bracketed paste (pastes of more than a few
    lines show as `[paste #n +m lines]` and expand on submit), Up/Down prompt history, Ctrl+G external editor (`externalEditor`, `$VISUAL`,
    `$EDITOR`), Ctrl+Z suspend, Ctrl+T show/hide thinking, Ctrl+O expand/collapse tool output, Ctrl+L model selector, Ctrl+P / Ctrl+Shift+P cycle
    the model scope (`--models`, `enabledModels`), Esc interrupts, a second Esc follows `doubleEscapeAction`, Ctrl+C clears the editor, interrupts a
    running turn, or quits on a second press within a second.
  - Autocomplete: `/` commands (built-in, extension, prompt templates, skills), `@` file references (fuzzy), Tab path completion.
  - `keybindings.json` in the agent directory maps Pi's action ids to keys (`"ctrl+x"`, `["ctrl+x","alt+y"]`, `[]` to disable); a configured value
    replaces the defaults. Ids Koine has no action for are accepted and ignored. `/hotkeys` lists the active bindings.
  - Themes are JSON files with `vars` (hex, `oklch(...)`, 0-255, references, `""` for the terminal default) and `colors` using Pi's token names.
    `system`, `dark` and `light` are built in; others come from `<agent dir>/themes`, `.koine/themes` (trusted folders) and `--theme`. Missing
    tokens are filled from the built-in theme of the same appearance and unknown ones warn. `/reload` reloads them; they are not watched.
  - Settings acted on: `theme`, `quietStartup`, `editorPaddingX`, `outputPad`, `autocompleteMaxVisible`, `showHardwareCursor`, `externalEditor`,
    `doubleEscapeAction`, `terminal.{showTerminalProgress,hyperlinks,trueColor}`, `markdown.codeBlockIndent`. `/settings` edits them and writes the
    user `settings.json` (only when it is plain JSON). A project `externalEditor` is ignored in an untrusted folder.
  - Rendering, layout and key decoding are pure functions covered by unit tests; only `term_unix.go` touches a terminal (one pty test).
- **Bubble Tea and desktop frontends** (REQ-FE-002, REQ-FE-003, ADR 0013): `tui-bubbletea/` and `desktop/` (Wails v3, ADR 0015) are separate modules that start
  `koine --mode rpc` (binary from `$KOINE_BIN`, next to the program, or on `PATH`) and show its events through `core/rpcclient`. Arguments given to
  the Bubble Tea program are passed to koine (`-C DIR`, `--model`, ...); the desktop app uses `$KOINE_CWD` as the directory. Enter sends a prompt, and
  while the agent runs it steers (`streamingBehavior: steer`); Esc aborts; extension dialogs (`select`, `confirm`, `input`, `editor`) are answered in
  the UI. Commands are those of RPC mode (extension commands, prompt templates, skills), not the built-in slash commands of the terminal UI. Status:
  the shared client is tested against the real server and both modules are built and tested in CI; the Wails window itself is not exercised.
- **Command line** (`koine [options] [--] [@files] [message]`, REQ-CLI-001): with a message, piped stdin, `-p` or `--mode json` it runs one
  prompt. The prompt is piped stdin, then `@file` contents as `<file name="…">` blocks, then the message, separated by blank lines. An `@file` that is an image by its content, and image data
  piped on stdin, are sent as images instead (REQ-CLI-005, REQ-LLM-006); an unreadable image is a usage error like a missing file.
  Text mode prints only the text after the last tool call to stdout; tool names, thinking, notices and errors go to stderr. `--mode json` prints one
  record per line on stdout in Pi's JSON event stream (below). Exit codes: 0 ok, 1 failed (provider, tool-loop or unknown `--model`), 2 usage (bad option, missing
  `@file`, nothing to do). Without a prompt it starts the interactive session. `koine --help` lists the options, and those that extensions registered.
- **Tool, resource and model options** (REQ-CLI-002): `-t a,b|pat*` allows only those tools; `-t +name,-name` changes the default selection (plain
  names and `+`/`-` cannot be mixed); `-xt list` removes tools by name or pattern after everything else; `-nbt` drops the built-in tools and keeps
  extension tools; `-nt` offers none. `-e PATH`, `--skill PATH`, `--prompt-template PATH` add resources (repeatable; a file, or a directory);
  `-ne`, `-ns`, `-np`, `-nc` switch off discovered extensions, skills, prompt templates and `AGENTS.md`/`CLAUDE.md`; explicit paths still load.
  `--theme PATH` adds a theme file or directory, `--use-theme NAME` picks the theme, `--no-themes` skips discovered ones (§8, terminal UI). `--model` takes an id, `provider/id` or part of an id or name; `--provider` limits
  it to one provider; `--models p1,p2` or `enabledModels` give the scope, whose first model starts the session when no model is set (`--model`,
  `$KOINE_MODEL`, `defaultModel`); `--api-key` replaces the key for this run; `--list-models [search]` lists the catalog. Options registered by an
  extension (`pi.registerFlag`) are `--name` (boolean) or `--name VALUE` / `--name=VALUE` (string); other unknown options exit 2.
- **JSON event stream** (REQ-CLI-003): `--mode json` writes one record per line: `{"type":"session","version":3,"id","timestamp","cwd"}`, then
  `agent_start`, `turn_start`, `message_start`/`message_end` (user), the assistant `message_start`, `message_update` records
  (`assistantMessageEvent` `text_*`, `thinking_*` and `toolcall_*` with `start|delta|end`), `message_end`, `tool_execution_start`
  `{toolCallId,toolName,args}`, `tool_execution_end` `{toolCallId,toolName,result,isError,durationMs}`, the `toolResult` messages, `turn_end`
  `{message,toolResults}`, and finally `agent_end` `{messages,willRetry}` and `agent_settled` `{aborted}`. When the provider streams, the deltas arrive as they come (`contentIndex` counts the blocks shown, so it shifts when `hideThinkingBlock` is on); otherwise each block is one delta. `message_end` always carries the whole message. Also written,
  with Pi's fields: `tool_execution_update` `{toolCallId,toolName,args,partialResult}` (partial output of an extension tool), `queue_update`
  `{steering,followUp}`, `entry_appended` `{entry}` (the session-file line), `session_info_changed` `{name}` (absent when cleared),
  `thinking_level_changed` `{level}`, `compaction_start` `{reason}` and `compaction_end` `{reason,result:{summary,firstKeptEntryId,tokensBefore,
  estimatedTokensAfter},aborted,willRetry,errorMessage}` with reason `manual|threshold|overflow`, `auto_retry_start` `{attempt,maxAttempts,delayMs,
  errorMessage}`, `auto_retry_end` `{success,attempt,finalError}`, and `summarization_retry_scheduled` `{attempt,maxAttempts,delayMs,errorMessage}`,
  `summarization_retry_attempt_start` `{source,reason}` and `summarization_retry_finished`. Assistant messages have `api`, `thinkingLevel` and
  `usage.cost` (zeros when the model has no price). The same events (name, with the fields under `data`) reach the HTTP server's SSE stream. Shapes
  Pi's page leaves open follow the session file; see `compat/DIVERGENCES.md`.
- **Environment** (REQ-CLI-004): the CLI sets `AI_AGENT=koine` and `KOINE_CODING_AGENT=true`; the bash tool also gets `KOINE_SESSION_ID`,
  `KOINE_SESSION_FILE` (unset with `--no-session`), `KOINE_SESSION_PROVIDER`, `KOINE_SESSION_MODEL` and, while the model is asked to think, `KOINE_SESSION_REASONING_LEVEL`. The mapping of Pi's variables is in
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
  ignored in a project; an untrusted project applies only `model`, `maxTurns`, `enabledModels`, `compaction`, the thinking settings, the retry settings and the image settings (not the tree settings). Problems are listed at startup (`/reload` re-checks).
- **Agent directory and prompt files** (REQ-CFG-002): the user directory is `~/.koine`, or `$KOINE_AGENT_DIR`. `SYSTEM.md` (replaces the system prompt) and `APPEND_SYSTEM.md` (added after the context files) are read from a trusted project's `.koine/` or `.pi/`, else from the user directory or `~/.pi/agent`; the first non-blank file wins and files are never combined. `--system-prompt` and `--append-system-prompt` override them.
- **Images** (REQ-LLM-006): a message block `{"type":"image","source":{"type":"base64","media_type","data"}}` internally; in session files and `--mode json`
  Pi's `{"type":"image","data","mimeType"}` inside user and `toolResult` content. Sources: `@file` arguments, piped image data, and the `read` tool.
  Anthropic: `image` blocks, and a tool result with images becomes a content array of `text` then `image` parts. OpenAI-compatible: `image_url` parts with
  `data:<type>;base64,…`; the images of a tool result follow as a user message headed `Attached image(s) from tool result:`. Limits with
  `images.autoResize` (default true): at most 2000 by 2000 pixels (Pi's documented value) and 4.5 MB encoded (Koine's own; Pi's default was not found);
  files over 25 MiB are refused. WebP and BMP are sent unchanged. `images.blockImages` (default false) keeps all images from providers. The catalog's
  `input` list (`["text","image"]`, Pi's field) gates by model; unlisted means allowed. Nothing is verified against a real provider: the wire shapes are
  tested with fake servers only. `terminal.showImages`, `terminal.imageWidthCells` and `terminal.images` control the terminal's inline display (REQ-TUI-003).
- **Compaction** (REQ-CMP-001): `/compact [focus]`, automatic before a turn would overflow, and once on a provider "prompt too long" error.
  Config (`settings.json`): `compaction.enabled`, `contextWindow`, `reserveTokens`, `keepRecentTokens`.
- **Usage and cost** (REQ-LLM-004): the model catalog is built in; `~/.koine/model-catalog.json` (`{"models":[{"id","contextWindow","maxOutput","cost":{"input","output","cacheRead","cacheWrite"},"longContext":{"threshold","multiplier"}}]}`, USD per million tokens) adds or replaces entries by id (`koine update --models`, REQ-LLM-016, refreshes it from a server). Each response emits an event `{"type":"usage","usage":{input,output,cacheRead,cacheWrite,cost?,contextTokens,contextWindow,contextPercent}}`; `cost` is absent when the model has no known price. Assistant messages in the session file carry Pi's `usage` object. `/session` prints totals, cost and context use. `compaction.contextWindow` overrides the catalog's window.
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
  block of the last user message. Cache reads and writes arrive in `Usage` (`cacheRead`, `cacheWrite`). `KOINE_CACHE_RETENTION=long` adds `ttl: "1h"` on `api.anthropic.com` and
  `prompt_cache_retention: "24h"` on `api.openai.com`; OpenAI and Azure OpenAI also get `prompt_cache_key` (the session id). The global-only setting
  `cacheWarming` (`off`, `streaming`, `idle`; default `idle`) replays the last request shortly before the cache of a model with `promptCache`
  (`{"short":300,"long":3600}` seconds) lapses and cancels it at the first token, when that saves at least $0.05 (REQ-LLM-013).
- **Changing model mid-session** (REQ-LLM-012): each request is converted from a copy of the history for the target provider: thinking of other models
  becomes text (redacted thinking is dropped), tool call ids are rewritten to the target's rules, and unmatched tool calls and results are repaired.
  The session file is not changed.
- **Amazon Bedrock** (REQ-LLM-014): provider `amazon-bedrock`, `api: "bedrock-converse-stream"`. Requests are signed with SigV4 or carry
  `AWS_BEARER_TOKEN_BEDROCK`; credentials come from `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_SESSION_TOKEN` or a static-key profile in
  `~/.aws`; region from `AWS_REGION`, `AWS_DEFAULT_REGION` or the profile (else `us-east-1`); endpoint from `AWS_ENDPOINT_URL_BEDROCK_RUNTIME` or
  `baseUrl`. `AWS_BEDROCK_SKIP_AUTH=1` and `AWS_BEDROCK_FORCE_CACHE=1` are acted on. REQ-LLM-017: a profile may be an SSO login (the token cached by
  `aws sso login` is used, never refreshed), a role (`role_arn` + `source_profile` or `credential_source = Environment`, chains allowed) or a
  web identity (`web_identity_token_file`, or `AWS_WEB_IDENTITY_TOKEN_FILE` + `AWS_ROLE_ARN`); `AWS_ENDPOINT_URL_STS` and `AWS_ENDPOINT_URL_SSO`
  move the services. `credential_process`, MFA and instance credentials are not supported.
- **Google Vertex AI** (REQ-LLM-015): provider `google-vertex`, `api: "google-vertex"`. `GOOGLE_CLOUD_API_KEY` uses express mode; otherwise
  Application Default Credentials (`GOOGLE_APPLICATION_CREDENTIALS` or the gcloud login file, `service_account` or `authorized_user`) with
  `GOOGLE_CLOUD_PROJECT` and `GOOGLE_CLOUD_LOCATION`. The metadata server is not read.
- **Retry and timeouts** (REQ-LLM-002, REQ-LLM-005): network errors, timeouts, 429 and 5xx are retried. `retry.maxRetries` (default 3, so four attempts;
  0 or `retry.enabled: false` turns retries off), `retry.baseDelayMs` (2000, doubled each time) and `retry.maxAgentDelayMs` (60000, the longest backoff).
  A `Retry-After` header is waited for, but one longer than `retry.provider.maxRetryDelayMs` (60000; 0 = no limit) ends the retries with an error.
  A 429 that reports an exhausted quota or credit is not retried. `retry.provider.timeoutMs`, else `httpIdleTimeoutMs` (a number, 0 or `"disabled"`),
  is how long a streamed response may be silent (default 300000 as in Pi; 0 or `"disabled"` turns it off). A response that is not streamed is bounded by
  them only when one is set. `retry.provider.maxRetries` is accepted and ignored. Each retry emits a `notice`; a stream is retried only before its
  first byte.
  A provider answer that does not fit the context window (HTTP 413, or the provider's own wording) triggers one compaction and one retry per prompt.
- **Streaming** (REQ-LLM-003): providers that can stream (Anthropic, OpenAI-compatible) request server-sent events; a server that answers with plain
  JSON still works. While a response arrives the agent emits `{"type":"text_delta","text":…}`, `{"type":"thinking_delta","text":…}` (not when
  `hideThinkingBlock` is set) and `{"type":"tool_delta","tool":NAME,"callId":ID,"text":FRAGMENT}`; the first `tool_delta` of a call has no text and
  announces it, later ones carry argument JSON. When the response is complete the unchanged `text` and `thinking` events carry the whole blocks, so a
  frontend should render either the deltas or the final blocks of a response, not both. `tool_start` and `tool_end` still mark the execution of a
  call, not its generation. Tested against fake servers only, not live APIs.
- **Queues and loop events** (REQ-AGT-001): `Agent.Steer(text)` and `Agent.FollowUp(text)` (or `POST /api/steer` and `/api/follow-up` with
  `{"text":…}`, answered `202` with `{"steering":[…],"followUp":[…]}`) queue messages typed during a run. `steeringMode` and `followUpMode`
  (`one-at-a-time`, the default, or `all`) say whether each delivery point hands over one message or all of them. Steering is delivered after the
  assistant turn and its tool calls, before the next request, and also ahead of the first request if it was queued before the run started;
  follow-ups only when the agent has no tool calls and no steering message left. Queued text is not passed through the `input` hook. Events:
  `{"type":"queue_update","data":{"steering":[…],"followUp":[…]}}` (the whole queues, always arrays) and `{"type":"user_message","text":…}` when a queued
  message enters the conversation. `Agent.ClearQueues()` empties both and returns the text.
- **Tool execution** (REQ-AGT-001): calls from one assistant message are checked in order and run concurrently; `tool_start` appears in call order, `tool_end`
  in completion order, results reach the model in call order. `Agent.ToolExecution = "sequential"` or a tool whose `ExecutionMode()` is `"sequential"`
  runs the whole batch one call at a time; `write` and `edit` run in parallel, serialized per file by the mutation queue. A tool that panics returns an error result.
- **Abort** (REQ-AGT-001): cancelling the run's context ends it with that error after closing off the transcript: text received so far is kept as an
  assistant message with `stopReason` `aborted` (saved in the session file, not sent to providers again), every `tool_use` has a `tool_result` (`aborted`
  for calls that did not run), and an `{"type":"aborted"}` event is emitted. Queued messages stay queued. A history with unanswered `tool_use` blocks
  (a crash) is repaired with synthetic error results whenever it is sent.
- **Per-request hooks** (REQ-AGT-001): `Agent.TransformContext(ctx, msgs)` and the `context` event rewrite the messages of one request (never the
  history); `Agent.PrepareNextTurn(ctx, TurnInfo)` is called before every request and may return a new model and thinking level, which then stay in
  force. Pi does not document `prepareNextTurn`, so this behaviour is Koine's own.
- **HTTP/SSE** (`koine --serve ADDR`): `GET /api/info` → `app.Info`;
  `POST /api/prompt {"text": …}` → `text/event-stream` of events then `done`;
  `409` if a run is in progress, `400` on a bad body; client disconnect cancels the run. `POST /api/steer` and `POST /api/follow-up` queue a message.
  This is the integration point for an Electron UI.
- **Wails v3** (`desktop/`): the `main.Service` calls `Info()`, `Send(text)`, `Cancel()`, `Answer(value, yes)` and `Dismiss()`, invoked by name from the page
  (`Call.ByName("main.Service.Send", text)`); one `koine:update` event per change carries the transcript entries that changed.
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

### 9.1 Provider credentials and `models.json`

A provider's key is the first of: `--api-key` (provider of the first model, this run), its entry in `~/.koine/auth.json`
(`{"<provider>":{"type":"api_key","key":"…","env":{…}}}`, mode 0600, `env` wins over the process environment), the provider's `apiKey` in
`models.json`, then its environment variables, which are Pi's: `ANTHROPIC_API_KEY` (or `ANTHROPIC_AUTH_TOKEN`, sent as a bearer token),
`OPENAI_API_KEY`, `AZURE_OPENAI_API_KEY`, `GEMINI_API_KEY`, `MISTRAL_API_KEY`, `GROQ_API_KEY`, `CEREBRAS_API_KEY`, `XAI_API_KEY`,
`OPENROUTER_API_KEY`, `DEEPSEEK_API_KEY`, `TOGETHER_API_KEY`, `FIREWORKS_API_KEY`, `HF_TOKEN`, `NVIDIA_API_KEY`, `MOONSHOT_API_KEY`,
`AI_GATEWAY_API_KEY`, `ZAI_API_KEY`. Azure also reads `AZURE_OPENAI_BASE_URL`, `AZURE_OPENAI_RESOURCE_NAME`, `AZURE_OPENAI_API_VERSION`
and `AZURE_OPENAI_DEPLOYMENT_NAME_MAP`. Ollama, LM Studio, vLLM, SGLang and llama.cpp need no key. A value of a key, `apiKey` or header is
a literal, `!command` (trimmed output, 10 s limit), or a template with `$NAME`/`${NAME}` (`$$` and `$!` are literals).

`models.json` (agent directory; a trusted project's `.koine/models.json` is read after it) follows Pi's schema: `providers.<id>` with `name`,
`baseUrl`, `api` (`anthropic-messages`, `openai-completions`, `openai-responses`, `azure-openai-responses`, `google-generative-ai`),
`apiKey`, `headers`, `authHeader`, `compat`, `models[]` and `modelOverrides`. Keys Koine does not act on are reported at startup.

`koine auth login|logout|check|print-api-key|print-bearer-token` (exit codes of `check`: 0 ready, 1 not ready, 2 invalid). `check --json`
prints `{"status":"ready|not_ready|invalid","provider":…,"source":…,"reason":…,"credentials":…}`; this shape is Koine's. Account sign-in: `koine auth login <provider>` (or `koine login`, `/login`) signs in to `anthropic`, `openai-codex`, `github-copilot` and `openrouter` with PKCE and a loopback callback or a device code (`--no-browser`: pasted code for Claude, device code for ChatGPT; `--api-key` stores a key instead); logins are kept in `auth.json` as `{"type":"oauth","access","refresh","expires"}` and renewed when they expire (`check --no-refresh` reports instead; `print-bearer-token [--min-expiry D]` prints the access token). Not implemented: OAuth for xAI, Kimi, Meta and Google Gemini CLI,
AWS `credential_process`, MFA and instance credentials, metadata-server credentials. Wire formats of the Responses, Azure, Gemini, Bedrock and Vertex providers are
tested against fake servers only; the live Bedrock and Vertex APIs were not exercised.

## 10. Known limits (v1)

- Providers: Anthropic, OpenAI-compatible Chat Completions, OpenAI Responses, Azure OpenAI, Gemini, Amazon Bedrock, Google Vertex AI, and the built-in and custom providers of §9.1; streamed, retried as configured (four attempts by default). No OAuth. Wire formats are checked against fake servers only. No frontend renders streamed deltas yet.
- Images: WebP and BMP are not resized; the HTTP server and the line-mode TUI take text only (no image paste; inline display is REQ-TUI-003).
- Upstream tracking: see `docs/UPSTREAM.md`; unsupported Pi surface is listed by `koine -compat`.
- No permission prompts yet.
- The `bubbletea` and `wails` frontends are written against the same API but are
  separate Go modules that need network access for `go mod tidy`; they are not
  built or tested by `task check`.
- Pi/opencode surface not listed as ✅ in §5 is not supported.

## 11. Pi packages (REQ-PKG-001..003)

```text
koine install [-l] [--allow-scripts] [-a] <source>   add a package (user scope, or project scope with -l)
koine remove  [-l] <source>                          remove it and its files
koine update  [-l] [--allow-scripts] [-a] [source]   update packages that are not pinned (all, or one)
koine update --models [--url URL] [--force]          refresh ~/.koine/model-catalog.json (REQ-LLM-016)
koine list    [-l]                                   show configured packages with their resources
koine config  [-l] [enable|disable <source> <resource>]   list resources, or toggle one (a numbered toggle loop on a terminal)
```

Options: `-l`/`--local` selects the project scope, `-a`/`--trust` trusts the folder for this command, `-C`/`--cwd DIR` sets the project folder. The
subcommand must come first. Exit status is 0, 1 for a failure and 2 for a usage error.

**Sources.** `npm:name`, `npm:@scope/name@1.2.3`; `git:github.com/user/repo@v1`, `https://host/user/repo.git@<ref>`, `ssh://…`, `git@host:user/repo`,
`file://…`; a local path starting with `./`, `../`, `~/` or `/`. A version, tag or commit makes the package pinned (`update` leaves it alone).
`ext::` and other remote-helper URLs are refused.

**Settings.** `packages` in `settings.json` (user: `~/.koine`, project: `.koine/`) is an array of source strings or objects
`{"source": …, "extensions": […], "skills": […], "prompts": […], "themes": […], "autoload": false}`. A missing list loads everything of that kind and
`[]` loads nothing. In a list, `glob` includes, `!glob` excludes, `+path` adds a path back and `-path` removes one (the last word wins); a list of
only exclusions starts from everything. Relative local paths resolve from the directory of the settings file. A project entry replaces a user entry for
the same package (same npm name, same repository, same path); `autoload: false` on it narrows the user's entry instead. Project packages load only
in a trusted folder. `npmCommand` (user settings only) names the program used instead of `npm`; `allowInstallScripts` (user settings only, default
false) lets `npm install` run package scripts; `collapseChangelog` prints one line per package on update. Koine-only: `allowInstallScripts`.
The `.pi/settings.json` `packages` array is not read.

**Files.** User: `~/.koine/packages/`, `~/.koine/packages.lock.json`. Project: `.koine/packages/` (ignored by git), `.koine/packages.lock.json`
(commit it). `packages.lock.json` is `{"version":1,"packages":{<identity>:{source, kind, version, commit, resolved, registryIntegrity, integrity}}}`;
`integrity` is `sha256-<hex>` over the package's file names, executable bits and contents without `.git` and `node_modules`.

**Package contents.** The `pi` key of `package.json` lists `extensions`, `skills`, `prompts` and `themes` (paths and globs relative to the package,
`!` excludes, nothing may leave the package); without such lists the directories `extensions/`, `skills/`, `prompts/` and `themes/` are used.
`pi.changelogPath` names the changelog (default `CHANGELOG.md`; `""` turns it off). Host packages (`@earendil-works/pi-*`, `typebox`, and the older
`@mariozechner/pi-*`) belong in `peerDependencies` with `"*"`; one in `dependencies` gives a warning on install and load.

**Safety.** `npm install` runs with `--ignore-scripts` unless `--allow-scripts` or `allowInstallScripts` says otherwise. Git clones refuse remote
helpers and prompts. Install, remove, update and config of project packages need trust (exit 1 with a message otherwise). An install or update that
would put different bytes under a locked version or commit is refused; a package whose files no longer match the lock is not loaded. Koine does not
install missing packages at startup: it warns and names `koine update`.

**Provenance.** `/packages` lists the loaded packages (source, scope, resource counts); `/sources [filter]` lists tools, commands, skills and prompts
with their `SourceInfo` (`path`, `source`, `scope`, `origin`, `baseDir`).

_Verified by:_ `pkgs.TestInstallNPMPinsRecordsAndLocks`, `pkgs.TestIntegrityMismatchIsRefusedAndNotRecorded`, `pkgs.TestFilterSemantics`,
`app.TestPackageResourcesFeedTheLoaders`, `app.TestProjectPackagesStayBehindTheTrustGate`, `app.TestSourceInfoIsAttachedToTheLoadedThings`,
`koine.TestUpdateShowsTheChangelogOfUpdatedPackages`. Filter precedence, `autoload` and the manifest fallback are Koine's reading of Pi's guide.

## 12. RPC mode (REQ-RPC-001)

`koine --mode rpc` serves Pi's RPC protocol (https://pi.dev/docs/latest/rpc and `rpc-extension-ui`) on standard input and output. Input is strict JSONL
(records end at LF only). Stdout holds only: one `response` per command, the events of the JSON event stream (§8, `agent_start` … `agent_settled`,
`extension_error`, `custom_message` as a `message_start`/`message_end` pair) and `extension_ui_request` lines. Notices, errors and warnings go to stderr.
`@file` arguments are refused (exit 2). A response is `{"type":"response","id"?,"command","success":true,"data"?}` or `… "success":false,"error"}`. A
line that is not JSON gets `{"type":"response","command":"parse","success":false,"error":"Failed to parse command: …"}`.

| Command                                                                                                      | Answer (`data`)                                                                            |
| ------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------ |
| `prompt` `{message, images?, streamingBehavior?}`                                                            | `{disposition: started\|queued\|handled}`; a busy agent needs `steer` or `followUp`        |
| `steer`, `follow_up` `{message}`                                                                             | `{disposition: "queued"}`; an extension command is refused                                 |
| `abort`, `abort_bash`, `clear_queue`                                                                         | nothing; `{steering, followUp}`                                                            |
| `new_session`, `switch_session` `{sessionPath}`, `clone`, `fork` `{entryId}`                                 | `{cancelled}` (and `text` for `fork`)                                                      |
| `compact` `{customInstructions?}`                                                                            | `{summary, firstKeptEntryId, tokensBefore, estimatedTokensAfter}`                          |
| `bash` `{command, excludeFromContext?}`                                                                      | `{output, exitCode, cancelled, truncated, fullOutputPath?}`; `bash_execution_update` lines |
| `get_state`, `get_messages`, `get_commands`, `get_session_stats`, `get_last_assistant_text`                  | see Pi                                                                                     |
| `get_entries` `{since?}`, `get_tree`, `get_fork_messages`, `export_html` `{outputPath?}`, `set_session_name` | entries, tree, user messages, `{path}`                                                     |
| `set_model`, `cycle_model`, `get_available_models`                                                           | model objects                                                                              |
| `set_thinking_level`, `cycle_thinking_level`, `get_available_thinking_levels`                                | `{level}`, `{levels}`                                                                      |
| `set_steering_mode`, `set_follow_up_mode` `{mode: all\|one-at-a-time}`, `set_auto_compaction` `{enabled}`    | nothing                                                                                    |
| `set_auto_retry`, `abort_retry`                                                                              | `success: false`: Koine always retries transient errors                                    |

A `prompt` is answered when its `input` event is settled, not when the run ends. An extension command sent with `prompt` runs at once, even
while the agent streams, and answers `handled`. The server exits 0 at the end of standard input or when an extension calls `ctx.shutdown()`, after
the commands in flight finished.

**Extension UI sub-protocol.** `ctx.ui.select`, `confirm`, `input` and `editor` become `{"type":"extension_ui_request","id":"ui-N","method","title",…}` and wait for
`{"type":"extension_ui_response","id","value"\|"confirmed"\|"cancelled":true}`. A `timeout` (ms) or the extension giving up resolves the dialog as cancelled.
`notify`, `setStatus` (`statusKey`, `statusText`), `setWidget` (`widgetKey`, `widgetLines`, `widgetPlacement`), `setTitle` (`title`) and
`set_editor_text` (`text`) are requests that expect no answer. `setWorkingMessage`, footers, headers and components are not sent. Stray responses
are dropped. `ctx.mode` is `rpc` and `ctx.hasUI` is true.

Koine-specific or undocumented by Pi: the ids `ui-N`; `get_state` leaves out `sessionFile` and `sessionName` when there is none; the exact fields of
`get_session_stats` and `get_tree` follow the session format; `set_auto_retry` and `abort_retry` fail. None of the shapes was compared with Pi's source.

_Verified by:_ `koine.TestRPCAnswersEveryCommandWithOneResponse`, `koine.TestRPCPromptRunsTheAgentAndSessionCommandsReadTheResult`,
`koine.TestRPCQueuesMessagesWhileTheAgentRuns`, `koine.TestRPCExtensionDialogsAndShutdown`, `koine.TestRPCModeRefusesFileArguments`.
