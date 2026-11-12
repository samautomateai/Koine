# Requirements

Living document. Every requirement has a stable ID, a status, and, once implemented, the tests that prove it.
`scripts/docscheck.mjs` (run by `task docs:check` and CI) rejects: unknown statuses, an `implemented` requirement with no
tests, a named test that does not exist, a missing design reference, and duplicate IDs.

**Status values:** `planned` (agreed, not built) · `implemented` (built, tests listed) · `deprecated` (kept for history).
**Test references:** a Go test function name, or `js:<text>` matching the start of a JS test title. Separate with `;`.
**Changing behaviour?** Update the requirement first, or add a new one. See `docs/ENGINEERING.md` §1.

## Extension and plugin compatibility

### REQ-DISC-001 — Discover skills, agents and commands across ecosystems

- Status: implemented
- Statement: Koine finds skills, sub-agents and prompt/commands in Koine, Pi, opencode, Claude and `.agents` locations, so existing assets work unchanged.
- Design: DES-001
- Tests: TestDiscoverAcrossEcosystems; TestDiscover; TestEverythingWorksTogether; TestAgentsSkillsWalkUpToHome

### REQ-EXT-001 — Language-agnostic extension protocol

- Status: implemented
- Statement: An extension in any language that speaks newline-delimited JSON-RPC 2.0 can register tools and commands and receive events. A crashing or broken extension is reported, never fatal.
- Design: DES-002; DES-003
- Tests: TestProtocolRoundTrip; TestExtensionCrashSurfacesAsError; TestBrokenExtensionIsReportedNotFatal; TestContextCancelAbortsCall

### REQ-EXT-002 — Run Pi extensions

- Status: implemented
- Statement: Pi TypeScript/JavaScript extensions load through the JS sidecar and receive Pi events and the `pi.*` API.
- Design: DES-004
- Tests: js:runner: Pi extension; js:conformance: verified events reach Pi handlers

### REQ-EXT-003 — Run opencode plugins

- Status: implemented
- Statement: opencode plugins load through the same sidecar, including tool definitions and `tool.execute.before/after` hooks.
- Design: DES-004
- Tests: js:runner: opencode plugin

### REQ-EXT-004 — Degrade gracefully on unsupported APIs

- Status: implemented
- Statement: Pi APIs Koine does not implement resolve to harmless stand-ins and are listed by `/compat`; an extension using a future API never crashes the host.
- Design: DES-004; DES-006
- Tests: js:conformance: unknown future API never crashes; TestReloadModelAndCompat

### REQ-HOOK-001 — Claude-compatible command hooks

- Status: implemented
- Statement: Hooks declared in Claude-format settings run as commands; exit code 2 blocks the action with stderr as the reason; JSON on stdout can rewrite arguments.
- Design: DES-002
- Tests: TestExit2Blocks; TestJSONDecisions; TestNestedClaudeFormAndStdin; TestClaudeSettingsOnlyContributeHooks

## Safety

### REQ-TRUST-001 — Project code is off until the folder is trusted

- Status: implemented
- Statement: Extensions, hooks and providers that come from the project directory do not run until the folder is trusted (`/trust`, `-trust`, or `KOINE_TRUST=1`). Harmless scalars still apply. The user is told what was skipped.
- Design: DES-005
- Tests: TestUntrustedProjectCodeDoesNotRun; TestDiscoverExtensionsUntrustedSkipsProjectCode; TestScopedConfigAndCodeCount; TestStoreTrustAndAncestors

### REQ-TRUST-002 — Trust persists and takes effect immediately

- Status: implemented
- Statement: `/trust` records the folder in a user-private file and reloads without restarting.
- Design: DES-005
- Tests: TestTrustCommandPersistsAndReloads; TestFromEnv

### REQ-SKILL-001 — Refuse skills hiding instructions in invisible text

- Status: implemented
- Statement: Skills containing hidden Unicode control or tag characters are not loaded, and the user sees why.
- Design: DES-005
- Tests: TestSkillWithHiddenTextIsRefused; TestAudit; TestDiscoverReportSkipsHiddenText

## Models

### REQ-LLM-001 — Route models to providers without guessing

- Status: implemented
- Statement: A model id resolves to a provider by known name or explicit `prefix/model`; an unrecognised id is an error, never a silent send to the wrong vendor.
- Design: DES-007
- Tests: TestRouterResolve; TestRouterRejectsInsteadOfGuessing; TestOpenAIRoundTrip

### REQ-LLM-002 — Retry transient provider failures only

- Status: implemented
- Statement: Network errors, timeouts, 429 and 5xx are retried, by default four attempts with waits of 2, 4 and 8 seconds; other 4xx are not, and neither is a 429 that reports an exhausted quota or credit. A `Retry-After` is waited for, unless it is longer than allowed. `retry.enabled`, `retry.maxRetries`, `retry.baseDelayMs`, `retry.maxAgentDelayMs`, `retry.provider.timeoutMs`, `retry.provider.maxRetryDelayMs` and `httpIdleTimeoutMs` tune this (REQ-LLM-005), and each retry is reported as a notice. A stream is retried only until its first byte (REQ-LLM-003).
- Design: DES-007; DES-016
- Tests: TestRetriesOn429ThenSucceeds; TestClientErrorIsNotRetried; TestOpenAIErrors; TestRetrySettingsControlAttempts; TestRetryAfterIsHonouredUpToTheLimit; TestQuotaErrorsAreNotRetried; TestRetryNoticesAndCancellation

### REQ-LLM-003 — Streaming responses

- Status: implemented
- Statement: The Anthropic and OpenAI-compatible providers stream: a request asks for server-sent events, and the agent receives text, thinking and tool-call argument increments as they arrive and still gets the complete response at the end. A provider that cannot stream, and a server that answers a streaming request with plain JSON, keep working. A stream may be silent for `retry.provider.timeoutMs`, else `httpIdleTimeoutMs` (default 300000 as in Pi; 0 or `"disabled"` turns it off) before the call fails; a response that is not streamed is bounded by those settings only when they are set. Failed attempts are retried only before the first byte. Rendering the increments is up to each frontend, and none does yet (gap rows LLM-02, LLM-32, AGT-01). Verified against fake servers only, not live APIs.
- Design: DES-021
- Tests: TestAnthropicStreamBuildsTheResponseAndReportsDeltas; TestAnthropicStreamErrorsAndPartialContent; TestOpenAIStreamBuildsTextThinkingAndToolCalls; TestOpenAIStreamErrors; TestStreamFallsBackToJSONBodiesAndToCompleteOnlyProviders; TestStreamOpeningIsRetriedAndHTTPErrorsSurface; TestStreamIdleTimeoutAppliesToSilenceNotToTotalTime; TestStreamAbortKeepsPartialTextAndDoesNotCallItAnIdleTimeout; TestReadSSEHandlesCRLFMultilineDataAndTrailingEventsWithoutBlankLine; TestStreamingProviderEmitsDeltasAndStillEmitsTheFinalBlocks; TestAppStreamsThroughTheRouter; TestRetryAndTimeoutSettingsMapToThePolicy

## Agent and frontends

### REQ-AGENT-001 — Tool-using agent loop with sub-agents

- Status: implemented
- Statement: The agent alternates model turns and tool calls until done, honours a turn limit and cancellation, and can delegate to named sub-agents through a `task` tool.
- Design: DES-001
- Tests: TestLoopRunsToolsAndStops; TestTaskToolSubAgents; TestMaxTurnsProviderErrorAndCancel

### REQ-AGT-001 — Agent loop parity with Pi: queues, parallel tools, hooks and abort

