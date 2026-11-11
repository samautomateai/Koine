package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// jsonLines decodes the stdout of --mode json, failing the test on any line that is not a JSON object.
func jsonLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("not a JSON object: %q", line)
		}
		recs = append(recs, r)
	}
	return recs
}

func kinds(recs []map[string]any) []string {
	var k []string
	for _, r := range recs {
		s := r["type"].(string)
		if s == "message_update" {
			s += ":" + r["assistantMessageEvent"].(map[string]any)["type"].(string)
		}
		if m, ok := r["message"].(map[string]any); ok && (s == "message_start" || s == "message_end") {
			s += ":" + m["role"].(string)
		}
		k = append(k, s)
	}
	return k
}

func TestJSONModeFollowsPisEventStream(t *testing.T) {
	api := newRecorder(t, "bash", map[string]any{"command": "echo hi"})
	isolate(t, api.URL)
	dir := t.TempDir()
	code, out, errs := cli("", "-C", dir, "--mode", "json", "run it")
	if code != 0 || errs != "" {
		t.Fatalf("%d %q", code, errs)
	}
	recs := jsonLines(t, out)
	head := recs[0]
	if head["type"] != "session" || head["version"] != float64(3) || head["cwd"] == "" || len(head["id"].(string)) != 36 || head["timestamp"] == "" {
		t.Errorf("header: %v", head)
	}
	want := strings.Join([]string{
		"session", "agent_start", "turn_start", "message_start:user", "message_end:user", "entry_appended",
		"message_start:assistant", "message_update:toolcall_start", "message_update:toolcall_delta", "message_update:toolcall_end", "message_end:assistant",
		"entry_appended", "tool_execution_start", "tool_execution_update", "tool_execution_end", "message_start:toolResult", "message_end:toolResult",
		"entry_appended", "turn_end",
		"turn_start", "message_start:assistant", "message_update:text_start", "message_update:text_delta", "message_update:text_end", "message_end:assistant",
		"entry_appended", "turn_end", "agent_end", "agent_settled"}, " ")
	if got := strings.Join(kinds(recs), " "); got != want {
		t.Errorf("sequence:\n got %s\nwant %s", got, want)
	}
	byType := func(typ string) map[string]any {
		for _, r := range recs {
			if r["type"] == typ {
				return r
			}
		}
		t.Fatalf("no %s", typ)
		return nil
	}
	start, end := byType("tool_execution_start"), byType("tool_execution_end")
	if start["toolCallId"] != "call1" || start["toolName"] != "bash" || start["args"].(map[string]any)["command"] != "echo hi" {
		t.Errorf("tool_execution_start: %v", start)
	}
	res := end["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if end["isError"] != false || !strings.Contains(res["text"].(string), "hi") || end["durationMs"] == nil {
		t.Errorf("tool_execution_end: %v", end)
	}
	te := byType("turn_end")
	asst := te["message"].(map[string]any)
	call := asst["content"].([]any)[0].(map[string]any)
	if asst["role"] != "assistant" || asst["stopReason"] != "toolUse" || call["type"] != "toolCall" || call["name"] != "bash" || call["id"] != "call1" {
		t.Errorf("first turn_end message: %v", asst)
	}
	if trs := te["toolResults"].([]any); len(trs) != 1 || trs[0].(map[string]any)["role"] != "toolResult" || trs[0].(map[string]any)["toolCallId"] != "call1" {
		t.Errorf("toolResults: %v", te["toolResults"])
	}
	last := recs[len(recs)-2]
	if msgs := last["messages"].([]any); last["type"] != "agent_end" || len(msgs) != 4 || last["willRetry"] != false {
		t.Errorf("agent_end: %v", last)
	}
	if settled := recs[len(recs)-1]; settled["aborted"] != false {
		t.Errorf("agent_settled: %v", settled)
	}
	var text string
	for _, r := range recs {
		if m, ok := r["assistantMessageEvent"].(map[string]any); ok && m["type"] == "text_delta" {
			text += m["delta"].(string)
			if r["usage"] == nil || m["contentIndex"] != float64(0) {
				t.Errorf("message_update: %v", r)
			}
		}
	}
	if !strings.HasPrefix(text, "RESULT: hi") {
		t.Errorf("text delta: %q", text)
	}
}

func TestJSONModeReportsAFailedRunAsAnErrorMessage(t *testing.T) {
	bad := newRecorder(t, "", nil)
	bad.Close()
	isolate(t, bad.URL)
	code, out, errs := cli("", "-C", t.TempDir(), "--mode", "json", "hi")
	if code != 1 || !strings.Contains(errs, "error") {
		t.Fatalf("%d %q", code, errs)
	}
	recs := jsonLines(t, out)
	var msg map[string]any
	for _, r := range recs {
		if m, ok := r["message"].(map[string]any); ok && r["type"] == "message_end" && m["role"] == "assistant" {
			msg = m
		}
	}
	if msg == nil || msg["stopReason"] != "error" || msg["errorMessage"] == "" {
		t.Errorf("error message: %v", msg)
	}
	k := kinds(recs)
	if k[len(k)-1] != "agent_settled" || k[len(k)-2] != "agent_end" || k[len(k)-3] != "turn_end" {
		t.Errorf("a failed run must still close its turn and the run: %v", k)
	}
}

func TestJSONModeKeepsNoticesOffStdout(t *testing.T) {
	isolate(t, "http://127.0.0.1:1")
	code, out, errs := cli("", "-C", t.TempDir(), "--mode", "json", "/nosuchcommand")
	if code != 0 || !strings.Contains(errs, "unknown command") {
		t.Fatalf("%d %q", code, errs)
	}
	for _, r := range jsonLines(t, out) {
		if r["type"] == "notice" {
			t.Errorf("notice on stdout: %v", r)
		}
	}
	if k := kinds(jsonLines(t, out)); strings.Join(k, " ") != "session agent_start agent_end agent_settled" {
		t.Errorf("a run with no messages: %v", k)
	}
}
