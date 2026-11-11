package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"koine/core/agent"
	"koine/core/app"
	"koine/core/llm"
)

// kv is one field of a record. Records are written with "type" first, as Pi's are, which a map would not keep.
type kv struct {
	K string
	V any
}

type record []kv

func (r record) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range r {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(f.K)
		v, err := json.Marshal(f.V)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func rec(typ string, fields ...any) record {
	r := record{{"type", typ}}
	for i := 0; i+1 < len(fields); i += 2 {
		r = append(r, kv{fields[i].(string), fields[i+1]})
	}
	return r
}

// jsonStream writes `--mode json`: Pi's JSON event stream (https://pi.dev/docs/latest/json). Koine's agent reports whole messages, not
// token deltas, so each content block of an assistant message is one start/delta/end triple. The conversation arrives through Message
// (every message added to the history, in order) and tool progress through Event.
type jsonStream struct {
	enc      *json.Encoder
	app      *app.App
	turnOpen bool
	last     map[string]any // the assistant message of the open turn
	results  []any          // tool-result messages of the open turn
	messages []any          // every message of the run, for agent_end
	started  map[string]time.Time
	names    map[string]string
}

func newJSONStream(w io.Writer, a *app.App) *jsonStream {
	return &jsonStream{enc: json.NewEncoder(w), app: a, started: map[string]time.Time{}, names: map[string]string{}}
}

func (j *jsonStream) put(r record) { _ = j.enc.Encode(r) }

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Start writes the session header and agent_start.
func (j *jsonStream) Start() {
	id := newUUID()
	if j.app.Session != nil {
		id = j.app.Session.ID
	}
	j.put(rec("session", "version", 3, "id", id, "timestamp", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "cwd", j.app.Cwd))
	j.put(rec("agent_start"))
}

func (j *jsonStream) openTurn() {
	if !j.turnOpen {
		j.turnOpen = true
		j.put(rec("turn_start"))
	}
}

func (j *jsonStream) both(msg map[string]any) {
	j.put(rec("message_start", "message", msg))
	j.put(rec("message_end", "message", msg))
	j.messages = append(j.messages, msg)
}

func usageOf(m llm.Message) map[string]any {
	u := map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0}
	if m.Usage != nil {
		u["input"], u["output"] = m.Usage.Input, m.Usage.Output
		u["cacheRead"], u["cacheWrite"], u["totalTokens"] = m.Usage.CacheRead, m.Usage.CacheWrite, m.Usage.Total()
	}
	if c := m.Cost; c != nil {
		u["cost"] = map[string]any{"input": c.Input, "output": c.Output, "cacheRead": c.CacheRead, "cacheWrite": c.CacheWrite, "total": c.Total}
	}
	return u
}

// Message reports one message added to the conversation.
func (j *jsonStream) Message(m llm.Message) {
	now := time.Now().UnixMilli()
	switch {
	case m.Role == "assistant":
		j.openTurn()
		j.assistant(m, now)
	case len(m.Content) > 0 && m.Content[0].Type == "tool_result":
		for _, b := range m.Content {
			msg := map[string]any{"role": "toolResult", "toolCallId": b.ToolUseID, "toolName": j.names[b.ToolUseID], "isError": b.IsError,
				"content": []any{map[string]any{"type": "text", "text": b.Content}}, "timestamp": now}
			j.both(msg)
			j.results = append(j.results, msg)
		}
		j.closeTurn()
	default:
		j.openTurn()
		var blocks []any
		for _, b := range m.Content {
			if b.Type == "text" {
				blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
			}
		}
		j.both(map[string]any{"role": "user", "content": blocks, "timestamp": now})
	}
}

func (j *jsonStream) assistant(m llm.Message, now int64) {
	provider, model := j.app.ProviderAndModel()
	usage := usageOf(m)
	stop := "stop"
	blocks := []any{}
	var updates []record
	upd := func(ev record) {
		updates = append(updates, rec("message_update", "usage", usage, "assistantMessageEvent", ev))
	}
	for _, b := range m.Content {
		i := len(blocks)
		switch b.Type {
		case "text":
			blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
			upd(rec("text_start", "contentIndex", i))
			upd(rec("text_delta", "contentIndex", i, "delta", b.Text))
			upd(rec("text_end", "contentIndex", i, "content", b.Text))
		case "tool_use":
			stop = "toolUse"
			args := b.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			j.names[b.ID] = b.Name
			call := map[string]any{"type": "toolCall", "id": b.ID, "name": b.Name, "arguments": args}
			blocks = append(blocks, call)
			upd(rec("toolcall_start", "contentIndex", i, "id", b.ID, "toolName", b.Name))
			upd(rec("toolcall_delta", "contentIndex", i, "delta", string(args)))
			upd(rec("toolcall_end", "contentIndex", i, "toolCall", call))
		}
	}
	msg := map[string]any{"role": "assistant", "content": blocks, "provider": provider, "model": model, "usage": usage, "stopReason": stop, "timestamp": now}
	start := map[string]any{"role": "assistant", "content": []any{}, "provider": provider, "model": model, "usage": usageOf(llm.Message{}),
		"stopReason": "stop", "timestamp": now}
	j.put(rec("message_start", "message", start))
	for _, u := range updates {
		j.put(u)
	}
	j.put(rec("message_end", "message", msg))
	j.messages = append(j.messages, msg)
	j.last = msg
}

func (j *jsonStream) closeTurn() {
	if !j.turnOpen {
		return
	}
	results := j.results
	if results == nil {
		results = []any{}
	}
	j.put(rec("turn_end", "message", j.last, "toolResults", results))
	j.turnOpen, j.last, j.results = false, nil, nil
}

// Event reports tool progress; text, usage and done are already covered by Message.
func (j *jsonStream) Event(e agent.Event) {
	switch e.Type {
	case "tool_start":
		j.started[e.CallID] = time.Now()
		j.names[e.CallID] = e.Tool
		args := e.Args
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		j.put(rec("tool_execution_start", "toolCallId", e.CallID, "toolName", e.Tool, "args", args))
	case "tool_end":
		fields := []any{"toolCallId", e.CallID, "toolName", e.Tool,
			"result", map[string]any{"content": []any{map[string]any{"type": "text", "text": e.Text}}, "details": map[string]any{}}, "isError", e.IsError}
		if t, ok := j.started[e.CallID]; ok {
			fields = append(fields, "durationMs", time.Since(t).Milliseconds())
		}
		j.put(rec("tool_execution_end", fields...))
	case "error":
		// a failed provider call has no assistant message in the history, so report it as the error message Pi would have recorded
		j.openTurn()
		provider, model := j.app.ProviderAndModel()
		msg := map[string]any{"role": "assistant", "content": []any{}, "provider": provider, "model": model, "usage": usageOf(llm.Message{}),
			"stopReason": "error", "errorMessage": e.Text, "timestamp": time.Now().UnixMilli()}
		j.both(msg)
		j.last = msg
	}
}

// Finish closes the run: the open turn, agent_end and agent_settled.
func (j *jsonStream) Finish(aborted bool) {
	j.closeTurn()
	msgs := j.messages
	if msgs == nil {
		msgs = []any{}
	}
	j.put(rec("agent_end", "messages", msgs, "willRetry", false))
	j.put(rec("agent_settled", "aborted", aborted))
}