- Status: implemented
- Statement: While a run is in progress, `Agent.Steer` queues a message for delivery after the current assistant turn and its tool calls, before the next model request, and `Agent.FollowUp` queues one for when the agent has no more tool calls and no steering message left; each is delivered as a user message with `user_message` and `queue_update` events, one per delivery point (`one-at-a-time`, Pi's default) or all together (`all`), as `steeringMode` and `followUpMode` say. `POST /api/steer` and `/api/follow-up` queue over HTTP. The tool calls of one assistant message are checked in order (the `tool_call` hook may block or rewrite them), then the allowed ones run concurrently; `tool_start` is announced in source order, `tool_end` as each finishes, and the results go to the model in source order. A tool can ask for `sequential` execution for its whole batch (`ExecutionModer`); `write` and `edit` need not, because the per-file mutation queue serializes them. The hooks may block a call, replace the arguments, replace the output, override the error flag and ask to `terminate`; the loop stops after a batch only when every result terminates. Cancelling a run keeps the transcript valid: text received so far is kept as an assistant message with `stopReason` `aborted` that is saved but never sent again, every `tool_use` gets a `tool_result` (calls not run say `aborted`), an `aborted` event is emitted and the context error is returned; calls left without a result by a crash are answered when the history is sent. `Agent.TransformContext`, the `context` event and `Agent.PrepareNextTurn` change the messages, model and thinking level per request. Gap rows AGT-02..07, CLI-27, EXT-14, EXT-21, TOOL-11. Pi does not document `prepareNextTurn`, the shape of its aborted message or `steeringMode: all` beyond one line each, so those follow our reading (compat/DIVERGENCES.md).
- Design: DES-021
- Tests: TestRunDeliversQueuedMessagesAsTheLoopGoes; TestQueueModesAll; TestFollowUpsWaitForSteeringAndQueuedTextIsExpanded; TestSteerReportsQueueChangesOfTheRunInProgress; TestToolsRunConcurrentlyAndResultsKeepSourceOrder; TestSequentialModesRunOneAtATime; TestPreflightIsSequentialAndBlockedCallsDoNotRun; TestTerminateStopsTheLoopOnlyWhenEveryResultAsksForIt; TestToolResultHookCanFlipTheErrorFlagAndPanicsAreErrors; TestDecisionMergeCarriesTheNewFields; TestEventsAreSerializedWhileToolsRunInParallel; TestAbortDuringTheResponseKeepsAnAbortedMessageThatIsNotSentAgain; TestAbortDuringToolsAnswersEveryToolCall; TestRequestMessagesAnswersOrphanedToolCalls; TestContextEventTransformContextAndPrepareNextTurn; TestQueueModeSettingsReachTheAgent; TestSteerAndFollowUpQueueMessagesForTheAgent; TestAbortedAssistantMessageIsSavedWithPisStopReasonAndRestored; js:loop:

### REQ-FE-001 — One core, many frontends over HTTP/SSE

- Status: implemented
- Statement: The core serves prompts as server-sent events so a desktop shell or browser can drive the same agent as the terminal, one run at a time.
- Design: DES-008
- Tests: TestPromptStreamsSSE; TestConcurrentRunRejected; TestServeMode; TestPromptRejectsBadBody

### REQ-FE-002 — Bubble Tea TUI and Wails desktop app build and are tested in CI

- Status: implemented
- Statement: The Bubble Tea TUI (`tui-bubbletea/`) and the Wails desktop app (`desktop/`) compile with their pinned dependencies, have committed `go.sum` files and pass their own tests in the blocking CI job `frontends`, which installs GTK 3 and WebKitGTK 4.1 for Wails, requires `go mod tidy -diff` to be clean, builds, vets and runs the tests with the race detector, and gates coverage of the package that holds the logic (80% for the TUI, whose `main` is process wiring; 85% for `desktop/bridge`). Both are RPC clients of the core (REQ-FE-003). Not covered: nothing renders the Wails window in CI, so the page (`desktop/frontend/index.html`) and the real window are untested, and the Bubble Tea program has not been run on a terminal against a real model.
- Design: DES-008; DES-075
- Tests: TestEnterSendsAPromptAndSteersWhileBusy; TestEventsRenderIntoTheViewAndEscAborts; TestDialogsAreAnsweredFromTheEditor; TestErrorsAndExit; TestRenderEntry; TestKeysViewAndDialogHelpers; TestEventsBecomeUpdatesForThePage; TestSendSteersWhileBusyAndInfoReadsTheServer; TestFailuresAreShownAndDialogsAnswered
- Gap rows: CLI-09, TUI-01

### REQ-FE-003 — Frontends drive the core through RPC mode

- Status: implemented
- Statement: `core/rpcclient` is a standard-library client for `koine --mode rpc`: it starts the binary (`KOINE_BIN`, next to the program, or `PATH`), sends commands with ids and returns their responses (`Prompt`, `Steer`, `FollowUp`, `Abort`, `GetState`, `GetCommands`, `RespondUI`), delivers events in order together with the server's standard error lines as `stderr` events, and fails pending commands when the server ends. `Transcript` folds events into entries (user, assistant, thinking, tool, notice, error), the busy flag, extension dialogs, statuses, widgets and the title. The Bubble Tea and Wails frontends use only this package from the core and no core internals (ADR 0009).
- Design: DES-075
- Tests: TestClientCorrelatesResponsesAndKeepsEvents; TestCommandErrorsAndHelpers; TestRespondUIWritesAResponseLine; TestContextCancelAndClosedConnection; TestReadErrorIsReportedAndCloseStopsDelivery; TestStartRunsAChildAndForwardsStderr; TestChildExitIsReported; TestStartFailures; TestFindBinary; TestStreamedRunBecomesEntries; TestUnstreamedMessagesErrorsAndMissedToolStart; TestExtensionUIRequests; TestSummarize; TestRPCClientDrivesTheServer

### REQ-TUI-001 — Interactive terminal UI with Pi's editing, rendering and navigation

- Status: implemented
- Statement: On a terminal, `koine` runs a raw-mode UI in regular (scrollback) mode with Pi's editor (multiline, kill ring, undo, paste markers, history), `/` and `@` autocomplete, markdown and tool-call rendering with edit diffs, a footer with context, tokens and cost, selectors (model, thinking, settings, theme, scoped models), a `/tree` navigator, external editor, suspend, thinking toggle, Esc interrupt and a trust prompt. `keybindings.json` and JSON themes (`--theme`, `--use-theme`, `--no-themes`, `theme`) configure keys and colours. Fullscreen mode (REQ-TUI-002) and images (REQ-TUI-003) are separate. Without a terminal or with `KOINE_LINE_MODE=1` the line loop is used. Gap rows TUI-02..11, 13..15, CMD-02, CMP-07.
- Design: DES-041
- Tests: TestEditorKillRingYankAndUndo; TestEditorPasteMarkers; TestEditorHistory; TestCompleteSlashCommands; TestCompleteFileReferences; TestMarkdownTables; TestEditRendersADiffFromItsArguments; TestFooter; TestLoadBindings; TestParseTheme; TestUIModelSelector; TestUITreeNavigator; TestUIDoubleEscape; TestUIAskAndTrustPrompt; TestLoopRunsAPromptAndExits; TestRawTerminalOnAPseudoTerminal; TestTUIDefaultsAndClamps; TestTUIProjectSettingsNeedTrust; TestThemeArgs; TestTreeMovePrefillsTheEditorWhenOneIsAttached; TestUntrustedCodeIsCounted

### REQ-TUI-002 — Fullscreen (alternate-screen) terminal UI mode

- Status: implemented
- Statement: With `tuiMode: "fullscreen"` the interactive UI runs on the alternate screen: the transcript is a scrollable viewport above the editor and footer, driven by `tui.altScreen.*` keys (page, half page, line, top, bottom) and the mouse wheel (`fullscreenWheelScrollLines`), with a scrollbar (`fullscreenScrollbar`); new output follows the tail unless the user scrolled up. On exit the normal screen is restored and `fullscreenExitOutput` prints the transcript or a resume hint. The default stays `regular`. Gap rows TUI-01, CLI-09.
- Design: DES-075
- Tests: TestFullscreenFollowsTheTailAndScrolls; TestFullscreenKeepsPositionWhenOutputGrows; TestFullscreenShortTranscriptIsTopAligned; TestFullscreenScrollbar; TestFullscreenPaintDrawsOnlyWhatChanged; TestFullscreenPaintClearsKittyImagesWhenTheyMove; TestFullscreenDoesNotDrawAnImageCutOffAtTheTop; TestMouseWheelKeys; TestWheelStep; TestFullscreenIsOptIn; TestFullscreenFrameScrollsWithKeysAndWheel; TestFullscreenCustomKeysAndOverlayPrecedence; TestFullscreenExitOutput; TestFullscreenParkAndUnpark; TestLoopFullscreenEntersAndLeavesTheAlternateScreen; TestResolveSettingsDetectsImagesAndWheel; TestFullscreenAndImageSettings; TestFullscreenSettingsProjectOverridesUser

### REQ-TUI-003 — Inline terminal images with a text fallback

- Status: implemented
- Statement: Images in the transcript (tool results such as `read` of an image, user images of a resumed session) are drawn inline with the Kitty graphics protocol or the iTerm2 protocol when the terminal supports it, detected from the environment (`TERM`, `TERM_PROGRAM`, `KITTY_WINDOW_ID`, `LC_TERMINAL`, `TMUX`) and overridable with `terminal.images`; `terminal.showImages` (default true) turns drawing off and `terminal.imageWidthCells` (default 60) sets the width. Everywhere else, and for formats that cannot be drawn, a one-line text placeholder with type, size and pixel dimensions is shown. Gap row TUI-12 (display only; pasting and dragging images into the editor are not done).
- Design: DES-075
- Tests: TestDetectImageProtocol; TestImagePlaceholder; TestImageSize; TestKittyImageSequence; TestKittyChunksLongData; TestKittyConvertsJPEGAndGIFToPNG; TestITermImageSequence; TestImageFallsBackToAPlaceholder; TestBlocksCarryImages; TestHistoryImagesBecomeBlocks; TestFullscreenLiveToolAndImages; TestFullscreenAndImageSettings

### REQ-SESS-001 — Persist and resume sessions

- Status: implemented
- Statement: Every conversation is saved as it happens to a Pi v3 JSONL file under `~/.koine/sessions/--<cwd>--/` (or `--session-dir`, `$KOINE_SESSION_DIR`), unless `--no-session`. `-c` continues the latest session of the project, `--session ID|PATH|NAME` resumes one, `--fork` starts a copy, `-n` names it, `-r` lists them (a picker on a terminal, REQ-SESS-002). In the session, `/new`, `/session`, `/name`, `/resume [ref]` and `/fork` do the same. Pi session files are readable and appendable; a compaction entry on the branch replaces earlier messages with its summary. Gap rows CLI-15, 16, 17 (without a picker), 18 (`--fork`, `/fork`), 19, 20, SESS-01.
- Design: DES-011
- Tests: TestRoundTripKeepsToolCallsAndResultsTogether; TestReadsPiSessionsAndSkipsWhatItCannotRepresent; TestCompactionReplacesEarlierMessagesWithItsSummary; TestOpenRejectsBadFiles; TestNameModelAndFork; TestStoreListLatestResolve; TestSessionIsSavedContinuedAndResumed; TestForkAndNoSession; TestSessionSlashCommands; TestSessionsPersistContinueAndList

### REQ-SEC-001 — Sandboxed shell execution

- Status: planned
- Statement: The bash tool can run inside real isolation. Path allow-lists are not accepted as a substitute.
- Design: none yet

## Parity with upstream Pi (from the gap assessment, see `docs/gap-assessment.md`)

### REQ-CLI-001 — Scriptable command line

- Status: implemented
- Statement: `koine [options] [--] [@files] [message]` runs one prompt when a message is given, stdin is piped, or `-p`/`--mode json` is set; otherwise it starts the interactive session. Piped stdin, `@file` blocks and the message are joined in that order. Text mode writes only the final answer to stdout and tool activity, notices and errors to stderr; `--mode json` writes one event per line to stdout. Exit codes are 0 ok, 1 failed, 2 usage. `--model` (must resolve), `--system-prompt`, `--append-system-prompt`, `-t` and `-nt` select model, prompt and tools, and survive `/reload`. Gap rows CLI-01..04, 06 (basic), 10, 12 (basic), 29 (basic), 31 (basic), 48.
- Design: DES-010
- Tests: TestParseArgs; TestExpandFilesMakesImagesFromContentAndKeepsTextFiles; TestPipedStdinFilesAndMessageOrder; TestMessageAloneRunsPrintModeEvenOnTerminal; TestJSONModeFollowsPisEventStream; TestModelAndSystemPromptFlags; TestToolSelectionFlags; TestNothingToDoAndHelpAndMissingFile; TestEndToEndPrompt; TestBadFlagAndAPIFailure

### REQ-CLI-002 — Tool, resource, model and extension-flag options

- Status: implemented
- Statement: Tools can be allow-listed with names or `*` patterns (`-t`), adjusted with `+name`/`-name` entries, switched off in part (`-nbt`, which keeps extension and custom tools) or by pattern (`-xt`, applied last). Resources can be added per run (`-e`, `--skill`, `--prompt-template`, all repeatable) or switched off (`-ne`, `-ns`, `-np`, `-nc`); explicit paths load even with discovery off. `-e`, `--skill` and `--prompt-template` all obey the trust gate (ADR 0002): in an untrusted folder they are skipped with a warning that names the paths, and load once the folder is trusted (`/trust` or `--trust`). `--theme`, `--use-theme` and `--no-themes` are accepted and reported as unused. `--model` matches an exact id, `provider/id` or a fuzzy id/name (preferring a provider with credentials), `--provider` narrows it, `--models` and `enabledModels` give the startup scope (first match when no model is configured), `--api-key` overrides the key for the run and `--list-models [search]` prints the catalog. Long options that an extension registered with `pi.registerFlag` are accepted, handed to `pi.getFlag`, and listed by `--help`; any other unknown option is a usage error. An optional `:level` suffix on `--model` or a `--models` pattern is read after the match; the thinking level itself is handled with `--thinking`. Gap rows CLI-10 (extension flags in help), CLI-11, CLI-12 (fuzzy), CLI-13, CLI-29, CLI-30.
- Design: DES-030
- Tests: TestParseResourceArgs; TestParseWithExtensionTypes; TestToolOptionsEndToEnd; TestExtensionAndResourceFlags; TestExtensionRegisteredFlags; TestModelSelectionOptions; TestEnabledModelsSettingPicksTheStartupModel; TestAPIKeyOverridesTheEnvironmentForOneRun; TestListModels; TestMatchNamesAndGlobs; TestLoadPathsFilesDirectoriesAndProblems; TestMergeKeepsFirstOfEachName; TestResolveModelMatchesIdsNamesAndProviders; TestFuzzyMatchPrefersCredentialsThenReportsAmbiguity; TestExpandScope; TestScopeComesFromFlagOrSettingAndIsWarnedAbout; TestAPIKeyIsTheCredentialOfTheFirstModelsProviderOnly; TestToolSelectionCombinesSettingAndOptions; TestExplicitResourcesAndTheirProblems; TestExtensionFlagsReachTheHostAndSurviveReload; TestContextFilesCanBeSwitchedOff; TestExplicitExtensionsObeyTheTrustGate; TestListModelsAndTokens; TestExtensionFlagsAreRegisteredAndReceiveTheirValues; js:runner: registerFlag and getFlag

### REQ-CLI-003 — Pi's JSON event stream

- Status: implemented
- Statement: `--mode json` writes every event of Pi's JSON schema (https://pi.dev/docs/latest/json) with Pi's field names: a `session` header, `agent_start`, `turn_start`, `message_start`/`message_update`/`message_end` (`text_*`, `thinking_*`, `toolcall_*`), `tool_execution_start`/`tool_execution_update`/`tool_execution_end`, `turn_end`, `agent_end` and `agent_settled`, and the state events `queue_update`, `entry_appended`, `session_info_changed`, `thinking_level_changed`, `compaction_start`/`compaction_end` (reasons `manual`, `threshold`, `overflow`), `auto_retry_start`/`auto_retry_end` and `summarization_retry_scheduled`/`_attempt_start`/`_finished`. The state events are ordinary `agent.Event` values emitted by the agent and the app, so the server and the TUIs receive them too. Assistant messages carry `api`, `thinkingLevel` and a `usage.cost` that is always present. Stdout carries only these records; notices and errors go to stderr. Koine does not stream from providers yet, so each content block arrives as one delta. Where Pi's page names a field but not its inner shape (`entry`, `result`, `partialResult`, `messages`), Koine follows the session-file format and says so in `compat/DIVERGENCES.md`. Extensions feed three of the events: `pi.appendEntry` (a custom session entry), `pi.sendUserMessage` with `deliverAs` (the queues) and `onUpdate` of a tool (partial results). Gap rows CLI-06, EXT-20, EXT-23.
- Design: DES-031
- Tests: TestJSONModeFollowsPisEventStream; TestJSONModeReportsAFailedRunAsAnErrorMessage; TestJSONModeKeepsNoticesOffStdout; TestJSONModeWritesEveryPiEvent; TestJSONModeReportsNameThinkingAndSessionEntries; TestJSONModeAssistantMessagesCarryApiCostAndThinkingLevel; TestManualCompactionEventsCarryPisFields; TestCompactionEndsWithAnErrorAndNothingToCompactIsSilent; TestAutomaticCompactionReasons; TestAutoRetryEvents; TestSummarizationRetryEvents; TestQueuedMessagesAreDeliveredAndReported; TestToolsCanStreamPartialOutput; TestNameAndThinkingChangesAreReportedAndRecorded; TestManualCompactionIsReportedWithTheKeptEntry; TestExtensionEntriesQueuedMessagesAndToolUpdates; TestBranchSummaryAndSessionSwitchesKeepReportingEntries; TestCustomEntriesThinkingLevelAndTheAppendHook; TestExtensionsStoreSessionDataQueueMessagesAndStreamToolOutput; TestToolsCanReportPartialOutput; TestPromptStreamCarriesSessionAndThinkingEvents; js:runner: appendEntry, sendUserMessage and onUpdate reach the host

### REQ-CLI-004 — Process markers, shell session variables and the environment-variable map

- Status: implemented
- Statement: The CLI sets `AI_AGENT=koine` and `KOINE_CODING_AGENT=true` for itself and its children. The `bash` tool also gets `KOINE_SESSION_ID`, `KOINE_SESSION_FILE`, `KOINE_SESSION_PROVIDER` and `KOINE_SESSION_MODEL` for the current session and model, and `KOINE_SESSION_REASONING_LEVEL` while the model is asked to think. Every `PI_*` variable on Pi's environment-variables page is mapped to a `KOINE_*` name or recorded as not applicable in `compat/DIVERGENCES.md`. Gap rows CLI-42, CLI-43, CLI-54.
- Design: DES-030
- Tests: TestBashToolSeesSessionVariablesAndChildrenSeeMarkers; TestShellEnvironmentAndMarkers; TestBashGetsShellEnvironmentPerCall

### REQ-CLI-005 — Image `@file` arguments and piped images

- Status: implemented
- Statement: An `@file` argument whose bytes are an image (PNG, JPEG, GIF, WebP or BMP, recognised by content, not by name) is sent to the model as an image next to the prompt, and the prompt gets an empty `<file name="…"></file>` element for it (with a bracketed note when the image was resized); text files stay `<file>` blocks as before. Image data piped on stdin is sent as an image too. An image alone, with no message, is a prompt. A missing file, an unreadable image or one over 25 MiB is a usage error (exit 2) that names the argument, and nothing is sent. Notes about resizing go to stderr. Images are resized as REQ-LLM-006 says. Not covered: RPC mode (Koine has none), the interactive editor (TUI-12) and the HTTP server's `/prompt`, which take text only. Gap row CLI-05.
- Design: DES-032
- Tests: TestExpandFilesMakesImagesFromContentAndKeepsTextFiles; TestPipedInputIsAnImageOrText; TestImageFileArgumentIsSentAsAnImageAndSaved; TestImageAloneIsAPromptAndLargeImagesAreResized; TestPipedImageDataIsAnImage; TestBadImageArgumentsAreUsageErrors; TestHandleInputWithImagesSendsThemAndIgnoresThemForSlashCommands

### REQ-CFG-001 — Settings mirror Pi's schema, mapped and tracked

- Status: implemented
- Statement: Koine reads `~/.koine/settings.json` and `<project>/.koine/settings.json` (the older `config.json` still loads) using Pi's key names, nesting and merge rules: project overrides user, objects merge, `defaultTools` lists adjust or replace, global-only keys are ignored in a project. `compat/settings.json` lists every Pi key with its type and Koine status (verified, inert, todo, by-design) and the Koine-only keys. At startup each file is checked against it: unknown keys, wrong types, keys not acted on yet and misplaced global-only keys are reported, inert keys are accepted silently. `task upstream:check` diffs Pi's documented settings against the manifest and `task upstream:rebase` records new ones as `todo`. Acted on today: `defaultProvider`/`defaultModel`, `defaultTools`, `sessionDir`, `extensions`, `compaction.{enabled,reserveTokens,keepRecentTokens}`. Gap rows CLI-38..41.
- Design: DES-013
- Tests: TestSettingsManifestIsValid; TestSettingsValidateRules; TestSettingsCheck; TestVerifiedSettingsNameRealTests; TestSettingsPiKeysMapToKoine; TestSettingsExtensionsMixedStringsAndObjects; TestSettingsSessionDirAndDefaultTools; TestSettingsDiagnostics; js:parseSettingsDoc; js:diff finds new and vanished keys; js:rebase adds todo keys; js:main: exit codes

### REQ-CFG-002 — Agent directory and prompt files

- Status: implemented
- Statement: Koine's user-level directory is `~/.koine` unless `$KOINE_AGENT_DIR` names another (the counterpart of Pi's `PI_CODING_AGENT_DIR`) and holds settings, sessions, skills, agents, prompts, extensions, trust, credentials (`auth.json`, REQ-AUTH-001), provider configuration (`models.json`, REQ-LLM-007) and context files. `SYSTEM.md` replaces and `APPEND_SYSTEM.md` extends the system prompt: a trusted project's file wins over the user's and the two are never combined; project files are ignored without trust; the command-line options win over both. Gap rows CLI-31, CLI-41, SYS-02, CLI-52 to CLI-54.
- Design: DES-015
- Tests: TestOf; TestAgentDirOverrideMovesSettingsSessionsAndPrompts; TestProjectPromptFilesNeedTrustAndOverrideTheUsersWithoutCombining; TestPiAgentDirIsReadAsAFallbackAndBlankFilesAreIgnored

