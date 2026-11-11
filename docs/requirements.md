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
- Statement: Network errors, timeouts, 429 and 5xx are retried, by default four attempts with waits of 2, 4 and 8 seconds; other 4xx are not, and neither is a 429 that reports an exhausted quota or credit. A `Retry-After` is waited for, unless it is longer than allowed. `retry.enabled`, `retry.maxRetries`, `retry.baseDelayMs`, `retry.maxAgentDelayMs`, `retry.provider.timeoutMs`, `retry.provider.maxRetryDelayMs` and `httpIdleTimeoutMs` tune this (REQ-LLM-005), and each retry is reported as a notice.
- Design: DES-007; DES-016
- Tests: TestRetriesOn429ThenSucceeds; TestClientErrorIsNotRetried; TestOpenAIErrors; TestRetrySettingsControlAttempts; TestRetryAfterIsHonouredUpToTheLimit; TestQuotaErrorsAreNotRetried; TestRetryNoticesAndCancellation

### REQ-LLM-003 — Streaming responses

- Status: planned
- Statement: Provider responses stream token by token to every frontend.
- Design: none yet

## Agent and frontends

### REQ-AGENT-001 — Tool-using agent loop with sub-agents

- Status: implemented
- Statement: The agent alternates model turns and tool calls until done, honours a turn limit and cancellation, and can delegate to named sub-agents through a `task` tool.
- Design: DES-001
- Tests: TestLoopRunsToolsAndStops; TestTaskToolSubAgents; TestMaxTurnsProviderErrorAndCancel

### REQ-FE-001 — One core, many frontends over HTTP/SSE

- Status: implemented
- Statement: The core serves prompts as server-sent events so a desktop shell or browser can drive the same agent as the terminal, one run at a time.
- Design: DES-008
- Tests: TestPromptStreamsSSE; TestConcurrentRunRejected; TestServeMode; TestPromptRejectsBadBody

### REQ-FE-002 — Bubble Tea TUI and Wails desktop app build and are tested in CI

- Status: planned
- Statement: The two frontends compile and have tests; today they are written but unbuilt.
- Design: DES-008

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
- Statement: Tools can be allow-listed with names or `*` patterns (`-t`), adjusted with `+name`/`-name` entries, switched off in part (`-nbt`, which keeps extension and custom tools) or by pattern (`-xt`, applied last). Resources can be added per run (`-e`, `--skill`, `--prompt-template`, all repeatable) or switched off (`-ne`, `-ns`, `-np`, `-nc`); explicit paths load even with discovery off. `-e` runs code, so it obeys the trust gate like every other extension (ADR 0002): in an untrusted folder it is skipped with a warning; `--skill` and `--prompt-template` are data (still audited for hidden text) and load anyway. `--theme`, `--use-theme` and `--no-themes` are accepted and reported as unused. `--model` matches an exact id, `provider/id` or a fuzzy id/name (preferring a provider with credentials), `--provider` narrows it, `--models` and `enabledModels` give the startup scope (first match when no model is configured), `--api-key` overrides the key for the run and `--list-models [search]` prints the catalog. Long options that an extension registered with `pi.registerFlag` are accepted, handed to `pi.getFlag`, and listed by `--help`; any other unknown option is a usage error. An optional `:level` suffix on `--model` or a `--models` pattern is read after the match; the thinking level itself is handled with `--thinking`. Gap rows CLI-10 (extension flags in help), CLI-11, CLI-12 (fuzzy), CLI-13, CLI-29, CLI-30.
- Design: DES-030
- Tests: TestParseResourceArgs; TestParseWithExtensionTypes; TestToolOptionsEndToEnd; TestExtensionAndResourceFlags; TestExtensionRegisteredFlags; TestModelSelectionOptions; TestEnabledModelsSettingPicksTheStartupModel; TestAPIKeyOverridesTheEnvironmentForOneRun; TestListModels; TestMatchNamesAndGlobs; TestLoadPathsFilesDirectoriesAndProblems; TestMergeKeepsFirstOfEachName; TestResolveModelMatchesIdsNamesAndProviders; TestFuzzyMatchPrefersCredentialsThenReportsAmbiguity; TestExpandScope; TestScopeComesFromFlagOrSettingAndIsWarnedAbout; TestAPIKeyStandsInForTheProvidersVariableOnly; TestToolSelectionCombinesSettingAndOptions; TestExplicitResourcesAndTheirProblems; TestExtensionFlagsReachTheHostAndSurviveReload; TestContextFilesCanBeSwitchedOff; TestExplicitExtensionsObeyTheTrustGate; TestListModelsAndTokens; TestExtensionFlagsAreRegisteredAndReceiveTheirValues; js:runner: registerFlag and getFlag

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
- Statement: Koine's user-level directory is `~/.koine` unless `$KOINE_AGENT_DIR` names another (the counterpart of Pi's `PI_CODING_AGENT_DIR`) and holds settings, sessions, skills, agents, prompts, extensions, trust and context files. `SYSTEM.md` replaces and `APPEND_SYSTEM.md` extends the system prompt: a trusted project's file wins over the user's and the two are never combined; project files are ignored without trust; the command-line options win over both. Gap rows CLI-31, CLI-41, SYS-02, CLI-52 to CLI-54.
- Design: DES-015
- Tests: TestOf; TestAgentDirOverrideMovesSettingsSessionsAndPrompts; TestProjectPromptFilesNeedTrustAndOverrideTheUsersWithoutCombining; TestPiAgentDirIsReadAsAFallbackAndBlankFilesAreIgnored

