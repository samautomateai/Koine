package llm

import (
	"encoding/json"
	"sort"
)

// OpenAIFlags are the `compat` flags of models.json that Koine acts on for Chat Completions servers. The others Pi documents
// (thinkingFormat, cacheControlFormat, routing preferences, ...) are read and reported as not acted on (UnusedCompat).
type OpenAIFlags struct {
	NoReasoningEffort bool   // supportsReasoningEffort: false — never send reasoning_effort
	NoStreamUsage     bool   // supportsUsageInStreaming: false — do not ask for stream_options.include_usage
	MaxTokensField    string // maxTokensField: "max_tokens" or "max_completion_tokens"
	ToolResultName    bool   // requiresToolResultName — tool messages carry the tool's name
	NonNullAssistant  bool   // requiresNonNullAssistantContent — an assistant message with no text sends "" instead of null
	ToolsWithHistory  bool   // requiresToolsWithToolHistory — send "tools": [] when the history has tool calls but no tools are offered
}

// openAIFlagKeys are the compat keys parseOpenAIFlags acts on.
var openAIFlagKeys = []string{"supportsReasoningEffort", "supportsUsageInStreaming", "maxTokensField", "requiresToolResultName",
	"requiresNonNullAssistantContent", "requiresToolsWithToolHistory"}

// parseOpenAIFlags reads the compat object. Keys it does not act on are returned (sorted) so the caller can say so.
func parseOpenAIFlags(compat map[string]json.RawMessage) (OpenAIFlags, []string) {
	var f OpenAIFlags
	boolOf := func(k string) (val, set bool) {
		var b bool
		if raw, ok := compat[k]; ok && json.Unmarshal(raw, &b) == nil {
			return b, true
		}
		return false, false
	}
	if v, set := boolOf("supportsReasoningEffort"); set {
		f.NoReasoningEffort = !v
	}
	if v, set := boolOf("supportsUsageInStreaming"); set {
		f.NoStreamUsage = !v
	}
	f.ToolResultName, _ = boolOf("requiresToolResultName")
	f.NonNullAssistant, _ = boolOf("requiresNonNullAssistantContent")
	f.ToolsWithHistory, _ = boolOf("requiresToolsWithToolHistory")
	if raw, ok := compat["maxTokensField"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil && (s == "max_tokens" || s == "max_completion_tokens") {
			f.MaxTokensField = s
		}
	}
	return f, UnusedCompat(compat, openAIFlagKeys)
}

// UnusedCompat lists the keys of compat that are not in acted.
func UnusedCompat(compat map[string]json.RawMessage, acted []string) []string {
	known := map[string]bool{}
	for _, k := range acted {
		known[k] = true
	}
	var out []string
	for k := range compat {
		if !known[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// hasToolHistory reports whether any message carries a tool call or a tool result.
func hasToolHistory(msgs []Message) bool {
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "tool_use" || b.Type == "tool_result" {
				return true
			}
		}
	}
	return false
}

// ActedCompatKeys returns the compat keys Koine acts on for an api ("" for a key list of all). Only Chat Completions has any.
func ActedCompatKeys(api string) []string {
	if api == APICompletions || api == "" {
		return openAIFlagKeys
	}
	return nil
}