### REQ-LLM-004 — Model catalog, token usage and cost

- Status: implemented
- Statement: Koine knows each model's context window, output limit and price from a built-in catalog that the user can extend or override with `~/.koine/model-catalog.json`. Every model response's token usage (input, output, cache read, cache write) is recorded on the assistant message, saved in the session file in Pi's `usage` shape and rebuilt on resume. Cost is computed when the model's price is known (with long-context pricing); otherwise it is reported as unknown, never as zero. Compaction decides from the provider's reported context size plus an estimate of what was added since, and takes the window from the catalog unless `compaction.contextWindow` is set. A `usage` event reports each response; `/session` prints totals, cost and context use. Gap rows LLM-11, LLM-28, CMP-07, part of LLM-31 and CLI-22.
- Design: DES-014
- Tests: TestEmbeddedCatalogIsValid; TestLookupAcceptsProviderPrefixAndDatedSuffix; TestCostOfUsage; TestUsageArithmetic; TestMergeReplacesAddsAndRejects; TestProvidersReportUsage; TestUsageIsRecordedPricedAndReported; TestUnknownModelHasNoCostAndDefaultWindow; TestCompactionUsesTheProvidersCountAndResetClearsIt; TestUsageIsStoredInPiShapeAndRestored; TestSessionShowsUsageAndCatalogFileOverrides

### REQ-LLM-005 — Thinking levels and blocks, prompt caching, retry settings

- Status: implemented
- Statement: Thinking levels `off`, `minimal`, `low`, `medium`, `high`, `xhigh` and `max` are set with `--thinking`, a `:level` suffix on `--model` or `/model`, `/thinking`, and the settings `defaultThinkingLevel`, `modelThinkingLevels` (keyed by `provider/modelId`) and `thinkingBudgets`, in that order of precedence; the default is `medium`. The catalog says which models think and how (`reasoning`): Anthropic is asked with adaptive thinking and an effort, or with `budget_tokens`; OpenAI-compatible servers with `reasoning_effort`. A model the catalog does not know gets thinking only when a level was chosen for it. Levels a model does not accept are lowered to its highest. Thinking blocks, including redacted ones, are kept in the history, replayed to Anthropic with their signatures (not when thinking is disabled), saved in the session file in Pi's shape (`thinking`, `thinkingSignature`, `redacted`, and `thinkingLevel` on the message) and restored on resume; `hideThinkingBlock` keeps their text out of the events. Anthropic prompt caching marks the system prompt, the last tool and the last user message; cache reads and writes are reported through `Usage`. Per-provider context-overflow wording feeds the overflow retry with compaction. Long cache retention, OpenAI's cache key and `cacheWarming` are REQ-LLM-013; replaying thinking after a change of model is REQ-LLM-012. Gap rows LLM-22, 23, 25, 29 to 33, 35, CLI-14, AGT-08.
- Design: DES-016
- Tests: TestThinkingResolve; TestSplitThinkingSuffix; TestReasoningCatalogFlag; TestAnthropicThinkingRequests; TestAnthropicThinkingBlocksAreKeptAndReplayedWithSignatures; TestAnthropicPromptCachingBreakpoints; TestOpenAIReasoningEffortAndReasoningContent; TestIsOverflowPerProvider; TestRouterGivesItsRetryPolicyToProviders; TestThinkingLevelReachesTheRequestAndTheMessage; TestHideThinkingKeepsTheBlocksButNotTheEvents; TestNoThinkingForModelsThatDoNotThink; TestSubAgentsInheritThinkingSettings; TestRetriesAreReportedAsNotices; TestThinkingBlocksArePersistedInPiShapeAndRestored; TestEmptyThinkingBlocksAreNotRestored; TestThinkingSettingsDecideTheLevelPerModel; TestHideThinkingBlockSetting; TestThinkingFlagSuffixAndSlashCommand; TestRetryAndTimeoutSettingsMapToThePolicy; TestLaterSettingsFilesOverrideAndUntrustedProjectsKeepHarmlessOnes; TestThinkingFlagParsing; TestThinkingFlagAndModelSuffixReachTheRequest; TestHideThinkingBlockSettingKeepsThinkingOutOfPrintMode

### REQ-LLM-006 — Image input