### REQ-LLM-004 — Model catalog, token usage and cost

- Status: implemented
- Statement: Koine knows each model's context window, output limit and price from a built-in catalog that the user can extend or override with `~/.koine/model-catalog.json`. Every model response's token usage (input, output, cache read, cache write) is recorded on the assistant message, saved in the session file in Pi's `usage` shape and rebuilt on resume. Cost is computed when the model's price is known (with long-context pricing); otherwise it is reported as unknown, never as zero. Compaction decides from the provider's reported context size plus an estimate of what was added since, and takes the window from the catalog unless `compaction.contextWindow` is set. A `usage` event reports each response; `/session` prints totals, cost and context use. Gap rows LLM-11, LLM-28, CMP-07, part of LLM-31 and CLI-22.
- Design: DES-014
- Tests: TestEmbeddedCatalogIsValid; TestLookupAcceptsProviderPrefixAndDatedSuffix; TestCostOfUsage; TestUsageArithmetic; TestMergeReplacesAddsAndRejects; TestProvidersReportUsage; TestUsageIsRecordedPricedAndReported; TestUnknownModelHasNoCostAndDefaultWindow; TestCompactionUsesTheProvidersCountAndResetClearsIt; TestUsageIsStoredInPiShapeAndRestored; TestSessionShowsUsageAndCatalogFileOverrides

### REQ-LLM-005 — Thinking levels and blocks, prompt caching, retry settings

- Status: implemented
- Statement: Thinking levels `off`, `minimal`, `low`, `medium`, `high`, `xhigh` and `max` are set with `--thinking`, a `:level` suffix on `--model` or `/model`, `/thinking`, and the settings `defaultThinkingLevel`, `modelThinkingLevels` (keyed by `provider/modelId`) and `thinkingBudgets`, in that order of precedence; the default is `medium`. The catalog says which models think and how (`reasoning`): Anthropic is asked with adaptive thinking and an effort, or with `budget_tokens`; OpenAI-compatible servers with `reasoning_effort`. A model the catalog does not know gets thinking only when a level was chosen for it. Levels a model does not accept are lowered to its highest. Thinking blocks, including redacted ones, are kept in the history, replayed to Anthropic with their signatures (not when thinking is disabled), saved in the session file in Pi's shape (`thinking`, `thinkingSignature`, `redacted`, and `thinkingLevel` on the message) and restored on resume; `hideThinkingBlock` keeps their text out of the events. Anthropic prompt caching marks the system prompt, the last tool and the last user message; cache reads and writes are reported through `Usage`. Per-provider context-overflow wording feeds the overflow retry with compaction. `cacheWarming` is not implemented. Gap rows LLM-22, 23, 25, 29 to 33, 35, CLI-14, AGT-08.
- Design: DES-016
- Tests: TestThinkingResolve; TestSplitThinkingSuffix; TestReasoningCatalogFlag; TestAnthropicThinkingRequests; TestAnthropicThinkingBlocksAreKeptAndReplayedWithSignatures; TestAnthropicPromptCachingBreakpoints; TestOpenAIReasoningEffortAndReasoningContent; TestIsOverflowPerProvider; TestRouterGivesItsRetryPolicyToProviders; TestThinkingLevelReachesTheRequestAndTheMessage; TestHideThinkingKeepsTheBlocksButNotTheEvents; TestNoThinkingForModelsThatDoNotThink; TestSubAgentsInheritThinkingSettings; TestRetriesAreReportedAsNotices; TestThinkingBlocksArePersistedInPiShapeAndRestored; TestEmptyThinkingBlocksAreNotRestored; TestThinkingSettingsDecideTheLevelPerModel; TestHideThinkingBlockSetting; TestThinkingFlagSuffixAndSlashCommand; TestRetryAndTimeoutSettingsMapToThePolicy; TestLaterSettingsFilesOverrideAndUntrustedProjectsKeepHarmlessOnes; TestThinkingFlagParsing; TestThinkingFlagAndModelSuffixReachTheRequest; TestHideThinkingBlockSettingKeepsThinkingOutOfPrintMode

