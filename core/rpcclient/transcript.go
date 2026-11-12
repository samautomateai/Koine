package rpcclient

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Kind says what an Entry of the transcript is.
type Kind string

// The kinds of transcript entry.
const (
	KindUser      Kind = "user"
	KindAssistant Kind = "assistant"
	KindThinking  Kind = "thinking"
	KindTool      Kind = "tool"
	KindNotice    Kind = "notice"
	KindError     Kind = "error"
)

// Entry is one block of the conversation as a frontend shows it. A tool call is one entry: Args when it starts, Text and IsError when it ends.
type Entry struct {
	Kind    Kind   `json:"kind"`
	Text    string `json:"text"`
	Tool    string `json:"tool,omitempty"`
	CallID  string `json:"callId,omitempty"`
	Args    string `json:"args,omitempty"`
	IsError bool   `json:"isError,omitempty"`
	Done    bool   `json:"done,omitempty"` // a tool call has finished
}

// Dialog is an extension asking the person something (extension_ui_request with method select, confirm, input or editor).
type Dialog struct {
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Title       string   `json:"title"`
	Message     string   `json:"message,omitempty"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Prefill     string   `json:"prefill,omitempty"`
}

// Reply is the extension_ui_response fields for an answer: the chosen or typed value, or for a confirm whether the person agreed.
func (d Dialog) Reply(value string, yes bool) map[string]any {
	if d.Method == "confirm" {
		return map[string]any{"confirmed": yes}
	}
	return map[string]any{"value": value}
}

// Cancel is the fields that decline the dialog.
func (d Dialog) Cancel() map[string]any { return map[string]any{"cancelled": true} }

// Transcript folds the server's events into what a screen shows: entries, whether the agent is busy, the dialog waiting for an answer and
// the status lines, widgets and title extensions set. It holds no UI code, so the Bubble Tea and Wails frontends share it and it is tested
// without either.
type Transcript struct {
	Entries []Entry
	Busy    bool
	Dialog  *Dialog           // the dialog waiting for an answer, if any
	Status  map[string]string // setStatus by key
	Widgets map[string][]string
	Title   string
	// EditorText is text an extension asked to put in the editor (set_editor_text); the frontend takes it with TakeEditorText.
	EditorText string

	text, thinking int // index of the entry being streamed, or -1
	streamed       bool
	calls          map[string]int
}

// NewTranscript returns an empty transcript.
func NewTranscript() *Transcript {
	return &Transcript{Status: map[string]string{}, Widgets: map[string][]string{}, text: -1, thinking: -1, calls: map[string]int{}}
}

// TakeEditorText returns the text an extension set for the editor and forgets it.
func (t *Transcript) TakeEditorText() string {
	s := t.EditorText
	t.EditorText = ""
	return s
}

// Add appends an entry and returns its index.
func (t *Transcript) Add(e Entry) int {
	t.Entries = append(t.Entries, e)
	return len(t.Entries) - 1
}

// Apply updates the transcript with one event and returns the indexes of the entries it added or changed.
func (t *Transcript) Apply(e Event) []int {
	var m struct {
		Message              json.RawMessage                    `json:"message"`
		AssistantEvent       struct{ Type, Delta string }       `json:"assistantMessageEvent"`
		ToolCallID           string                             `json:"toolCallId"`
		ToolName             string                             `json:"toolName"`
		Args                 json.RawMessage                    `json:"args"`
		Result               *struct{ Content json.RawMessage } `json:"result"`
		PartialResult        *struct{ Content json.RawMessage } `json:"partialResult"`
		IsError              bool                               `json:"isError"`
		Text                 string                             `json:"text"`
		Error                string                             `json:"error"`
		ErrorMessage         string                             `json:"errorMessage"`
		Reason               string                             `json:"reason"`
		Attempt, MaxAttempts int
	}
	if err := e.Decode(&m); err != nil {
		return nil
	}
	switch e.Type {
	case "agent_start":
		t.Busy = true
	case "agent_settled":
		t.Busy = false
		t.text, t.thinking = -1, -1
	case "message_start":
		if parseMessage(m.Message).Role == "assistant" {
			t.streamed, t.text, t.thinking = false, -1, -1
		}
	case "message_update":
		return t.delta(m.AssistantEvent.Type, m.AssistantEvent.Delta)
	case "message_end":
		return t.messageEnd(parseMessage(m.Message))
	case "tool_execution_start":
		i := t.Add(Entry{Kind: KindTool, Tool: m.ToolName, CallID: m.ToolCallID, Args: Summarize(string(m.Args), 200)})
		t.calls[m.ToolCallID] = i
		return []int{i}
	case "tool_execution_update":
		if m.PartialResult != nil {
			return t.finishTool(m.ToolCallID, m.ToolName, blocksText(m.PartialResult.Content), false, false)
		}
	case "tool_execution_end":
		var out string
		if m.Result != nil {
			out = blocksText(m.Result.Content)
		}
		return t.finishTool(m.ToolCallID, m.ToolName, out, m.IsError, true)
	case "extension_error":
		return []int{t.Add(Entry{Kind: KindError, Text: "extension error: " + m.Error})}
	case "auto_retry_start":
		return []int{t.Add(Entry{Kind: KindNotice, Text: fmt.Sprintf("retrying (%d/%d): %s", m.Attempt, m.MaxAttempts, m.ErrorMessage)})}
	case "compaction_start":
		return []int{t.Add(Entry{Kind: KindNotice, Text: "compacting the conversation (" + m.Reason + ")"})}
	case "compaction_end":
		if m.ErrorMessage != "" {
			return []int{t.Add(Entry{Kind: KindError, Text: "compaction failed: " + m.ErrorMessage})}
		}
	case "stderr":
		return t.stderr(m.Text)
	case "extension_ui_request":
		return t.uiRequest(e)
	}
	return nil
}

type rawMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
}

func parseMessage(raw json.RawMessage) rawMessage {
	var m rawMessage
	_ = json.Unmarshal(raw, &m)
	return m
}

func (t *Transcript) delta(kind, text string) []int {
	switch kind {
	case "text_delta":
		t.streamed = true
		return []int{t.stream(&t.text, KindAssistant, text)}
	case "thinking_delta":
		t.streamed = true
		return []int{t.stream(&t.thinking, KindThinking, text)}
	case "text_end":
		t.text = -1
	case "thinking_end":
		t.thinking = -1
	}
	return nil
}

func (t *Transcript) stream(at *int, k Kind, text string) int {
	if *at < 0 {
		*at = t.Add(Entry{Kind: k})
	}
	t.Entries[*at].Text += text
	return *at
}

func (t *Transcript) messageEnd(m rawMessage) []int {
	switch m.Role {
	case "user":
		if s := contentText(m.Content); s != "" {
			return []int{t.Add(Entry{Kind: KindUser, Text: s})}
		}
	case "assistant":
		var changed []int
		if !t.streamed { // a provider that does not stream sends the whole message at the end
			if s := contentText(m.Content); s != "" {
				changed = append(changed, t.Add(Entry{Kind: KindAssistant, Text: s}))
			}
		}
		if m.ErrorMessage != "" && m.StopReason == "error" {
			changed = append(changed, t.Add(Entry{Kind: KindError, Text: m.ErrorMessage}))
		}
		t.text, t.thinking, t.streamed = -1, -1, false
		return changed
	}
	return nil
}

func (t *Transcript) finishTool(id, name, out string, isErr, done bool) []int {
	i, ok := t.calls[id]
	if !ok { // the start was missed (a client that joined mid-run)
		i = t.Add(Entry{Kind: KindTool, Tool: name, CallID: id})
		t.calls[id] = i
	}
	en := &t.Entries[i]
	en.Text, en.IsError, en.Done = Summarize(out, 400), isErr, done
	if done {
		delete(t.calls, id)
	}
	return []int{i}
}

// stderr turns a line of the server's standard error into a notice, or an error for the server's own "koine: error:" lines.
func (t *Transcript) stderr(line string) []int {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil
	}
	if rest, ok := strings.CutPrefix(line, "koine: error:"); ok {
		return []int{t.Add(Entry{Kind: KindError, Text: strings.TrimSpace(rest)})}
	}
	return []int{t.Add(Entry{Kind: KindNotice, Text: line})}
}

func (t *Transcript) uiRequest(e Event) []int {
	var r struct {
		ID, Method, Title, Message, Placeholder, Prefill string
		Options                                          []string
		NotifyType                                       string
		StatusKey                                        string
		StatusText                                       *string
		WidgetKey                                        string
		WidgetLines                                      []string
		Text                                             string
	}
	if e.Decode(&r) != nil {
		return nil
	}
	switch r.Method {
	case "select", "confirm", "input", "editor":
		t.Dialog = &Dialog{ID: r.ID, Method: r.Method, Title: r.Title, Message: r.Message, Options: r.Options,
			Placeholder: r.Placeholder, Prefill: r.Prefill}
	case "notify":
		k := KindNotice
		if r.NotifyType == "error" {
			k = KindError
		}
		return []int{t.Add(Entry{Kind: k, Text: r.Message})}
	case "setStatus":
		if r.StatusText == nil {
			delete(t.Status, r.StatusKey)
		} else {
			t.Status[r.StatusKey] = *r.StatusText
		}
	case "setWidget":
		if len(r.WidgetLines) == 0 {
			delete(t.Widgets, r.WidgetKey)
		} else {
			t.Widgets[r.WidgetKey] = r.WidgetLines
		}
	case "setTitle":
		t.Title = r.Title
	case "set_editor_text":
		t.EditorText = r.Text
	}
	return nil
}

// StatusLine joins the extension status entries in key order.
func (t *Transcript) StatusLine() string {
	keys := make([]string, 0, len(t.Status))
	for k := range t.Status {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, t.Status[k])
	}
	return strings.Join(parts, " · ")
}

// contentText is the text of a message's content, which is a string or a list of blocks; other blocks (images, tool calls) are skipped.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return blocksText(raw)
}

func blocksText(raw json.RawMessage) string {
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Summarize collapses whitespace and cuts s to at most n characters, ending with an ellipsis when it cut.
func Summarize(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