- Status: implemented
- Statement: Messages can carry image blocks (base64 data and a media type). Anthropic gets `image` blocks with a `base64` source, and a tool result with images as a content array of text and images; OpenAI-compatible providers get `image_url` data-URL parts, and the images of a tool result follow it as a user message (tool messages cannot hold images). The `read` tool returns an image file (sniffed by content: PNG, JPEG, GIF, WebP, BMP) as an attachment of its result with a text line `Read image file [type]`. With `images.autoResize` (default true) a PNG, JPEG or GIF over 2000 by 2000 pixels, or over 4.5 MB once encoded, is scaled down with the standard library (a GIF becomes a PNG of its first frame; a small GIF is kept as it is); WebP and BMP cannot be decoded without a dependency, so they are sent unchanged, with a note, and refused when over the byte cap. A model whose catalog entry lists `input` without `image` gets no images: each becomes the text `[image omitted: …]` and the user is told once; a model the catalog does not know, or lists without `input`, is allowed. `images.blockImages` does the same for every model. The history and the session file keep the images, so a later switch to a model that reads them sends them again. Images count 1200 tokens in estimates. Sessions store them in Pi's shape (`{"type":"image","data","mimeType"}` in user and toolResult content) and replay them; `--mode json` writes them in message content. Wire formats are tested against fake servers only. Gap rows LLM-27, TOOL-03, CLI-16; settings `images.autoResize` and `images.blockImages`.
- Design: DES-032
- Tests: TestExpandFilesMakesImagesFromContentAndKeepsTextFiles; TestPipedInputIsAnImageOrText; TestImageSettingsMapToTheAgentAndProjectOverridesUser; TestAnUntrustedProjectMayStillSetTheImageSettings; TestHandleInputWithImagesSendsThemAndIgnoresThemForSlashCommands; TestAnthropicSendsImageSourceBlocks; TestToolResultWithoutImagesStaysAString; TestOpenAISendsImageURLParts; TestOpenAITextOnlyUserMessageIsStillAString; TestStripImagesReplacesThemWithANoteAndLeavesTheHistoryAlone; TestAcceptsImagesFollowsTheCatalogAndAllowsUnknownModels; TestImageBlockAccessors; TestSniffGoesByContentNotExtension; TestPrepareKeepsSmallImagesUnchanged; TestPrepareResizesToTheDocumentedLimit; TestPrepareWithoutResizeSendsTheOriginal; TestPrepareJPEGStaysJPEGAndGIFBecomesPNG; TestPrepareWebPAndBMPPassThroughAndSayWhy; TestPrepareShrinksUntilThePayloadFits; TestPrepareReportsCorruptImages; TestReadFile; TestReadAttachesImagesByContentNotExtension; TestReadResizesLargeImagesAndSaysSo; TestReadImageWithoutASinkSaysItWasNotAttached; TestReadStillReadsTextAndReportsBadImages; TestPromptImagesJoinTheUserMessageAndReachTheProvider; TestToolImagesRideInTheToolResultAndFollowTheResizeSetting; TestImagesAreLeftOutForModelsThatCannotReadThem; TestBlockImagesSettingKeepsEveryImageFromTheProvider; TestImageBlocksAreStoredInPisShapeAndReplayed; TestAnImageOnlyUserMessageIsKeptAndImagesWithoutDataAreSkipped; TestImagesSurviveCloneByteForByte; TestImagesCountForTokensAndAppearInTheTranscript; TestImageFileArgumentIsSentAsAnImageAndSaved; TestImageAloneIsAPromptAndLargeImagesAreResized; TestAutoResizeAndBlockImagesSettings; TestPipedImageDataIsAnImage; TestBadImageArgumentsAreUsageErrors; TestReadToolSendsImagesBackAndJSONModeShowsThem; TestJSONModeShowsUserImages

### REQ-AUTH-001 — Provider credentials: one precedence, a private store, secrets that can be commands

- Status: implemented
- Statement: A provider's API key is found in this order: `--api-key` (for the provider of the first model, this run only), the provider's entry in `auth.json` in the agent directory, the provider's `apiKey` in `models.json`, then its environment variables (Pi's names, for example `GEMINI_API_KEY`, `HF_TOKEN`, `AI_GATEWAY_API_KEY`; `ANTHROPIC_AUTH_TOKEN` is sent as a bearer token). An `auth.json` entry is `{"type":"api_key","key":…,"env":{…}}`; its `env` object wins over the process environment for that provider (Azure and Cloudflare settings). A key, `apiKey` or header value may be written as `!command` (the trimmed output of a shell command), as a template with `$NAME` or `${NAME}`, with `$$` and `$!` as literal `$` and `!`, or as the literal. Commands from `auth.json` run once per process and a failure is kept; commands from `models.json` run again for each request (their output is reused for 5 seconds, because choosing a model asks many providers at once). An unset variable in a template is an error. `auth.json` is written atomically with mode 0600 in a 0700 directory, keeps entries it does not understand (Pi's OAuth logins), and is never overwritten when it is not valid JSON. An entry that cannot be used (an OAuth login, a failing command) does not hide a credential further down the list; it is reported only when nothing else works. Only user-level files may run commands or hold keys: a project `models.json` needs trust (REQ-LLM-007). Bedrock and Vertex sign in with the machine's cloud credentials (REQ-LLM-014, REQ-LLM-015; gap rows LLM-06, LLM-07, LLM-21). OAuth logins and workload identity federation are not implemented (gap rows LLM-17 to LLM-20, LLM-21). Gap rows LLM-13, LLM-14, LLM-15, LLM-16.
- Design: DES-060
- Tests: TestStoreWritesOwnerOnlyAndKeepsUnknownEntries; TestStoreRemoveIsIdempotentAndRefusesBrokenFiles; TestStoreWithoutADirectoryHoldsNothingAndRefusesToSave; TestResolveTemplatesAndEscapes; TestCommandSecretsAreCachedOnlyWhenAskedAndFailuresAreKept; TestUncachedCommandsAreReusedOnlyWithinTheBurstWindow; TestCommandSecretsTimeOut; TestShellCommandsReallyRun; TestCredentialPrecedenceIsRuntimeStoredModelsFileThenEnvironment; TestStoredEntryEnvWinsAndAnEntryWithoutAKeyFallsThrough; TestMissingAndUnusableCredentialsExplainThemselves; TestAnthropicBearerTokenFromTheEnvironment; TestStoredCredentialsMayBeCommandsOrTemplatesAndOAuthEntriesAreNotUsed; TestPrecedenceAcrossFlagStoreModelsFileAndEnvironment; TestAPIKeyIsTheCredentialOfTheFirstModelsProviderOnly

### REQ-AUTH-002 — `koine auth` and `/login`, `/logout`

- Status: implemented
- Statement: `koine auth login <provider> [--api-key] [--no-browser]` reads an API key on the terminal without echo (stty; where that is not possible the key is visible and the user is told) and stores it in `auth.json`; it needs an interactive terminal, refuses `--no-browser` with `--api-key`, and says that signing in with an account is not implemented. `koine auth logout <provider>` removes the entry and exits 0 when there is none. `koine auth check (--provider P | --model M) [--json] [--credentials] [--no-refresh]` prints `ready`, `not_ready` (no credential anywhere) or `invalid` (a credential exists and cannot be used, or the provider is unknown) and exits 0, 1 or 2; the key is printed only with `--credentials`. `print-api-key` prints the key; `print-bearer-token [--min-expiry D]` prints a bearer token if the credential is one (`ANTHROPIC_AUTH_TOKEN`), the duration is checked but not enforced because Koine cannot know when a token it did not mint expires. In a session, `/login [provider]` asks for a key through `App.Prompt` (default: the terminal without echo) or offers the providers through `App.Choose`, and `/logout [provider]` removes one; both refuse in print and server runs, where nobody can be asked. In the terminal UI the key is typed into a prompt that shows bullets. A login takes effect at once, without `/reload`. The JSON of `check` is Koine's own: Pi documents the states and exit codes only. Gap row CLI-36.
- Design: DES-060
- Tests: TestReadSecretReadsALineAndHidesEchoOnTerminals; TestDefaultSttyRunnerReportsAFailure; TestLoginStoresTheKeyPrivatelyAndLogoutRemovesIt; TestLoginRefusesUnknownProvidersAndHandlesPromptFailures; TestUIPromptHidesSecretsAndCanBeCancelled; TestLoopLoginAsksForTheKeyInTheTerminalUI; TestAuthLoginStoresAKeyAndLogoutRemovesIt; TestAuthLoginUsageErrors; TestAuthCheckStatesExitCodesAndJSON; TestAuthPrintCommands; TestAuthSeesModelsJSONProvidersAndTheirWarnings; TestARunUsesTheStoredKeyBeforeTheEnvironmentAndTheFlagBeforeBoth

### REQ-LLM-007 — Providers and models from Pi's models.json

- Status: implemented
- Statement: `models.json` in the agent directory (and `.koine/models.json` of a trusted project) has Pi's schema (https://pi.dev/docs/latest/models-configuration): `providers.<id>` with `name`, `baseUrl`, `api`, `apiKey`, `headers`, `authHeader`, `compat`, `models[]` and `modelOverrides`. A key that names a built-in provider changes it (a bare `baseUrl` routes it through a proxy); any other key adds a provider (`api` defaults to `openai-completions`; Koine's older `type: "anthropic"` and `apiKeyEnv` of the `providers` setting still work). A model entry may override `api`, `baseUrl`, `headers` and `compat`, and its `id`, `name`, `reasoning`, `input`, `contextWindow` (default 128000), `maxTokens` (default 16384) and `cost` join the model catalog; `modelOverrides` change those fields on an existing model, merging `cost` field by field; `samplingParams` are merged into Chat Completions and Responses request bodies. The `compat` flags Koine acts on are `supportsReasoningEffort`, `supportsUsageInStreaming`, `maxTokensField`, `requiresToolResultName`, `requiresNonNullAssistantContent` and `requiresToolsWithToolHistory`; every other key (`thinkingFormat`, `thinkingLevelMap`, `inputLimits`, `promptCache`, `cost.tiers`, `oauth`, ...) is read and reported as not acted on, and an unsupported `api` makes the provider's first request fail with that message. `model-catalog.json` keeps its role and has the last word on windows and prices. `/reload` re-reads the file. A project file can run `!command` secrets and send your key to its `baseUrl`, so it is skipped, and counted in the untrusted-folder warning, until the folder is trusted. Gap row LLM-36.
- Design: DES-061
- Tests: TestReadModelsFileAndCatalog; TestModelsJSONProvidersChangeAndAddProviders; TestOpenAICompatFlagsChangeTheRequest; TestModelsJSONInTheAgentDirectoryDefinesProvidersModelsAndKeys; TestModelsJSONProblemsAreWarnedAboutAndNeverFatal; TestAProjectsModelsJSONNeedsTrustBecauseItCanRunCommandsAndRedirectKeys; TestNewProviderSetupWithoutAHomeStillWorks

### REQ-LLM-008 — More built-in providers and local servers

- Status: implemented
- Statement: Amazon Bedrock and Google Vertex AI are REQ-LLM-014 and REQ-LLM-015. Besides Anthropic and OpenAI, these providers work with an API key from Pi's variable and their public endpoint: Azure OpenAI, Google Gemini (`google`, alias `gemini`), Mistral, Groq, Cerebras, xAI, OpenRouter, DeepSeek, Together AI, Fireworks, Hugging Face, NVIDIA NIM, Moonshot AI, Vercel AI Gateway and Z.AI. Ollama, LM Studio, vLLM, SGLang and llama.cpp are local OpenAI-compatible servers on their usual ports and need no key. A bare model id starting with `gemini-` goes to Google. The endpoints are the authors' knowledge of the vendors' public addresses and were not compared with Pi's registry; `baseUrl` in models.json corrects one. The providers in Pi's list that are not here (GitHub Copilot, Kimi, MiniMax, Qwen, Xiaomi, Baseten, Ant Ling, Meta, Radius, TypeSafe, OpenCode Zen, Cloudflare, and the regional variants) can be added with models.json. Gap rows LLM-10, LLM-40.
- Design: DES-061
- Tests: TestBuiltinProvidersAreWellFormedAndLocalOnesNeedNoKey; TestRouterResolve; TestRouterRejectsInsteadOfGuessing

### REQ-LLM-009 — OpenAI Responses API and Azure OpenAI

- Status: implemented
- Statement: A provider or model with `api: "openai-responses"` is called at `POST <baseUrl>/responses`, stateless (`store: false`): the system prompt is `instructions`, messages become input items, tool calls and results become `function_call` and `function_call_output` items, tools are sent with `strict: false`, thinking levels become `reasoning.effort` with summaries, and streaming reads the event stream (text, reasoning summary and call-argument deltas) and takes the final response from `response.completed`. Reasoning items are not sent back. Azure OpenAI (`azure`, alias `azure-openai-responses`) uses the same protocol with the `api-key` header, `AZURE_OPENAI_BASE_URL` or `AZURE_OPENAI_RESOURCE_NAME` (else `baseUrl` in models.json), `AZURE_OPENAI_DEPLOYMENT_NAME_MAP` (`model=deployment,…`) and `AZURE_OPENAI_API_VERSION` (default `v1`, sent as `api-version`). OpenAI itself still uses Chat Completions unless models.json says otherwise. Verified against fake servers only, not live APIs. Gap rows LLM-04, LLM-08.
- Design: DES-062
- Tests: TestResponsesSendsAStatelessRequestAndParsesTheOutput; TestResponsesErrorsAndIncompleteAnswers; TestResponsesStreamReportsDeltasAndReturnsTheFinalResponse; TestResponsesStreamErrorsKeepWhatArrived; TestAzureUsesTheResourceDeploymentsKeyHeaderAndApiVersion