### REQ-LLM-006 — Image input

- Status: implemented
- Statement: Messages can carry image blocks (base64 data and a media type). Anthropic gets `image` blocks with a `base64` source, and a tool result with images as a content array of text and images; OpenAI-compatible providers get `image_url` data-URL parts, and the images of a tool result follow it as a user message (tool messages cannot hold images). The `read` tool returns an image file (sniffed by content: PNG, JPEG, GIF, WebP, BMP) as an attachment of its result with a text line `Read image file [type]`. With `images.autoResize` (default true) a PNG, JPEG or GIF over 2000 by 2000 pixels, or over 4.5 MB once encoded, is scaled down with the standard library (a GIF becomes a PNG of its first frame; a small GIF is kept as it is); WebP and BMP cannot be decoded without a dependency, so they are sent unchanged, with a note, and refused when over the byte cap. A model whose catalog entry lists `input` without `image` gets no images: each becomes the text `[image omitted: …]` and the user is told once; a model the catalog does not know, or lists without `input`, is allowed. `images.blockImages` does the same for every model. The history and the session file keep the images, so a later switch to a model that reads them sends them again. Images count 1200 tokens in estimates. Sessions store them in Pi's shape (`{"type":"image","data","mimeType"}` in user and toolResult content) and replay them; `--mode json` writes them in message content. Wire formats are tested against fake servers only. Gap rows LLM-27, TOOL-03, CLI-16; settings `images.autoResize` and `images.blockImages`.
- Design: DES-032
- Tests: TestExpandFilesMakesImagesFromContentAndKeepsTextFiles; TestPipedInputIsAnImageOrText; TestImageSettingsMapToTheAgentAndProjectOverridesUser; TestAnUntrustedProjectMayStillSetTheImageSettings; TestHandleInputWithImagesSendsThemAndIgnoresThemForSlashCommands; TestAnthropicSendsImageSourceBlocks; TestToolResultWithoutImagesStaysAString; TestOpenAISendsImageURLParts; TestOpenAITextOnlyUserMessageIsStillAString; TestStripImagesReplacesThemWithANoteAndLeavesTheHistoryAlone; TestAcceptsImagesFollowsTheCatalogAndAllowsUnknownModels; TestImageBlockAccessors; TestSniffGoesByContentNotExtension; TestPrepareKeepsSmallImagesUnchanged; TestPrepareResizesToTheDocumentedLimit; TestPrepareWithoutResizeSendsTheOriginal; TestPrepareJPEGStaysJPEGAndGIFBecomesPNG; TestPrepareWebPAndBMPPassThroughAndSayWhy; TestPrepareShrinksUntilThePayloadFits; TestPrepareReportsCorruptImages; TestReadFile; TestReadAttachesImagesByContentNotExtension; TestReadResizesLargeImagesAndSaysSo; TestReadImageWithoutASinkSaysItWasNotAttached; TestReadStillReadsTextAndReportsBadImages; TestPromptImagesJoinTheUserMessageAndReachTheProvider; TestToolImagesRideInTheToolResultAndFollowTheResizeSetting; TestImagesAreLeftOutForModelsThatCannotReadThem; TestBlockImagesSettingKeepsEveryImageFromTheProvider; TestImageBlocksAreStoredInPisShapeAndReplayed; TestAnImageOnlyUserMessageIsKeptAndImagesWithoutDataAreSkipped; TestImagesSurviveCloneByteForByte; TestImagesCountForTokensAndAppearInTheTranscript; TestImageFileArgumentIsSentAsAnImageAndSaved; TestImageAloneIsAPromptAndLargeImagesAreResized; TestAutoResizeAndBlockImagesSettings; TestPipedImageDataIsAnImage; TestBadImageArgumentsAreUsageErrors; TestReadToolSendsImagesBackAndJSONModeShowsThem; TestJSONModeShowsUserImages

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

- Status: planned
- Statement: Koine connects to MCP servers over stdio and streamable HTTP from `mcp.json` (also reading `.mcp.json` and opencode config), exposes their tools as `mcp__server__tool`, and treats project-scoped servers as untrusted code. Gap rows LLM-42..47.
- Design: none yet

### REQ-PKG-001 — Install Pi packages

- Status: planned
- Statement: `koine install|remove|update|list` handles npm, git and local Pi packages with a `pi` manifest, feeds their extensions, skills and prompts to the loaders, pins by hash and never runs install scripts. Gap rows EXT-34..40.
- Design: none yet

### REQ-EXT-005 — Extensions can use the user interface

- Status: planned
- Statement: Extension dialogs (`select`, `confirm`, `input`) and text status surfaces reach the active frontend; `ctx.mode` and `ctx.hasUI` are truthful; shim exports match upstream so extensions never fail to link. Gap rows EXT-07, 24..26, 28.
- Design: none yet

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
