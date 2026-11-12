package rpcclient

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func ev(s string) Event {
	var h struct{ Type string }
	_ = json.Unmarshal([]byte(s), &h)
	return Event{Type: h.Type, Raw: json.RawMessage(s)}
}

func apply(t *Transcript, lines ...string) {
	for _, l := range lines {
		t.Apply(ev(l))
	}
}

func TestStreamedRunBecomesEntries(t *testing.T) {
	tr := NewTranscript()
	apply(tr,
		`{"type":"agent_start"}`,
		`{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"list files"}]}}`,
		`{"type":"message_start","message":{"role":"assistant","content":[]}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"hm"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_end"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Let me "}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"look."}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","delta":"{"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Let me look."}]}}`,
		`{"type":"tool_execution_start","toolCallId":"t1","toolName":"bash","args":{"command":"ls"}}`,
	)
	if !tr.Busy {
		t.Error("should be busy mid-run")
	}
	got := tr.Apply(ev(`{"type":"tool_execution_update","toolCallId":"t1","toolName":"bash","partialResult":{"content":[{"type":"text","text":"a"}]}}`))
	if len(got) != 1 || tr.Entries[got[0]].Done || tr.Entries[got[0]].Text != "a" {
		t.Errorf("update: %v %+v", got, tr.Entries)
	}
	apply(tr,
		`{"type":"tool_execution_end","toolCallId":"t1","toolName":"bash","result":{"content":[{"type":"text","text":"a\nb"}]},"isError":false}`,
		`{"type":"agent_settled","aborted":false}`,
	)
	if tr.Busy {
		t.Error("settled")
	}
	want := []Entry{
		{Kind: KindUser, Text: "list files"},
		{Kind: KindThinking, Text: "hm"},
		{Kind: KindAssistant, Text: "Let me look."},
		{Kind: KindTool, Tool: "bash", CallID: "t1", Args: `{"command":"ls"}`, Text: "a b", Done: true},
	}
	if !reflect.DeepEqual(tr.Entries, want) {
		t.Errorf("entries:\n got %+v\nwant %+v", tr.Entries, want)
	}
}

func TestUnstreamedMessagesErrorsAndMissedToolStart(t *testing.T) {
	tr := NewTranscript()
	apply(tr,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":"plain string"}}`,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"429 slow down"}}`,
		`{"type":"message_end","message":{"role":"user","content":[]}}`, // nothing to show
		`{"type":"message_end","message":{"role":"toolResult","content":[]}}`,
		`{"type":"tool_execution_end","toolCallId":"late","toolName":"read","result":{"content":[{"type":"text","text":"boom"}]},"isError":true}`,
		`{"type":"tool_execution_end","toolCallId":"nil","toolName":"read"}`,
		`{"type":"extension_error","error":"x failed"}`,
		`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"errorMessage":"overloaded"}`,
		`{"type":"compaction_start","reason":"threshold"}`,
		`{"type":"compaction_end","errorMessage":"too big"}`,
		`{"type":"compaction_end","result":{}}`,
		`{"type":"stderr","text":"just a notice"}`,
		`{"type":"stderr","text":"koine: error: bad thing"}`,
		`{"type":"stderr","text":""}`,
	)
	var kinds []Kind
	for _, e := range tr.Entries {
		kinds = append(kinds, e.Kind)
	}
	want := []Kind{KindAssistant, KindError, KindTool, KindTool, KindError, KindNotice, KindNotice, KindError, KindNotice, KindError}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds %v\nwant  %v", kinds, want)
	}
	if e := tr.Entries[2]; !e.IsError || e.Text != "boom" || !e.Done {
		t.Errorf("late tool: %+v", e)
	}
	if tr.Entries[1].Text != "429 slow down" || !strings.Contains(tr.Entries[4].Text, "x failed") {
		t.Errorf("%+v", tr.Entries)
	}
	if got := tr.Apply(Event{Type: "x", Raw: json.RawMessage(`[1]`)}); got != nil {
		t.Errorf("an undecodable event is ignored: %v", got)
	}
}

func TestExtensionUIRequests(t *testing.T) {
	tr := NewTranscript()
	apply(tr,
		`{"type":"extension_ui_request","id":"ui-1","method":"select","title":"Pick","options":["a","b"]}`,
	)
	if d := tr.Dialog; d == nil || d.ID != "ui-1" || d.Title != "Pick" || len(d.Options) != 2 {
		t.Fatalf("dialog %+v", tr.Dialog)
	}
	if got := tr.Dialog.Reply("b", false); !reflect.DeepEqual(got, map[string]any{"value": "b"}) {
		t.Errorf("reply %v", got)
	}
	if got := (Dialog{Method: "confirm"}).Reply("", true); !reflect.DeepEqual(got, map[string]any{"confirmed": true}) {
		t.Errorf("confirm %v", got)
	}
	if got := tr.Dialog.Cancel(); got["cancelled"] != true {
		t.Errorf("cancel %v", got)
	}
	apply(tr,
		`{"type":"extension_ui_request","id":"ui-2","method":"notify","message":"hello"}`,
		`{"type":"extension_ui_request","id":"ui-3","method":"notify","message":"bad","notifyType":"error"}`,
		`{"type":"extension_ui_request","id":"ui-4","method":"setStatus","statusKey":"b","statusText":"two"}`,
		`{"type":"extension_ui_request","id":"ui-5","method":"setStatus","statusKey":"a","statusText":"one"}`,
		`{"type":"extension_ui_request","id":"ui-6","method":"setStatus","statusKey":"gone","statusText":"x"}`,
		`{"type":"extension_ui_request","id":"ui-7","method":"setStatus","statusKey":"gone"}`,
		`{"type":"extension_ui_request","id":"ui-8","method":"setWidget","widgetKey":"w","widgetLines":["l1","l2"]}`,
		`{"type":"extension_ui_request","id":"ui-9","method":"setWidget","widgetKey":"w2","widgetLines":["x"]}`,
		`{"type":"extension_ui_request","id":"ui-10","method":"setWidget","widgetKey":"w2"}`,
		`{"type":"extension_ui_request","id":"ui-11","method":"setTitle","title":"T"}`,
		`{"type":"extension_ui_request","id":"ui-12","method":"set_editor_text","text":"prefilled"}`,
	)
	if tr.Entries[0].Kind != KindNotice || tr.Entries[1].Kind != KindError {
		t.Errorf("notify: %+v", tr.Entries)
	}
	if tr.StatusLine() != "one · two" || len(tr.Widgets) != 1 || len(tr.Widgets["w"]) != 2 || tr.Title != "T" {
		t.Errorf("status %q widgets %v title %q", tr.StatusLine(), tr.Widgets, tr.Title)
	}
	if tr.TakeEditorText() != "prefilled" || tr.TakeEditorText() != "" {
		t.Error("editor text is taken once")
	}
	if tr.Apply(ev(`{"type":"extension_ui_request","method":"setWorkingMessage"}`)) != nil {
		t.Error("unknown methods are ignored")
	}
}

func TestSummarize(t *testing.T) {
	if got := Summarize("  a \n b\t c ", 20); got != "a b c" {
		t.Errorf("%q", got)
	}
	if got := Summarize("héllo wörld", 4); got != "héll…" {
		t.Errorf("%q", got)
	}
}