### REQ-LLM-010 — Google Gemini

- Status: implemented
- Statement: The `google` provider (`api: "google-generative-ai"`) calls `models/<model>:generateContent`, or `:streamGenerateContent?alt=sse` when streaming, with `x-goog-api-key` (`Authorization: Bearer` with `authHeader`). The system prompt is `systemInstruction`; tools are sent as `functionDeclarations` with `parametersJsonSchema`; a function response carries the name of its call; images are `inlineData`; thinking levels become `thinkingConfig` with a budget and thoughts are returned as thinking blocks; usage separates cached tokens and counts thinking tokens as output. A blocked prompt and an error chunk become errors, and a stream cut short keeps its text. Thought signatures are REQ-LLM-018 (Vertex AI is REQ-LLM-015). Verified against fake servers only. Gap row LLM-05.
- Design: DES-062
- Tests: TestGeminiRequestAndResponse; TestGeminiThinkingOffBearerKeyAndStopReasons; TestGeminiBlockedPromptsAndErrors; TestGeminiStreamReportsDeltasAndKeepsPartialText

### REQ-LLM-011 — Readable provider errors and auth guidance

- Status: implemented
- Statement: A failed provider call reports the message inside the JSON body (OpenAI, Anthropic, Google, OpenRouter, Mistral and FastAPI shapes) instead of the raw body, with the error type when it adds something. A 401 or 403 adds what to do: set the provider's variable or run `koine auth login <provider>`; 402 says the account has no credit. A missing key names the variables and the login command, and a credential that exists but cannot be used says why. Overflow and quota detection still read the raw body. Gap row LLM-41.
- Design: DES-062
- Tests: TestErrorMessagesFromProviderBodies; TestHTTPErrorsAddAuthGuidance; TestARunWithoutAKeyExplainsHowToGetOne; TestMissingAndUnusableCredentialsExplainThemselves

### REQ-LLM-012 — Changing model or provider in the middle of a session

- Status: implemented
- Statement: The model can be changed with `/model`, `--model` on resume or a `PrepareNextTurn` hook while the conversation continues, across providers. Every request goes through `llm.TransformMessages` in the router, which converts a copy of the history for the target provider and leaves the history and the session file as they were. Assistant messages remember the model that wrote them (`Message.Origin`: the model as selected, or Pi's `provider` and `model` joined when a session file is resumed); for a message from another model, readable thinking becomes plain text and redacted thinking is dropped, because a signature only replays on the model that made it, while a message of the same model, or of unknown origin, is left alone. Tool call ids are rewritten, in the calls and in their results, to what the target accepts: letters, digits, `_` and `-` up to 64 characters for Anthropic, Bedrock, Responses and Azure; up to 40 for Chat Completions; exactly 9 alphanumeric characters for Mistral; Gemini and Vertex are sent no ids. Valid ids are kept and a rewritten id is a pure function of the old one, so the conversion is repeatable and idempotent. A tool result with no matching call before it is dropped; a call with no result gets an error result; results are placed first in their message; messages left empty are removed. Images are handled as REQ-LLM-006 says. Not done: Gemini thought signatures, and a comparison of every rule with Pi's `transform-messages.ts`, which was not read. Gap row LLM-24.
- Design: DES-063
- Tests: TestTransformConvertsThinkingOfOtherModels; TestTransformDoesNotChangeItsInput; TestTransformNormalisesToolCallIDs; TestTransformIDRulesPerProvider; TestTransformRepairsOrphanToolCallsAndResults; TestRouterConvertsHistoryAcrossProviders; TestRestoredAssistantMessagesRememberTheirModel; TestCacheHintsOriginAndWarmingReachTheProvider

### REQ-LLM-013 — Prompt cache retention, OpenAI cache key and cache warming

- Status: implemented
- Statement: `KOINE_CACHE_RETENTION=long` (Pi: `PI_CACHE_RETENTION=long`, "extended provider prompt caching where supported") asks for the longest cache lifetime where Koine knows it is accepted: Anthropic's `cache_control` gets `ttl: "1h"` on `api.anthropic.com` only, and OpenAI (`api.openai.com`) is sent `prompt_cache_retention: "24h"`. OpenAI (Chat Completions and Responses) and Azure OpenAI are also sent `prompt_cache_key`, the session id, with or without long retention; a proxy or custom server gets neither field. The setting `cacheWarming` (`off`, `streaming` or `idle`, default `idle`, honoured only in the user's settings file) keeps a cache from lapsing: when the model declares a cache lifetime (`promptCache.short` and `promptCache.long` in seconds, built in for the three Claude models, settable in models.json and `modelOverrides`) and a cache miss would cost at least $0.05 more than a refresh, the last request is replayed shortly before the cache expires and cancelled at its first token. `streaming` does this only while a run is active, `idle` also for 30 minutes after it; a new request, a new session or a provider that cannot stream cancels it. The usage of refreshes is added to the session totals and cost and never enters the conversation. The savings figure is Koine's estimate (a miss re-writes the prompt at the cache-write price, a refresh reads it), because Pi documents only the threshold. Not done: `/session` showing the next decision, the `cache_warming_decision` extension event, `showCacheMissNotices`, a cache lifetime for Bedrock cache points. Verified against fake servers only. Gap rows LLM-25, LLM-26.
- Design: DES-064
- Tests: TestAnthropicLongCacheRetentionOnlyOnAnthropicsEndpoint; TestOpenAIPromptCacheKeyAndRetention; TestRouterEnablesPromptCacheForOpenAIAndAzureOnly; TestSessionIDAndRetentionReachTheRequest; TestDecideWarm; TestWarmerRefreshesDuringARunAndCountsTheUsage; TestWarmerIdleModeKeepsWarmForThirtyMinutes; TestWarmerStopsWhenItCannotHelp; TestWarmerOverlapWithARequestKeepsItsUsage; TestCatalogDeclaresPromptCacheLifetimesForClaude; TestCacheHintsOriginAndWarmingReachTheProvider; TestRunWithoutAWarmerStillWorks; TestCacheWarmingSettingIsGlobalOnlyAndDefaultsToIdle

### REQ-LLM-014 — Amazon Bedrock (Converse and ConverseStream)

- Status: implemented
- Statement: The `amazon-bedrock` provider (alias `bedrock`; `api: "bedrock-converse-stream"`; bare ids containing `anthropic.claude` or starting with `amazon.nova` are routed to it) calls `POST /model/<id>/converse-stream` (`/converse` without streaming) on `bedrock-runtime.<region>.amazonaws.com`, or on `AWS_ENDPOINT_URL_BEDROCK_RUNTIME`, or on the provider's `baseUrl` in models.json (which wins). Requests are signed with AWS Signature Version 4, implemented on the standard library (the model id is percent-encoded once in the path and again in the canonical request), or carry a Bedrock API key as a bearer token. The credential is, in order: `--api-key`, auth.json, models.json `apiKey` or `AWS_BEARER_TOKEN_BEDROCK`; else `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN`; else the profile named by `AWS_PROFILE` (default `default`) in `~/.aws/credentials` and `~/.aws/config` (or `AWS_SHARED_CREDENTIALS_FILE` and `AWS_CONFIG_FILE`), which holds static keys, or, since REQ-LLM-017, an SSO login, a role or a web identity token; `credential_process` and MFA are refused with a message. The region is `AWS_REGION`, `AWS_DEFAULT_REGION`, the profile's, else `us-east-1`. The `env` object of the auth.json entry supplies any of these variables. `AWS_BEDROCK_SKIP_AUTH=1` sends no credentials (a proxy that adds its own) and `AWS_BEDROCK_FORCE_CACHE=1` writes cache points for ids that are not recognisable Claude names; `AWS_BEDROCK_FORCE_HTTP1` is not acted on. The streamed answer is decoded from AWS's event stream framing (`application/vnd.amazon.eventstream`, CRC-32 checked) by code in `core/llm`: text, reasoning (with signatures and redacted payloads), tool-call argument deltas and usage including cache reads and writes, and exception frames end the stream with an error. Requests carry the system prompt, tools, images and tool results; neighbouring messages of one role are merged because Converse needs the roles to alternate. For Claude ids, thinking is sent in `additionalModelRequestFields` (budget or adaptive with effort, as for Anthropic) and cache points follow the system prompt, the tools and the last user message. Credentials never appear in errors. Not done: SSO, assumed roles, `credential_process`, ECS, IRSA and EC2 instance credentials, reasoning for non-Claude models, a cache lifetime on cache points. Verified against fake servers, a published SigV4 test vector and the documented signing-key example only; the live API was not exercised. Gap rows LLM-07, LLM-21.
- Design: DES-065
- Tests: TestSigV4GetVanilla; TestSigV4SigningKey; TestSigV4PathQueryTokenAndHeaders; TestAWSEscape; TestReadEventMessageDecodesHeadersOfEveryType; TestReadEventMessageSequenceAndEOF; TestReadEventMessageRejectsCorruptFrames; TestAWSCredentialsFromEnvironment; TestAWSCredentialsFromSharedFiles; TestAWSCredentialsErrors; TestBedrockCompleteSignsAndParses; TestBedrockBearerSkipAuthAndNoCredentials; TestBedrockMessagesMergeRolesAndConvertBlocks; TestBedrockThinkingForClaudeOnly; TestBedrockStreamDecodesEventStream; TestBedrockStreamErrors; TestBedrockHTTPErrorsAreReadable; TestBedrockRouting; TestBedrockAndVertexAreRegisteredWithTheRouter

### REQ-LLM-015 — Google Vertex AI (Gemini)

- Status: implemented
- Statement: The `google-vertex` provider (alias `vertex`; `api: "google-vertex"`) speaks the Gemini protocol of REQ-LLM-010 at Vertex AI. With an API key (`GOOGLE_CLOUD_API_KEY`, `--api-key`, auth.json or models.json) it uses express mode at `https://aiplatform.googleapis.com/v1/publishers/google/models/<model>` with `x-goog-api-key`. Otherwise it signs in with Application Default Credentials: the file named by `GOOGLE_APPLICATION_CREDENTIALS`, else gcloud's `application_default_credentials.json` (`CLOUDSDK_CONFIG` or `~/.config/gcloud`). A `service_account` key file is exchanged for an access token with a self-signed RS256 JWT (the JWT bearer grant, scope `cloud-platform`), an `authorized_user` file with its refresh token; tokens are cached until a minute before they expire. The address is `https://<location>-aiplatform.googleapis.com/v1/projects/<project>/locations/<location>/publishers/google/models/<model>` (`global` uses `aiplatform.googleapis.com`), with the project from `GOOGLE_CLOUD_PROJECT`, `GCLOUD_PROJECT` or the credentials file and the location from `GOOGLE_CLOUD_LOCATION`; both are required for ADC and checked for unsafe characters, and a `baseUrl` in models.json replaces the host. A `token_uri` in a key file must be https (or the local machine). Errors name files and variables and never print keys or tokens; `koine auth check google-vertex` reports whether a credentials file is found. Thought signatures are REQ-LLM-018. Not done: the Compute Engine/Cloud Run metadata server, `external_account` and impersonation files, Claude and other publishers on Vertex. Verified against fake servers and a locally generated RSA key only; the live API and the express-mode URL were not exercised. Gap rows LLM-06, LLM-21.
- Design: DES-065
- Tests: TestGoogleServiceAccountTokenExchange; TestGoogleAuthorizedUserRefresh; TestGoogleCredentialErrorsNeverLeakSecrets; TestGoogleTokenRefusedAndPKCS1Key; TestVertexWithApplicationDefaultCredentials; TestVertexAPIKeyAndConfigurationErrors; TestBedrockAndVertexAreRegisteredWithTheRouter

