package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"koine/core/tools"
)

type procTool struct {
	p          *Proc
	name, desc string
	schema     json.RawMessage
	snippet    string   // promptSnippet: the one-line entry in the system prompt's tool list
	guidelines []string // promptGuidelines: rule bullets while the tool is active
	callSeq    atomic.Int64
}

func (t *procTool) PromptSnippet() string      { return t.snippet }
func (t *procTool) PromptGuidelines() []string { return t.guidelines }

func (t *procTool) Name() string            { return t.name }
func (t *procTool) Description() string     { return t.desc }
func (t *procTool) Schema() json.RawMessage { return t.schema }
func (t *procTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	id := fmt.Sprintf("ext-%d", t.callSeq.Add(1))
	if up := tools.Updates(ctx); up != nil { // partial results of this call go to whoever listens for the run
		t.p.host.updates.Store(t.name+"/"+id, up)
		defer t.p.host.updates.Delete(t.name + "/" + id)
	}
	res, err := t.p.call(ctx, "tool.execute", map[string]any{"name": t.name, "args": args, "callId": id})
	if err != nil {
		return "", err
	}
	var r struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return "", err
	}
	if r.IsError {
		return r.Content, errors.New(r.Content)
	}
	return r.Content, nil
}
