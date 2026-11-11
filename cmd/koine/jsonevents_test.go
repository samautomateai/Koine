package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"koine/core/agent"
)

// Every event in Pi's schema that comes from the agent or the app is written with Pi's type and field names.
func TestJSONModeWritesEveryPiEvent(t *testing.T) {
	var out bytes.Buffer
	j := newJSONStream(&out, nil)
	data := agent.Data
	for _, e := range []agent.Event{
		{Type: "queue_update", Data: data("steering", []string{"a"}, "followUp", []string{})},
		{Type: "entry_appended", Data: data("entry", json.RawMessage(`{"type":"custom","id":"1a2b3c4d","parentId":null,"customType":"x"}`))},
		{Type: "session_info_changed", Data: data("name", "my work")},
		{Type: "session_info_changed", Data: data()},
		{Type: "thinking_level_changed", Data: data("level", "high")},
		{Type: "compaction_start", Data: data("reason", "threshold")},
		{Type: "compaction_end", Data: data("reason", "manual", "aborted", false, "willRetry", false,
			"result", data("summary", "S", "firstKeptEntryId", "e1", "tokensBefore", 900, "estimatedTokensAfter", 300))},
		{Type: "compaction_end", Data: data("reason", "overflow", "aborted", true, "willRetry", false, "errorMessage", "stopped")},
		{Type: "auto_retry_start", Data: data("attempt", 1, "maxAttempts", 3, "delayMs", int64(2000), "errorMessage", "HTTP 503")},
		{Type: "auto_retry_end", Data: data("success", false, "attempt", 3, "finalError", "HTTP 503")},
		{Type: "auto_retry_end", Data: data("success", true, "attempt", 2)},
		{Type: "summarization_retry_scheduled", Data: data("attempt", 1, "maxAttempts", 2, "delayMs", int64(500), "errorMessage", "overloaded")},
		{Type: "summarization_retry_attempt_start", Data: data("source", "compaction", "reason", "manual")},
		{Type: "summarization_retry_attempt_start", Data: data("source", "branchSummary")},
		{Type: "summarization_retry_finished", Data: data()},
		{Type: "tool_execution_update", CallID: "c1", Tool: "bash", Args: json.RawMessage(`{"command":"make"}`), Text: "building"},
	} {
		j.Event(e)
	}
	want := []string{
		`{"type":"queue_update","followUp":[],"steering":["a"]}`,
		`{"type":"entry_appended","entry":{"type":"custom","id":"1a2b3c4d","parentId":null,"customType":"x"}}`,
		`{"type":"session_info_changed","name":"my work"}`,
		`{"type":"session_info_changed"}`,
		`{"type":"thinking_level_changed","level":"high"}`,
		`{"type":"compaction_start","reason":"threshold"}`,
		`{"type":"compaction_end","aborted":false,"reason":"manual","result":{"estimatedTokensAfter":300,"firstKeptEntryId":"e1","summary":"S",` +
			`"tokensBefore":900},"willRetry":false}`,
		`{"type":"compaction_end","aborted":true,"errorMessage":"stopped","reason":"overflow","willRetry":false}`,
		`{"type":"auto_retry_start","attempt":1,"delayMs":2000,"errorMessage":"HTTP 503","maxAttempts":3}`,
		`{"type":"auto_retry_end","attempt":3,"finalError":"HTTP 503","success":false}`,
		`{"type":"auto_retry_end","attempt":2,"success":true}`,
		`{"type":"summarization_retry_scheduled","attempt":1,"delayMs":500,"errorMessage":"overloaded","maxAttempts":2}`,
		`{"type":"summarization_retry_attempt_start","reason":"manual","source":"compaction"}`,
		`{"type":"summarization_retry_attempt_start","source":"branchSummary"}`,
		`{"type":"summarization_retry_finished"}`,
		`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","args":{"command":"make"},` +
			`"partialResult":{"content":[{"text":"building","type":"text"}],"details":{}}}`,
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != len(want) {
		t.Fatalf("%d records, want %d:\n%s", len(got), len(want), out.String())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}

func TestJSONModeReportsNameThinkingAndSessionEntries(t *testing.T) {
	isolate(t, "http://127.0.0.1:1")
	code, out, errs := cli("", "-C", t.TempDir(), "--mode", "json", "/name my work")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	recs := jsonLines(t, out)
	k := kinds(recs)
	if strings.Join(k, " ") != "session agent_start entry_appended session_info_changed agent_end agent_settled" {
		t.Fatalf("sequence: %v", k)
	}
	if recs[3]["name"] != "my work" {
		t.Errorf("session_info_changed: %v", recs[3])
	}
	if e := recs[2]["entry"].(map[string]any); e["type"] != "session_info" || e["name"] != "my work" || e["timestamp"] == "" || len(e["id"].(string)) != 8 {
		t.Errorf("entry: %v", e)
	}
	_, out, _ = cli("", "-C", t.TempDir(), "--mode", "json", "/thinking high")
	recs = jsonLines(t, out)
	if recs[3]["type"] != "thinking_level_changed" || recs[3]["level"] != "high" || recs[2]["entry"].(map[string]any)["type"] != "thinking_level_change" {
		t.Errorf("thinking: %v", kinds(recs))
	}
	// --no-session: the change is still reported, there is no entry to append
	_, out, _ = cli("", "-C", t.TempDir(), "--mode", "json", "--no-session", "/thinking low")
	if k = kinds(jsonLines(t, out)); strings.Join(k, " ") != "session agent_start thinking_level_changed agent_end agent_settled" {
		t.Errorf("no session: %v", k)
	}
}

func TestJSONModeAssistantMessagesCarryApiCostAndThinkingLevel(t *testing.T) {
	api := newRecorder(t, "", nil)
	isolate(t, api.URL)
	code, out, errs := cli("", "-C", t.TempDir(), "--mode", "json", "--thinking", "low", "hi")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	var msg map[string]any
	for _, r := range jsonLines(t, out) {
		if m, ok := r["message"].(map[string]any); ok && r["type"] == "message_end" && m["role"] == "assistant" {
			msg = m
		}
	}
	if msg == nil || msg["api"] != "anthropic-messages" {
		t.Fatalf("assistant message: %v", msg)
	}
	cost, ok := msg["usage"].(map[string]any)["cost"].(map[string]any)
	if !ok || cost["total"] == nil || cost["input"] == nil || cost["output"] == nil || cost["cacheRead"] == nil || cost["cacheWrite"] == nil {
		t.Errorf("usage.cost must always be there: %v", msg["usage"])
	}
	if apiOf("openai") != "openai-completions" {
		t.Errorf("other providers speak chat completions")
	}
}