### REQ-LLM-016 — Opt-in model catalog refresh

- Status: implemented
- Statement: `koine update --models [--url URL] [--force]` (Pi: `pi update --models`) fetches a model catalog and merges it into the user catalog `model-catalog.json` in the agent directory (`~/.koine`, `$KOINE_AGENT_DIR`), which Koine already applies over the built-in catalog. It is the only thing that fetches a catalog; nothing runs at startup, so Koine works offline and a missing or stale file changes nothing. The address is `--url`, else `KOINE_MODEL_CATALOG_URL`, else the user setting `modelCatalogUrl` (Koine-only; there is no built-in default address because Koine has no catalog service of its own); it must be https (http only for this machine) and redirects may not leave that rule. `KOINE_OFFLINE=1` (Pi: `PI_OFFLINE`) refuses the request. The download is capped at 4 MiB and validated before anything is written: JSON, `schema` 1 if given, 1 to 5000 models with unique printable ids, non-negative windows and prices within sane bounds, `input` kinds `text` or `image`. Entries the server lists replace entries with the same id; the user's other entries and unknown fields stay in place; the previous file is copied to `model-catalog.json.bak`; the new file is written beside the old one and renamed over it, and the merged result is validated again. On any failure (network, status, size, validation, an existing file that is not valid JSON) the file is not touched and the command says so and exits 1. The file records `source`, `fetched` and the server's `etag`; a later refresh from the same address sends `If-None-Match` and reports "up to date" on a 304 (`--force` downloads anyway). Not done: the Pi catalog's own address and format are not documented, so a server must serve Koine's `{"models":[…]}` shape; no automatic or background refresh. Verified against local test servers only. Gap row LLM-12.
- Design: DES-066
- Tests: TestRefreshCatalogWritesMergesAndKeepsABackup; TestRefreshCatalogIsConditionalAndReportsUnchanged; TestRefreshCatalogNeverOverwritesOnFailure; TestRefreshCatalogURLRules; TestUpdateModelsRefreshesTheUserCatalog

### REQ-LLM-017 — AWS SSO, assumed roles and web identity for Bedrock

- Status: implemented
- Statement: The Bedrock credential chain of REQ-LLM-014 also resolves, with the standard library only (ADR 0012): the profile named by `AWS_PROFILE` (default `default`) in `~/.aws/config` and `~/.aws/credentials` when it is (1) an IAM Identity Center login, `sso_session` with `[sso-session NAME]` (`sso_start_url`, `sso_region`) or the older `sso_start_url`/`sso_region` form, plus `sso_account_id` and `sso_role_name`, using the access token that `aws sso login` cached in `~/.aws/sso/cache/<sha1 of session name or start URL>.json` to call the portal's `GetRoleCredentials`; (2) a role, `role_arn` with `source_profile` (any depth; loops are refused) or `credential_source = Environment`, plus `role_session_name`, `external_id` and `duration_seconds`, through STS `AssumeRole` signed with SigV4 by the source's keys, in the profile's region (else the usual region); (3) a web identity, `role_arn` with `web_identity_token_file`, or the variables `AWS_WEB_IDENTITY_TOKEN_FILE` and `AWS_ROLE_ARN` (and `AWS_ROLE_SESSION_NAME`) with an unsigned `AssumeRoleWithWebIdentity`. `AWS_ENDPOINT_URL_STS` and `AWS_ENDPOINT_URL_SSO` move the services. Temporary keys are cached until five minutes before they expire and then replaced. Environment keys still win, except when `AWS_PROFILE` names a role, SSO or web identity profile. Koine never starts or refreshes an SSO login: a missing or expired token is an error that says `aws sso login --profile NAME`. `koine auth check amazon-bedrock` validates the profile and the cached token without calling AWS and names the source (for example `aws:profile dev (assume role, source base)`). Refused with a message naming the setting: `credential_process`, `mfa_serial`, other `credential_source` values (EC2 and ECS instance credentials). Errors never print keys or tokens. Verified against local fake STS and portal servers only, not a live account. Gap rows LLM-07, LLM-21.
- Design: DES-066
- Tests: TestAWSAssumeRoleFromSourceProfileIsCachedUntilItExpires; TestAWSRoleChainsSignWithTheEarlierRolesKeysAndLoopsAreRefused; TestAWSAssumeRoleFromEnvironmentKeysAndErrors; TestAWSWebIdentityFromEnvironmentAndProfile; TestAWSSSOUsesTheCachedLoginTokenForASessionAndALegacyProfile; TestAWSSSOErrorsTellTheUserToLogInAgain; TestAWSRoleOverSSOProfileAndDescribeWithoutNetwork; TestBedrockRouterUsesTheAWSChain; TestAWSCredentialsFromEnvironment; TestAWSCredentialsFromSharedFiles; TestAWSCredentialsErrors

### REQ-LLM-018 — Gemini thought signatures

- Status: implemented
- Statement: Gemini's `thoughtSignature` is kept and sent back, for the Google and Vertex providers. A signature on a function call is stored on the `tool_use` block, one on a text part on the text block, and one on a thought part (or a signature that arrives alone, as the last streamed chunk may deliver it) on the block it closes; a signed block is never merged with the part after it. The same model gets them back unchanged: signed thoughts are replayed as `thought` parts, text and calls carry `thoughtSignature`. A value that is not base64 (another provider's token in the same session field) is not sent. For `gemini-3` models a function call without a signature gets Google's documented placeholder `skip_thought_signature_validator` on the first call of a model turn. Signatures of another model are dropped when the conversation is converted (REQ-LLM-012). Session files use Pi's names: `textSignature` on text blocks, `thoughtSignature` on tool calls, `thinkingSignature` on thinking. Unsigned thinking is still not sent back. Verified against fake servers only. Gap rows LLM-05, LLM-06, LLM-24.
- Design: DES-066
- Tests: TestGeminiKeepsThoughtSignaturesOnTextAndCalls; TestGeminiStreamKeepsThoughtSignatures; TestGeminiReplaysThoughtSignaturesAndFillsInForGemini3; TestTransformDropsThoughtSignaturesOfAnotherModel; TestGeminiThoughtSignaturesArePersistedInPiShapeAndRestored

### REQ-SESS-002 — Session tree, labels and full Pi entry fidelity

- Status: implemented
- Statement: `/tree [filter|id]` lists the entry tree and moves the conversation to an earlier entry (a user message is returned for editing and re-sending as a new branch), optionally summarising the branch it leaves with the compaction summariser (`--summary`; settings `branchSummary.reserveTokens`, `branchSummary.skipPrompt`, `treeFilterMode`). `/label` bookmarks entries, `/clone` copies the branch to a new session, `/fork <id>` starts one from an earlier user message, `/export [file]` writes JSONL or a self-contained HTML page. `-r` and `/resume` offer a numbered picker. Version 1 and 2 files are migrated to version 3 on load. Entries Koine does not interpret (custom, custom_message, unknown fields and blocks) are never rewritten by appends and are copied byte for byte by `/clone` and `/fork`. Extends REQ-SESS-001. Gap rows CLI-17, 18 (`/clone`), 21, 22, 23 (`/export`; `--export` and `/share` not built), 46, SESS-02, CMP-06. Sending thinking blocks to a model is REQ-LLM-005 and image blocks are REQ-LLM-006, not this requirement.
- Design: DES-020
- Tests: TestAppendKeepsExistingBytesAndAddsMissingNewline; TestBranchingNavigationAndSummary; TestLabelsFindAndRows; TestRowsDescribeAndNoToolsFilter; TestForkAtKeepsRawLinesAndRelinksSkippedNames; TestExportJSONLAndHTML; TestExportHTMLOtherEntriesAndHostileImages; TestOpenMigratesV1ToV3; TestOpenMigratesV2HookMessages; TestListMigratesInMemoryOnly; TestMigrationErrors; TestTreeListsAndFilters; TestTreeMoveBranchAndSummary; TestTreeSummaryPromptAndSettings; TestTreeSummaryFailureKeepsPosition; TestBranchSummaryBudgetKeepsNewestMessages; TestLabelCommand; TestCloneAndForkFromMessage; TestExportCommand; TestResumePickerNumbersAndChoose; TestTreeCommandsWithoutSessionAndSettings; TestResumePickerOnATerminal; TestChoose

### REQ-CMP-001 — Compaction and overflow recovery

- Status: implemented
- Statement: When the estimated context (system prompt plus history) exceeds the window minus a reserve (default 200000 and 16384 tokens, `compaction.*` in config), the older part of the conversation is replaced by a structured summary, keeping about 20000 recent tokens; `/compact [focus]` does it on demand and `compaction.enabled: false` turns the automatic one off. The cut never separates a tool result from its call. Later compactions update the previous summary, and the summary lists files read and modified. A provider "prompt too long" error triggers one compaction and one retry per prompt. Compactions are saved as `compaction` entries, so a resumed session starts from the summary. Gap rows CMP-01..05 (token counts are estimated, not provider-reported).
- Design: DES-012
- Tests: TestCutPointNeverSeparatesAResultFromItsCall; TestSummarize; TestIsOverflow; TestManualCompactReplacesOldHistoryAndReportsTheCut; TestCompactErrors; TestAutoCompactsBeforeTheWindowFillsUp; TestOverflowCompactsAndRetriesOnce; TestCompactIsSavedAndReplayedOnResume; TestCompactCommandIsSavedAndSurvivesResume; TestCompactionNotSavedWhenFileAndConversationDisagree

### REQ-MCP-001 — MCP client

- Status: implemented
- Statement: Koine connects to MCP servers over stdio and streamable HTTP (JSON or SSE answers; the legacy SSE transport is refused) and registers their tools as `mcp__<server>__<tool>`. Servers come from `mcp.json` (`{"mcpServers": {…}}`) in the agent directory and in the project (`.koine/mcp.json`, `.pi/mcp.json`), from Claude's `.mcp.json` and from the `mcp` block of opencode's `opencode.json`. A project entry replaces a user entry of the same name, except that an entry with only `enabled`, `exposure` or `toolExposure` changes just those fields. `${VAR}` / `${VAR:-default}` and whole-value `!command` secrets are resolved when connecting; `enabled`, `timeout` (seconds, restarted by progress notifications, default 60), `description`, `cwd`, `env`, `headers` and `type` are honoured, invalid entries are reported and skipped. A server's tools are `direct` (default), `deferred` (found with `tool_search`, offered from the next model call) or `hidden`, per server (`exposure`) and per tool (`toolExposure`, exact names before patterns, first pattern wins); Pi's `codemode` is read as `deferred` because Koine has no sandbox. Tool results become text and images, structured-only results show their JSON, text over 20 KB is cut in the middle with the full text saved to a file, and `list_mcp_resources`, `list_mcp_resource_templates` and `read_mcp_resource` expose resources. Stdio servers are shut down by closing stdin, then SIGTERM, then SIGKILL of the process group, their stderr goes to `mcp.log` (rotated at 5 MB), HTTP errors 408/429/5xx are retried twice, and a dropped connection reconnects on the next call. Servers connect in the background (the session waits up to 10 seconds). A server is third-party code, so project-level servers are not read or started until the folder is trusted (REQ-TRUST-001); user-level ones run. `/mcp` shows state and `/mcp reconnect NAME` reconnects. Gap rows LLM-42, 43, 45, 46, 47. Not covered: the codemode sandbox (LLM-48), passing tool annotations to `tool_call` events, `--no-mcp`, `pi.registerMcpServer`, listChanged for resources.
- Design: DES-050
- Tests: TestStdioInitializeListAndCall; TestClientTimeoutAndProgress; TestStdioServerCrash; TestStdioCloseKillsStubbornProcess; TestHTTPJSONAndSSE; TestHTTPRetriesTransientFailures; TestHTTPSessionExpiry; TestHTTPBearerAndChallenge; TestReadSSE; TestLoadUserAndProject; TestLoadProblems; TestOpencodeAndAgentDirOverride; TestResolver; TestManagerDirectTools; TestToolResultsErrorsImagesAndLimits; TestExposureModesAndToolSearch; TestToolNaming; TestResourceTools; TestRenderContentKinds; TestLimitText; TestReconnectAfterCrashAndExplicit; TestToolListChangedRefresh; TestStdioCwdAndEnv; TestLogRotates; TestMCPUserServerToolsAndSlashCommand; TestMCPProjectServersNeedTrust; TestMCPProblemsAndFailuresAreWarnings; TestMCPToolSelectionPattern

