package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"koine/core/events"
)

func runner(by map[string][]Entry) *Runner {
	return &Runner{Cwd: ".", SessionID: "s", ByEvent: by, Logf: func(string, ...any) {}}
}

func emit(r *Runner, name string, p any) events.Decision {
	return r.Emit(context.Background(), name, p)
}

var call = map[string]any{"toolName": "bash", "callId": "1", "input": json.RawMessage(`{"command":"ls"}`)}

func TestExit2Blocks(t *testing.T) {
	r := runner(map[string][]Entry{"PreToolUse": {{Matcher: "Bash", Command: "echo nope >&2; exit 2"}}})
	d := emit(r, events.ToolCall, call)
	if !d.Block || d.Reason != "nope" {
		t.Fatalf("%+v", d)
	}
}

func TestExit2DefaultReason(t *testing.T) {
	r := runner(map[string][]Entry{"PreToolUse": {{Command: "exit 2"}}})
	if d := emit(r, events.ToolCall, call); !d.Block || d.Reason != "blocked by hook" {
		t.Fatalf("%+v", d)
	}
}

func TestNestedClaudeFormAndStdin(t *testing.T) {
	// the hook reads stdin JSON and blocks only if tool_name is Bash with the right command
	cmd := `python3 -c "import sys,json;d=json.load(sys.stdin);assert d['hook_event_name']=='PreToolUse';` +
		`sys.exit(2 if d['tool_name']=='Bash' and d['tool_input']['command']=='ls' else 0)"`
	r := runner(map[string][]Entry{"PreToolUse": {{Matcher: "Bash|Write", Hooks: []Entry{{Type: "command", Command: cmd}}}}})
	if d := emit(r, events.ToolCall, call); !d.Block {
		t.Fatalf("expected block: %+v", d)
	}
	other := map[string]any{"toolName": "read", "input": json.RawMessage(`{}`)}
	if d := emit(r, events.ToolCall, other); d.Block {
		t.Fatal("matcher should not match read")
	}
}

func TestJSONDecisions(t *testing.T) {
	r := runner(map[string][]Entry{"PreToolUse": {{Command: `echo '{"decision":"block","reason":"json says no"}'`}}})
	if d := emit(r, events.ToolCall, call); !d.Block || d.Reason != "json says no" {
		t.Fatalf("%+v", d)
	}
	r = runner(map[string][]Entry{"PreToolUse": {{Command: `echo '{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"pd"}}'`}}})
	if d := emit(r, events.ToolCall, call); !d.Block || d.Reason != "pd" {
		t.Fatalf("%+v", d)
	}
	r = runner(map[string][]Entry{"PreToolUse": {{Command: `echo '{"continue":false,"stopReason":"halt"}'`}}})
	if d := emit(r, events.ToolCall, call); !d.Block || d.Reason != "halt" {
		t.Fatalf("%+v", d)
	}
	r = runner(map[string][]Entry{"PreToolUse": {{Command: `echo '{"hookSpecificOutput":{"updatedInput":{"command":"pwd"}}}'`}}})
	if d := emit(r, events.ToolCall, call); d.Block || string(d.Args) != `{"command":"pwd"}` {
		t.Fatalf("%+v", d)
	}
}

func TestNonBlockingFailuresAndGarbage(t *testing.T) {
	var logged []string
	r := &Runner{Cwd: ".", ByEvent: map[string][]Entry{"PreToolUse": {{Command: "exit 1"}, {Command: "echo plain text"}, {Command: "echo '{bad json'"}}},
		Logf: func(f string, a ...any) { logged = append(logged, f) }}
	if d := emit(r, events.ToolCall, call); d.Block {
		t.Fatalf("%+v", d)
	}
	if len(logged) != 1 {
		t.Fatalf("expected exactly the exit-1 failure to be logged: %v", logged)
	}
}

func TestEventsAndMatchers(t *testing.T) {
	dump := `cat > "$OUT"`
	_ = dump
	r := runner(map[string][]Entry{
		"UserPromptSubmit": {{Command: `python3 -c "import sys,json;d=json.load(sys.stdin);sys.exit(2 if 'secret' in d['prompt'] else 0)"`}},
		"PostToolUse":      {{Matcher: "*", Command: `python3 -c "import sys,json;d=json.load(sys.stdin);sys.exit(2 if d['tool_response']=='bad' else 0)"`}},
	})
	if d := emit(r, events.Input, map[string]any{"text": "my secret"}); !d.Block {
		t.Fatal("prompt hook")
	}
	if d := emit(r, events.Input, map[string]any{"text": "fine"}); d.Block {
		t.Fatal("prompt hook false positive")
	}
	if d := emit(r, events.ToolResult, map[string]any{"toolName": "x", "output": "bad"}); !d.Block {
		t.Fatal("post hook")
	}
	// events with no mapping or no entries are ignored cheaply
	if d := emit(r, events.TurnStart, nil); d.Block {
		t.Fatal("unmapped")
	}
	if r.Count() != 2 {
		t.Fatal(r.Count())
	}
}

func TestMatches(t *testing.T) {
	for _, c := range []struct {
		m     string
		names []string
		want  bool
	}{
		{"", []string{"x"}, true}, {"*", []string{"x"}, true},
		{"Bash", []string{"bash", "Bash"}, true}, {"bash", []string{"Bash"}, true},
		{"Edit|Write", []string{"write"}, true}, {"Edit", []string{"editor"}, false},
		{"(", []string{"x"}, false}, {"Bash", []string{""}, false},
	} {
		if got := matches(c.m, c.names...); got != c.want {
			t.Errorf("matches(%q,%v)=%v", c.m, c.names, got)
		}
	}
}

func TestTimeout(t *testing.T) {
	r := runner(map[string][]Entry{"PreToolUse": {{Command: "sleep 5", Timeout: 1}}})
	if d := emit(r, events.ToolCall, call); d.Block || !strings.Contains("", "") {
		t.Fatal("timeouts must fail open")
	}
}
