package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// streamingAPI is a fake Messages API that answers with server-sent events: first a thinking block, some text and a bash call, then (after
// the tool result) a text answer. With cut set, the first answer breaks off after its text. Fake servers only, not live APIs.
func streamingAPI(t *testing.T, cut bool) *httptest.Server {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		send := func(data string) { _, _ = fmt.Fprintf(w, "data: %s\n\n", data) }
		if calls.Add(1) == 1 {
			send(`{"type":"message_start","message":{"usage":{"input_tokens":3}}}`)
			send(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
			send(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`)
			send(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"s"}}`)
			send(`{"type":"content_block_stop","index":0}`)
			send(`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)
			send(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Let me "}}`)
			send(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"look."}}`)
			if cut {
				return
			}
			send(`{"type":"content_block_stop","index":1}`)
			send(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call1","name":"bash"}}`)
			send(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}`)
			send(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"echo hi\"}"}}`)
			send(`{"type":"content_block_stop","index":2}`)
			send(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`)
			send(`{"type":"message_stop"}`)
			return
		}
		send(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		send(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`)
		send(`{"type":"content_block_stop","index":0}`)
		send(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`)
		send(`{"type":"message_stop"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestJSONModeWritesStreamedDeltasAsMessageUpdates(t *testing.T) {
	isolate(t, streamingAPI(t, false).URL)
	code, out, errs := cli("", "-C", t.TempDir(), "--mode", "json", "run it")
	if code != 0 || errs != "" {
		t.Fatalf("%d %q", code, errs)
	}
	recs := jsonLines(t, out)
	want := strings.Join([]string{
		"session", "agent_start", "turn_start", "message_start:user", "message_end:user", "entry_appended",
		"message_start:assistant",
		"message_update:thinking_start", "message_update:thinking_delta", "message_update:thinking_end",
		"message_update:text_start", "message_update:text_delta", "message_update:text_delta", "message_update:text_end",
		"message_update:toolcall_start", "message_update:toolcall_delta", "message_update:toolcall_delta", "message_update:toolcall_end",
		"message_end:assistant", "entry_appended", "tool_execution_start", "tool_execution_update", "tool_execution_end", "message_start:toolResult",
		"message_end:toolResult", "entry_appended", "turn_end",
		"turn_start", "message_start:assistant", "message_update:text_start", "message_update:text_delta", "message_update:text_end",
		"message_end:assistant", "entry_appended", "turn_end", "agent_end", "agent_settled"}, " ")
	if got := strings.Join(kinds(recs), " "); got != want {
		t.Fatalf("sequence:\n got %s\nwant %s", got, want)
	}
	var deltas []string
	for _, r := range recs {
		ev, ok := r["assistantMessageEvent"].(map[string]any)
		if !ok {
			continue
		}
		switch ev["type"] {
		case "text_delta", "thinking_delta", "toolcall_delta":
			deltas = append(deltas, fmt.Sprintf("%s@%v=%s", ev["type"], ev["contentIndex"], ev["delta"]))
		case "toolcall_end":
			call := ev["toolCall"].(map[string]any)
			args := call["arguments"].(map[string]any)
			if call["id"] != "call1" || call["name"] != "bash" || args["command"] != "echo hi" || ev["contentIndex"] != float64(2) {
				t.Errorf("toolcall_end: %v", ev)
			}
		case "text_end":
			if ev["content"] != "Let me look." && ev["content"] != "done" {
				t.Errorf("text_end: %v", ev)
			}
		}
	}
	wantDeltas := `thinking_delta@0=hmm text_delta@1=Let me  text_delta@1=look. toolcall_delta@2={"command": toolcall_delta@2="echo hi"} text_delta@0=done`
	if got := strings.Join(deltas, " "); got != wantDeltas {
		t.Errorf("deltas:\n got %s\nwant %s", got, wantDeltas)
	}
	// message_end is authoritative and carries the whole message
	for _, r := range recs {
		if m, ok := r["message"].(map[string]any); ok && r["type"] == "message_end" && m["role"] == "assistant" {
			if c := m["content"].([]any); len(c) == 0 {
				t.Errorf("message_end without content: %v", m)
			}
			break
		}
	}
}

func TestJSONModeClosesAStreamThatFailedMidway(t *testing.T) {
	isolate(t, streamingAPI(t, true).URL)
	code, out, errs := cli("", "-C", t.TempDir(), "--mode", "json", "run it")
	if code != 1 || !strings.Contains(errs, "stream ended") {
		t.Fatalf("%d %q", code, errs)
	}
	recs := jsonLines(t, out)
	k := strings.Join(kinds(recs), " ")
	if !strings.Contains(k, "message_update:text_end message_end:assistant") || strings.Count(k, "message_start:assistant") != 1 {
		t.Errorf("sequence: %s", k)
	}
	for _, r := range recs {
		if m, ok := r["message"].(map[string]any); ok && r["type"] == "message_end" && m["role"] == "assistant" && m["stopReason"] != "error" {
			t.Errorf("a failed stream ends with an error message: %v", m)
		}
	}
}