### REQ-MCP-002 — MCP OAuth for remote servers

- Status: implemented
- Statement: An HTTP server that answers 401 can be signed in to with `koine mcp login NAME`: the authorization server is found from the 401's `resource_metadata` or `/.well-known/oauth-protected-resource` (RFC 9728) and its metadata (RFC 8414, else OpenID configuration; `oauth.authServerMetadataUrl` overrides), the client is `oauth.clientId` or registered dynamically (RFC 7591), the authorization code flow uses PKCE S256 and the `resource` parameter on a loopback callback (`oauth.callbackPort` or `callbackUrl`, state checked), and the tokens go to `<agent dir>/mcp-auth.json` (mode 0600, scoped to server name and URL). Access tokens are refreshed before they expire and after a 401; when that fails the server shows as "needs login". Every OAuth endpoint must be https (http only on loopback). An `Authorization` header in the config switches OAuth off. Not covered: Client ID Metadata Documents (`clientRegistration: "cimd"` is refused, because Koine has no hosted client document), and the interactive browser step itself is only tested with a simulated browser against a fake authorization server. Gap row LLM-44.
- Design: DES-050
- Tests: TestOAuthLoginDiscoversRegistersAndStoresTokens; TestOAuthRefreshAndChallenge; TestOAuthManagerReportsNeedsLogin; TestOAuthConfiguredClientAndCallbackPort; TestOAuthLoginFailures; TestOAuthDiscoveryFallbacks; TestPKCEPair; TestTokenStoreFile

### REQ-MCP-003 — koine mcp subcommands

- Status: implemented
- Statement: `koine mcp add NAME [--url URL [--bearer-token-env-var VAR] [--header N=V]… | -- COMMAND [ARGS…]] [--env N=V]… [--cwd DIR] [--exposure MODE] [--timeout S] [--description TEXT] [-l|--local]` writes a server to the user `mcp.json` (or `.koine/mcp.json` with `-l`) keeping the rest of the file; `remove NAME [-l]` deletes one; `list` connects to every enabled server, prints name, state, tool count, exposure and source, marks tools whose exposure differs from the server's, and exits 1 when an entry is invalid or an enabled server is not connected; `login NAME` and `logout NAME` manage OAuth tokens (REQ-MCP-002). Project servers are used by `list` and `login` only in a trusted folder (`-a`, `$KOINE_TRUST` or earlier trust). `-C DIR` selects the project. The subcommand loads no extensions, skills or models. The flag names beyond Pi's documented `--url`, `--bearer-token-env-var` and `--local/-l` are Koine's own. Gap row CLI-37.
- Design: DES-050
- Tests: TestMCPAddRemoveAndUsage; TestMCPList; TestMCPLoginAndLogout; TestAddAndRemoveServerKeepsTheRest

### REQ-PKG-001 — Install and manage Pi packages

- Status: implemented
- Statement: `koine install|remove|update|list|config [-l]` manages Pi packages from `npm:` (a pinned version is never moved), `git:` or URL (a tag or commit is never moved) and local-path sources. User packages live under the agent directory (`packages/`, with `packages.lock.json`), project packages under `.koine/packages/` (with a committed `.koine/packages.lock.json`). Installing, removing and updating a project package needs trust (ADR 0002) and `koine` loads project packages only from a trusted folder. npm installs run `npm install --ignore-scripts` unless the user opts in with `--allow-scripts` or `allowInstallScripts`; `npm` and `git` are the user's own programs and a missing one is a clear error. Every installed package is hashed (sha256 over its files) into the lockfile together with the version, commit and registry integrity; an install or update that finds the same version or commit with different bytes is refused, and a package whose files no longer match its lock entry is not loaded. Dependencies listed in `dependencies` that the host provides (`peerDependencies "*"` is the rule) are warned about. `update` prints the changelog sections between the old and new version of packages that carry extensions. Koine never installs at startup. Gap rows EXT-34, EXT-35, EXT-39, EXT-40, EXT-44, CLI-34.
- Design: DES-070
- Tests: TestParseSources; TestParseRejectsUnsafeOrUnknownSources; TestInstallNPMPinsRecordsAndLocks; TestIntegrityMismatchIsRefusedAndNotRecorded; TestProjectInstallsNeedTrust; TestNPMCommandSettingIsUsed; TestLoadWarnsAboutHostPackagesInDependenciesAndMissingLock; TestDependencyWarnings; TestUpdateShowsTheChangelogOfUpdatedPackages

### REQ-PKG-002 — Package resources reach the loaders

- Status: implemented
- Statement: A package supplies extensions, skills, prompts and themes through the `pi` key of its `package.json` (lists of paths and globs relative to the package, with `!` exclusions) or, without one, through the directories `extensions/`, `skills/`, `prompts/` and `themes/`. Entries that leave the package are refused. The `packages` setting takes source strings or objects whose `extensions`, `skills`, `prompts` and `themes` lists filter what loads (`!glob` excludes, `+path` adds back, `-path` removes, an empty list loads nothing); a project entry replaces a user entry of the same package, and `autoload: false` makes it a filter over the user entry. The resources feed the same extension, skill and prompt loading as the user and project directories, subject to `-ne`, `-ns` and `-np`. A subdirectory `package.json` with `pi.extensions` makes its entry points one extension (any extension directory). Gap rows EXT-02, EXT-36, EXT-37, EXT-38.
- Design: DES-070
- Tests: TestPiManifestDeclaresResources; TestConventionalDirectories; TestFilterSemantics; TestProjectEntryReplacesUserEntryOfTheSamePackage; TestAutoloadFalseNarrowsTheUserPackage; TestSubdirectoryPackageJSONListsExtensionEntries; TestSubdirectoryPackageJSONListsTheEntryPoints; TestPackageResourcesFeedTheLoaders; TestProjectPackagesStayBehindTheTrustGate

### REQ-PKG-003 — Provenance of loaded things and extension-contributed resources

- Status: implemented
- Statement: Every tool, slash command, skill and prompt carries a `SourceInfo` (path, source, scope, origin, baseDir) that says whether it is built in, from a package, found in a user or project directory or given on the command line; `/packages` lists loaded packages and `/sources [filter]` lists what each supplied. Extensions can answer the `resources_discover` event (sent after `session_start` and after a reload, with the working directory and the reason) with skill, prompt and theme paths, which the app loads like any other; this works for Pi's JS handlers through the sidecar and for any-language extensions through the wire protocol. Gap rows EXT-42, EXT-43.
- Design: DES-071
- Tests: TestSourceInfoIsAttachedToTheLoadedThings; TestResourcesDiscoverAndSourceInfo; TestResourcesDiscoverOnReload; TestMergeAddsUpDiscoveredResources; js:conformance: resources_discover returns skill, prompt and theme paths

### REQ-EXT-005 — Extensions can use the user interface

- Status: implemented
- Statement: Extension dialogs (`select`, `confirm`, `input`, `editor`) are answered by the active frontend (terminal prompts, or an `extension_ui_request` in RPC mode) and resolve at once as cancelled when there is none or when their `timeout` or `AbortSignal` ends. The text surfaces `setStatus`, `setWidget`, `setTitle`, `setWorkingMessage`, `setEditorText` and `notify` reach the frontend. `ctx.mode` (`tui`, `rpc`, `json`, `print`) and `ctx.hasUI` tell the truth, and `ctx.shutdown()` asks the host to quit. Components (`custom`, footers, headers, themes, renderers) stay inert (`compat/DIVERGENCES.md`). Gap rows EXT-24, 25, 28.
- Design: DES-073
- Tests: TestUIDialogsAreAnsweredByTheHost; TestDialogWithoutAFrontendIsCancelled; TestShutdownRequestReachesTheHost; TestExtensionSurfacesReachTheFrontend; TestDialogsUseTheFrontendsPrompts; js:ui: dialogs are requests; js:ui: without a frontend; js:ui: surfaces are notifications

### REQ-EXT-006 — Event handlers behave as in Pi

- Status: implemented
- Statement: A `tool_call` handler that changes `event.input` in place changes the arguments, and later handlers see the change. `tool_result` handlers chain: each sees the content, details and `isError` the earlier ones produced, and may replace text and image content. `input` handlers chain (transform, `handled`, images, and the real `source`: `interactive`, `rpc` or `extension`). `before_agent_start` runs once per prompt; its `systemPrompt` replaces the prompt for that run (chained) and its `message` is added to the conversation. A handler that throws is reported (`extension_error`) and the extension carries on, except a throwing `tool_call` handler, which blocks the call (gates fail closed, observers fail open). Failures of the host side (a timeout, an exited process) stay fail-open. Gap rows EXT-12..16.
- Design: DES-072
- Tests: js:events: handlers chain; TestChainCarriesChangesToTheNextHandler; TestBeforeAgentStartPromptsChainBetweenExtensions; TestMergeCarriesTheExtensionAPIFields; TestInputHandledBlockedAndTransformed; TestBeforeAgentStartReplacesThePromptForOneRunAndAddsAMessage; TestToolResultContentAndDetailsReplaceTheResult; TestContinueAnswersAMessageAnExtensionAdded; TestImagesFromPiKeepsOnlyWellFormedBlocks

### REQ-EXT-007 — Session lifecycle and tool execution events

- Status: implemented
- Statement: Extensions receive `session_shutdown` (reason `quit`, `reload`, `new`, `resume`, `fork`), `session_start` with the same reasons and `previousSessionFile`, the gates `session_before_switch`, `session_before_fork`, `session_before_compact` and `session_before_tree` (answering `{ cancel: true }` stops the operation), `session_compact`, `session_tree` and `tool_execution_start`, `_update` and `_end`. A custom summary returned from a gate is not applied. Gap row EXT-17; the events `message_*`, `context` and the provider hooks remain open.
- Design: DES-072
- Tests: TestSessionGatesCancelSwitchForkAndTree; TestSessionLifecycleEventsReachExtensions; TestCompactionGateAndEvent; TestToolExecutionEventsReachExtensions; TestMultiStopsAtCancel

### REQ-EXT-008 — Tool arguments are coerced and validated

- Status: implemented
- Statement: Before `tool_call` and before a tool runs, the arguments are coerced as Pi documents (a numeric string becomes a number, `"true"` and `"false"` become booleans, a single value becomes a one-item array, a null in an optional property is dropped) and checked against the tool's JSON Schema. A failure is an error result, `Invalid arguments for tool "x":` and one `- path: problem` line per problem, and the tool is not run. Keywords that are not understood never reject a call. Gap row EXT-45.
- Design: DES-072
- Tests: TestValidateCoercesLooseValuesTheWayPiDoes; TestValidateReportsEachProblemOnItsOwnLine; TestValidateNeverRejectsWhatItDoesNotUnderstand; TestInvalidArgumentsAreAnswerWithoutToolCallOrRun

### REQ-EXT-009 — Extension tools: abort, nested calls, overrides

