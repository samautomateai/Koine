package compact

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"koine/core/llm"
)

func user(t string) llm.Message {
	return llm.Message{Role: "user", Content: []llm.Block{{Type: "text", Text: t}}}
}
func asst(t string) llm.Message {
	return llm.Message{Role: "assistant", Content: []llm.Block{{Type: "text", Text: t}}}
}
func call(id, name, in string) llm.Message {
	return llm.Message{Role: "assistant", Content: []llm.Block{{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(in)}}}
}
func result(id, out string, bad bool) llm.Message {
	return llm.Message{Role: "user", Content: []llm.Block{{Type: "tool_result", ToolUseID: id, Content: out, IsError: bad}}}
}

func TestCutPointNeverSeparatesAResultFromItsCall(t *testing.T) {
	big := strings.Repeat("x", 400) // ~100 tokens
	msgs := []llm.Message{user("start"), asst(big), user(big), call("1", "read", `{"path":"a"}`), result("1", big, false), asst(big)}
	for keep := 1; keep < 600; keep += 7 {
		cut := CutPoint(msgs, keep)
		if cut > 0 && isToolResult(msgs[cut]) {
			t.Fatalf("keep=%d cut at a tool result (index %d)", keep, cut)
		}
		if cut > 0 && EstimateTokens(msgs[cut:]) < keep {
			t.Fatalf("keep=%d but only %d tokens kept", keep, EstimateTokens(msgs[cut:]))
		}
	}
	// with ~100 tokens to keep, the last message alone suffices
	if cut := CutPoint(msgs, 100); cut != 5 {
		t.Errorf("cut %d, want 5", cut)
	}
	// ~200: the result alone is not a valid start, so the call comes with it
	if cut := CutPoint(msgs, 150); cut != 3 {
		t.Errorf("cut %d, want 3 (call + result + answer)", cut)
	}
	if CutPoint(msgs, 100000) != 0 || CutPoint(nil, 10) != 0 || CutPoint([]llm.Message{user("only")}, 1) != 0 {
		t.Error("too little history must give 0")
	}
}

func TestSummaryMessageRoundTrip(t *testing.T) {
	m := SummaryMessage("S1")
	if s, ok := SummaryText(m); !ok || s != "S1" {
		t.Errorf("%q %v", s, ok)
	}
	if _, ok := SummaryText(user("plain")); ok {
		t.Error("a plain message is not a summary")
	}
	if _, ok := SummaryText(asst(SummaryPrefix + "x")); ok {
		t.Error("only user messages can be summaries")
	}
}

func TestTranscriptAndFileOps(t *testing.T) {
	msgs := []llm.Message{user("fix it"), call("1", "read", `{"path":"a.go"}`), result("1", strings.Repeat("y", 3000), false),
		call("2", "edit", `{"file_path":"b.go"}`), result("2", "no match", true), call("3", "write", `{"path":"a.go"}`),
		call("4", "read", `{"path":"c.go"}`), call("5", "read", `{}`), asst("done")}
	tr := Transcript(msgs)
	for _, want := range []string{"[User]: fix it", "[Tool call]: read(", "[Tool error]: no match", "[Assistant]: done", "[truncated]"} {
		if !strings.Contains(tr, want) {
			t.Errorf("transcript missing %q:\n%s", want, tr)
		}
	}
	read, mod := FileOps(msgs)
	if strings.Join(read, ",") != "c.go" || strings.Join(mod, ",") != "a.go,b.go" {
		t.Errorf("read=%v modified=%v (a.go was read then written, so it only counts as modified)", read, mod)
	}
}

type fake struct {
	reply string
	err   error
	req   llm.Request
}

func (f *fake) Complete(_ context.Context, r llm.Request) (*llm.Response, error) {
	f.req = r
	if f.err != nil {
		return nil, f.err
	}
	return &llm.Response{Content: []llm.Block{{Type: "text", Text: f.reply}}}, nil
}

func TestSummarize(t *testing.T) {
	f := &fake{reply: "  ## Goal\nship it  "}
	msgs := []llm.Message{user("do"), call("1", "write", `{"path":"x.go"}`), call("2", "read", `{"path":"y.go"}`)}
	s, err := Summarize(context.Background(), f, "m1", "OLD SUMMARY", msgs, "focus on tests")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "## Goal\nship it") || !strings.Contains(s, "<modified-files>\nx.go") || !strings.Contains(s, "<read-files>\ny.go") {
		t.Errorf("summary: %q", s)
	}
	prompt := f.req.Messages[0].Content[0].Text
	for _, want := range []string{"<previous-summary>\nOLD SUMMARY", "Update the previous summary", "[User]: do", "## Next steps", "focus on tests"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if f.req.Model != "m1" || f.req.System == "" || len(f.req.Tools) != 0 {
		t.Errorf("request: %+v", f.req)
	}
	// without a previous summary or instructions, neither appears
	f2 := &fake{reply: "ok"}
	_, _ = Summarize(context.Background(), f2, "m", "", msgs[:1], "")
	if p := f2.req.Messages[0].Content[0].Text; strings.Contains(p, "previous-summary") || strings.Contains(p, "Extra focus") {
		t.Errorf("prompt: %s", p)
	}
	if _, err := Summarize(context.Background(), &fake{reply: " \n"}, "m", "", msgs, ""); err == nil {
		t.Error("empty summary must be an error")
	}
	if _, err := Summarize(context.Background(), &fake{err: errors.New("down")}, "m", "", msgs, ""); err == nil {
		t.Error("provider error must surface")
	}
}

func TestIsOverflow(t *testing.T) {
	for _, m := range []string{"anthropic: HTTP 400: prompt is too long: 250000 tokens > 200000 maximum", "openai: HTTP 400: context_length_exceeded",
		"HTTP 413: Request Too Large", "This model's maximum context length is 8192 tokens"} {
		if !IsOverflow(errors.New(m)) {
			t.Errorf("%q should be an overflow", m)
		}
	}
	if IsOverflow(nil) || IsOverflow(errors.New("HTTP 401: bad key")) || IsOverflow(errors.New("HTTP 429: slow down")) {
		t.Error("other errors are not overflows")
	}
}