- Status: implemented
- Statement: The `signal` of a tool's `execute` and `ctx.signal` of an event handler abort when the host gives up on the call (cancelled run, timeout). `ctx.executeTool(name, args)` and `ctx.tools` let a tool run the other tools of the session, with argument validation and the `tool_call` hook, and without entering the transcript. A tool an extension registers replaces a built-in of the same name, and `withFileMutationQueue` is exported by the `pi-coding-agent` shim. Gap rows EXT-19, 20.
- Design: DES-072
- Tests: TestToolAbortSignalFiresWhenTheCallIsCancelled; TestExtensionsCanRunToolsThroughTheHost; TestExecuteToolRunsToolsForExtensionsAndGuardsThem; TestAnExtensionToolReplacesABuiltInOfTheSameName; js:tools: a cancel notification; js:shim: withFileMutationQueue

### REQ-EXT-010 — Commands, custom messages and the event bus

- Status: implemented
- Statement: When several extensions register the same command name Koine keeps all of them and numbers them `name:1`, `name:2` in load order. `pi.getCommands()` lists extension commands, prompt templates and skills with their `sourceInfo`. A command's `getArgumentCompletions` completes in the terminal editor. `pi.sendMessage` adds a custom message to the conversation and the session (`custom_message` entry) with `triggerTurn` and `deliverAs` (`steer`, `followUp`, `nextTurn`). `pi.events` is one bus shared by all extensions. Message renderers stay inert. Gap rows EXT-22, 23 (partly), 29.
- Design: DES-072
- Tests: TestCommandCollisionsAreNumberedAndCompleted; TestGetCommandsListsExtensionsTemplatesAndSkills; TestSendMessageAddsACustomMessage; TestHostMessagesReachTheApp; TestEventBusReachesOtherExtensions; js:messages: sendMessage; js:bus: emits reach

### REQ-EXT-011 — Shim exports match upstream

- Status: planned
- Statement: The virtual modules (`pi-coding-agent`, `pi-tui`, `pi-ai`) export every runtime name upstream exports, so no extension fails to link on a missing named import. Gap row EXT-07.
- Design: none yet

### REQ-RPC-001 — RPC mode

- Status: implemented
- Statement: `koine --mode rpc` reads JSON commands, one per line, from stdin and writes a response for each, the JSON event stream (REQ-CLI-003) and the extension UI requests to stdout, as Pi's RPC documentation describes (prompt with `streamingBehavior`, steer, follow_up, abort, session, model, thinking, queue, compaction, bash, state, message, command, fork and tree commands). Extension dialogs are sent as `extension_ui_request` and answered by `extension_ui_response`. A line that does not parse gets a failed `parse` response. `@file` arguments are refused. `set_auto_retry` and `abort_retry` answer `success: false`, because Koine always retries transient provider errors. Gap rows CLI-07, EXT-26.
- Design: DES-073
- Tests: TestRPCAnswersEveryCommandWithOneResponse; TestRPCPromptRunsTheAgentAndSessionCommandsReadTheResult; TestRPCQueuesMessagesWhileTheAgentRuns; TestRPCExtensionDialogsAndShutdown; TestRPCModeRefusesFileArguments; TestRunModeTellsExtensionsHowKoineRuns

### REQ-TOOL-001 — Built-in tool parity, what remains

- Status: planned
- Statement: The built-in tools match Pi where Pi's docs and behaviour could be checked (REQ-TOOL-002..005). Still open: `read` of images with resizing and a model-support check (TOOL-03), the Windows `powershell` tool (TOOL-16), the `user_bash` extension event, and rendering of tool updates in the frontends.
- Design: none yet

### REQ-TOOL-002 — File tools: read, write, edit, ls

- Status: implemented
- Statement: `read` returns plain text (no line numbers), cut at 2000 lines or 50KB with a hint to continue with `offset=N`, reports a first line over the limit, a missing file, a binary file and an offset past the end, and drops a BOM. `write` creates parent directories. `edit` takes `edits[]` (older single `oldText`/`newText` and `old`/`new` still work), matches every `oldText` against the original file, refuses missing, duplicate and overlapping matches, falls back to fuzzy matching (smart quotes, dashes, unusual spaces, trailing whitespace), keeps the BOM and the CRLF or LF endings, changes nothing when any edit fails, and returns a unified diff with the first changed line. `edit` and `write` on one file run one after the other (symlink aliases share a queue). `ls` is sorted without regard to case, marks directories with `/`, includes dotfiles and stops at 500 entries. Paths accept `~`, a leading `@`, unicode spaces, and macOS screenshot and curly-apostrophe spellings. Gap rows TOOL-01, 02, 04, 07..11, 19, 20.
- Design: DES-040
- Tests: TestReadReturnsPlainTextWithoutLineNumbers; TestReadLimitSaysHowToContinue; TestReadTruncatesAt2000LinesAndSaysWhereToContinue; TestReadTruncatesAt50KB; TestReadFirstLineOverTheByteLimit; TestReadErrors; TestReadDropsABOMAndExpandsHome; TestWriteCreatesParentsAndOverwrites; TestApplyEditsMatchesAgainstTheOriginal; TestApplyEditsErrors; TestApplyEditsFuzzyMatching; TestLineEndingAndBOMHelpers; TestEditToolMultiEditDiffAndFirstChangedLine; TestEditToolKeepsBOMAndCRLF; TestEditToolAcceptsOlderArgumentShapes; TestEditToolErrorsLeaveTheFileAlone; TestEditAndWriteInParallelAreSerialisedPerFile; TestFileMutationQueueSerialisesOneFile; TestFileMutationQueueSharesSymlinkAliasesAndSeparatesFiles; TestUnifiedDiff; TestUnifiedDiffOfAHugeChangeFallsBackToOneBlock; TestExpandAndResolvePath; TestResolveReadPathFindsMacOSAndCurlyNames; TestLs; TestLsIsCappedAt500Entries; TestTruncateHead; TestTruncateTail; TestTruncateLine; TestFormatSize

### REQ-TOOL-003 — Search tools: grep, find

- Status: implemented
- Statement: `grep` takes `pattern`, `path`, `glob`, `ignoreCase`, `literal`, `context` and `limit` (100), prints `path:line: text` with context as `path-line- text`, cuts lines at 500 characters, stops at 50KB and says which limit was reached. It uses ripgrep when `rg` is installed and a Go search with the same options otherwise; both respect `.gitignore`, include dotfiles and skip `.git` and binary files. `find` (formerly `glob`, still accepted as a name) matches a glob (with `**`, `?`, `[…]`, `{a,b}`) against names or paths, respects `.gitignore` files of the tree and of the parent directories up to the repository root, includes dotfiles, marks directories with `/` and stops at 1000 results. Gap rows TOOL-17, 18.
- Design: DES-040
- Tests: TestFind; TestFindHonoursGitignoreInNestedAndParentDirectories; TestGrepGoFallback; TestGrepContextAndSeparators; TestGrepLimitAndLineCap; TestGrepByteLimit; TestGrepWithRipgrep; TestGrepWithRipgrepErrors; TestGrepUsesTheRealRipgrepWhenInstalled; TestIgnorePatterns; TestBraceExpansion

### REQ-TOOL-004 — bash: truncation, abort, shell settings and user commands

- Status: implemented
- Statement: `bash` returns the last 2000 lines or 50KB of the combined output with a note that names a temp file holding the whole output, strips terminal escapes, enforces a timeout (default 120 seconds), kills the shell and everything it started on timeout or abort, reports partial output through the tool-update callback while it runs, and runs under `shellPath` with `shellCommandPrefix` put before the command. Those two settings, like other code-running settings, apply only from the user file or a trusted project. In interactive frontends `!cmd` runs a command and adds its output to the conversation, `!!cmd` runs it without telling the model; print, JSON and server runs treat such text as an ordinary prompt. Gap rows TOOL-12..15, CMD-03.
- Design: DES-040
- Tests: TestBashRunsInTheWorkingDirectoryAndReportsFailures; TestBashTimeoutKillsTheWholeProcessTree; TestBashAbortKillsTheCommand; TestBashKeepsTheTailAndSavesTheFullOutput; TestBashByteLimitAndALongLastLine; TestBashOutputBeyondTheMemoryWindowStillReachesTheFile; TestBashStripsTerminalEscapes; TestBashReportsPartialOutputWhileRunning; TestShellPathAndCommandPrefix; TestShellErrors; TestUserBashHasNoDefaultTimeout; TestFormatShellResult; TestShellSettingsReachTheBashToolAndUserBash; TestUntrustedProjectCannotChooseTheShell; TestUserBashAddsOutputToTheConversation; TestDoubleBangRunsWithoutTellingTheModel; TestUserBashCanBeCancelledAndSwitchedOff

### REQ-TOOL-005 — Default tools and a structured system prompt

- Status: implemented
- Statement: `read`, `bash`, `edit` and `write` are the tools offered by default; `grep`, `find` and `ls` are registered but off until `-t`, `+name` or `defaultTools` names them. A tool definition may carry `promptSnippet` (one line in the system prompt's `<tools>` section) and `promptGuidelines` (bullets in `<rules>`, only while the tool is active); extension tools registered through the sidecar pass both on. The default system prompt is the preamble, `<tools>`, `<rules>`, `<project_context>`, the skills and `<environment>` (working directory and date); a custom `SYSTEM.md` replaces the preamble, `<tools>` and `<rules>`. Gap rows TOOL-21, 22, SYS-01.
- Design: DES-040
- Tests: TestBuiltinsAreTheSevenPiTools; TestDefaultToolsAreReadBashEditWrite; TestToolSelectionCombinesSettingAndOptions; TestPromptPartsListSnippetsAndDedupeGuidelines; TestSystemPromptHasToolsRulesAndEnvironmentSections; TestSystemPromptRulesFollowTheActiveTools; TestCustomBaseReplacesThePreambleAndToolSections; TestAddUserContextIsPersistedAndSentToTheModel; TestRegisteredToolsCarryPromptSnippetAndGuidelines; TestFilteredAndCanon

## Tracking upstream Pi

### REQ-PI-001 — Pinned baseline and an honest manifest

- Status: implemented
- Statement: Koine is pinned to one upstream Pi release. Every Pi surface item has a status; `verified` requires a named, existing test; the baseline in the binary equals the manifest's.
- Design: DES-006
- Tests: TestManifestIsValid; TestValidateRules; TestVerifiedItemsNameRealTests; TestPiBaselineMatchesManifest; js:committed surface.json equals

### REQ-PI-002 — Detect upstream drift

- Status: implemented
- Statement: A script diffs Pi's published extension types against the manifest, reports additions and removals, fails loudly when it cannot extract, and can rebase the manifest without losing history.
- Design: DES-006
- Tests: js:extract + diff against the real manifest; js:rebase adds unsupported items; js:CLI: fixture package drifts; js:CLI: extraction failure exits 3

### REQ-PI-003 — Report compatibility to users

- Status: implemented
- Statement: `koine -compat` and `/compat` tell users what is verified, inert or unsupported, and which APIs their extensions actually hit.
- Design: DES-006
- Tests: TestCompatFlagPrintsReport; TestReport; TestVersionAndUntrustedByDefault

## Engineering process

### REQ-QA-001 — Quality gates are enforced by automation

- Status: implemented
- Statement: Format, lint, line and file length, coverage, dependency audit, vulnerability scan and security scan run in CI for Go and JS, and a single check blocks merging.
- Design: DES-009
- Tests: js:flags long lines; js:flags long files; js:main exits 1 on a violation

### REQ-DOC-001 — Requirements, design and tests stay linked

- Status: implemented
- Statement: Requirement IDs, design IDs, ADR status and doc links are machine-checked; a change to product code without a doc change fails review unless it says `Docs-Impact: none`.
- Design: DES-009
- Tests: js:docscheck validates; js:docscheck rejects; js:docscheck living-docs rule

### REQ-AGENTDEV-001 — The repository is ready for coding agents

- Status: implemented
- Statement: One `AGENTS.md`, shared skills, sub-agents and commands work in Claude Code, opencode, Pi and Koine, and Koine itself can load them.
- Design: DES-009
- Tests: TestRepoAgentConfigLoads
