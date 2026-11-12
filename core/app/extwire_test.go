package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"koine/core/agent"
	"koine/core/events"
	"koine/core/ext"
	"koine/core/llm"
)

// recExt records every session event it gets in $REC (one JSON object per line), cancels the session_before_* ones while $REC.cancel exists,
// and offers commands and a tool that use the extension API.
const recExt = `import fs from "node:fs";
const rec = process.env.REC;
export default (pi) => {
  const note = (n, e) => fs.appendFileSync(rec, JSON.stringify({ n, e }) + "\n");
  const seen = ["session_start", "session_shutdown", "session_before_switch", "session_before_fork", "session_before_tree", "session_tree",
    "session_before_compact", "session_compact", "tool_execution_start", "tool_execution_end"];
  for (const n of seen) {
    pi.on(n, async (e) => {
      note(n, e);
      if (n.startsWith("session_before") && fs.existsSync(rec + ".cancel")) return { cancel: true };
    });
  }
  pi.registerCommand("say", { description: "says", handler: async (a) => {
    const opts = a === "trigger" ? { triggerTurn: true } : a === "next" ? { deliverAs: "nextTurn" } : undefined;
    pi.sendMessage({ customType: "note", content: "said " + a, display: true, details: { k: 1 } }, opts);
  } });
  pi.registerCommand("ui", { handler: async (a, ctx) => {
    ctx.ui.setStatus("b", "second"); ctx.ui.setStatus("a", "first"); ctx.ui.setStatus("gone", "x"); ctx.ui.setStatus("gone", undefined);
    ctx.ui.setWidget("up", ["above"]); ctx.ui.setWidget("down", ["below"], { placement: "belowEditor" });
    ctx.ui.setWidget("tmp", ["t"]); ctx.ui.setWidget("tmp", undefined);
    ctx.ui.setTitle("my title"); ctx.ui.setWorkingMessage("pondering"); ctx.ui.setEditorText("draft text");
    ctx.ui.notify("plain"); ctx.ui.notify("careful", "warning"); ctx.ui.notify("broken", "error");
  } });
  pi.registerCommand("ask", { handler: async (a, ctx) => {
    note("answers", [await ctx.ui.select("pick", ["x", "y"]), await ctx.ui.confirm("sure", "really?"), await ctx.ui.input("name", "who"),
      await ctx.ui.editor("text", "")]);
  } });
  pi.registerTool({ name: "nest", description: "d", parameters: { type: "object", properties: {} },
    async execute(id, p, signal, onUpdate, ctx) {
      const r = [await ctx.executeTool("read", { path: "hello.txt" }), await ctx.executeTool("nope", {}), await ctx.executeTool("read", { path: 5, extra: 1 })];
      return { content: [{ type: "text", text: JSON.stringify(r.map((x) => [x.isError, x.content[0].text])) }] };
    } });
  pi.registerTool({ name: "fire", description: "d", parameters: { type: "object", properties: {} },
    async execute() {
      pi.sendMessage({ customType: "note", content: "followed", display: true }, { deliverAs: "followUp" });
      return { content: [{ type: "text", text: "fired" }] };
    } });
};
`

type recEntry struct {
	N string
	E map[string]any
}

type recFixture struct {
	t   *testing.T
	a   *App
	rec string
	dir string
	sc  *scripted
}

func newRecApp(t *testing.T, o Options, turns ...[]llm.Block) *recFixture {
	t.Helper()
	isolate(t)
	f := &recFixture{t: t, dir: fixture(t), rec: filepath.Join(t.TempDir(), "rec.jsonl")}
	t.Setenv("REC", f.rec)
	write(t, f.dir, ".pi/extensions/rec.mjs", recExt)
	o.SessionDir = filepath.Join(f.dir, "sessions")
	a, err := NewWithOptions(context.Background(), f.dir, o)
	if err != nil {
		t.Fatal(err)
	}
	f.a = a
	f.sc = &scripted{turns: turns}
	a.Agent.Provider = f.sc
	return f
}

func (f *recFixture) events() []recEntry {
	f.t.Helper()
	b, _ := os.ReadFile(f.rec)
	var out []recEntry
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e recEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.N != "" {
			out = append(out, e)
		}
	}
	return out
}

// all returns the payloads of the events called name.
func (f *recFixture) all(name string) []map[string]any {
	var out []map[string]any
	for _, e := range f.events() {
		if e.N == name {
			out = append(out, e.E)
		}
	}
	return out
}

func (f *recFixture) cancelling(on bool) {
	if on {
		_ = os.WriteFile(f.rec+".cancel", nil, 0o644)
	} else {
		_ = os.Remove(f.rec + ".cancel")
	}
}

func (f *recFixture) firstUserEntry() string {
	f.t.Helper()
	raw, _ := f.a.Session.Raw()
	for _, line := range strings.Split(string(raw), "\n") {
		var e struct{ ID, Type string }
		if json.Unmarshal([]byte(line), &e) == nil && e.Type == "message" {
			return e.ID
		}
	}
	f.t.Fatal("no message entry")
	return ""
}

func TestSessionGatesCancelSwitchForkAndTree(t *testing.T) {
	f := newRecApp(t, Options{})
	a := f.a
	defer a.Close()
	say(t, a, "first prompt")
	say(t, a, "second prompt")
	id, first := a.Session.ID, f.firstUserEntry()
	history := len(a.Agent.History)

	f.cancelling(true)
	for _, line := range []string{"/new", "/resume " + id[:8], "/fork", "/tree " + first + " --no-summary", "/fork " + first} {
		if got := say(t, a, line); !strings.Contains(got, "cancelled by an extension") {
			t.Errorf("%s: %q", line, got)
		}
	}
	if a.Session.ID != id || len(a.Agent.History) != history {
		t.Fatal("a cancelled operation must change nothing")
	}
	if n := len(f.all("session_tree")) + len(f.all("session_shutdown")); n != 0 {
		t.Errorf("no lifecycle events after a cancel: %d", n)
	}

	sw := f.all("session_before_switch")
	if len(sw) != 2 || sw[0]["reason"] != "new" || sw[1]["reason"] != "resume" || sw[1]["targetSessionFile"] != a.Session.Path {
		t.Errorf("switch gates: %v", sw)
	}
	fk := f.all("session_before_fork")
	if len(fk) != 2 || fk[0]["position"] != "at" || fk[0]["entryId"] == "" || fk[1]["entryId"] == nil {
		t.Errorf("fork gates: %v", fk)
	}
	tr := f.all("session_before_tree")
	if len(tr) != 1 || tr[0]["preparation"].(map[string]any)["targetId"] != first {
		t.Errorf("tree gate: %v", tr)
	}

	f.cancelling(false)
	say(t, a, "/tree "+first+" --no-summary")
	after := f.all("session_tree")
	if len(after) != 1 || after[0]["newLeafId"] != a.Session.Leaf() || after[0]["fromExtension"] != false {
		t.Errorf("session_tree: %v", after)
	}
	if got := say(t, a, "/fork"); !strings.Contains(got, "forked into") {
		t.Errorf("an uncancelled fork goes ahead: %q", got)
	}
}

func TestSessionLifecycleEventsReachExtensions(t *testing.T) {
	f := newRecApp(t, Options{})
	a := f.a
	start := f.all("session_start")
	if len(start) != 1 || start[0]["reason"] != "startup" {
		t.Fatalf("startup: %v", start)
	}
	say(t, a, "hello")
	oldID := a.Session.ID

	say(t, a, "/new")
	say(t, a, "/resume "+oldID[:8])
	say(t, a, "/fork")
	a.Reload(context.Background())
	a.Close()

	var seq []string
	for _, e := range f.events() {
		if e.N == "session_start" || e.N == "session_shutdown" {
			seq = append(seq, e.N+":"+e.E["reason"].(string))
		}
	}
	want := "session_start:startup session_shutdown:new session_start:new session_shutdown:resume session_start:resume " +
		"session_shutdown:fork session_start:fork session_shutdown:reload session_start:reload session_shutdown:quit"
	if got := strings.Join(seq, " "); got != want {
		t.Errorf("sequence:\n got %s\nwant %s", got, want)
	}
	for _, e := range f.all("session_start") {
		switch e["reason"] {
		case "new", "resume", "fork":
			if e["previousSessionFile"] == nil {
				t.Errorf("%v has no previousSessionFile", e)
			}
		}
	}
	for _, e := range f.all("session_shutdown") {
		if (e["reason"] == "resume" || e["reason"] == "fork") && e["targetSessionFile"] == nil {
			t.Errorf("%v has no targetSessionFile", e)
		}
	}
}

func TestSendMessageAddsACustomMessage(t *testing.T) {
	f := newRecApp(t, Options{})
	a := f.a
	defer a.Close()
	var shown []agent.Event
	a.OnMessage = nil
	emit := func(e agent.Event) { shown = append(shown, e) }
	last := func() string {
		h := a.Agent.History
		return h[len(h)-1].Content[0].Text
	}

	// between runs the message enters the conversation and the session at once, and does not start a turn
	if _, err := a.HandleInput(context.Background(), "/say plain", emit); err != nil {
		t.Fatal(err)
	}
	if last() != "said plain" || len(f.sc.systems) != 0 {
		t.Errorf("history %d, requests %d", len(a.Agent.History), len(f.sc.systems))
	}
	raw, _ := a.Session.Raw()
	stored := string(raw)
	if !strings.Contains(stored, `"customType":"note"`) || !strings.Contains(stored, "said plain") || !strings.Contains(stored, "custom_message") {
		t.Errorf("session: %s", raw)
	}
	var custom *agent.Event
	for i := range shown {
		if shown[i].Type == "custom_message" {
			custom = &shown[i]
		}
	}
	if custom == nil || custom.Data["customType"] != "note" || custom.Data["display"] != true {
		t.Errorf("events: %v", shown)
	}

	// triggerTurn asks the model for an answer
	if _, err := a.HandleInput(context.Background(), "/say trigger", emit); err != nil {
		t.Fatal(err)
	}
	if len(f.sc.systems) == 0 {
		t.Error("triggerTurn must start a turn")
	}

	// nextTurn holds the message back until the next prompt
	n := len(a.Agent.History)
	if _, err := a.HandleInput(context.Background(), "/say next", emit); err != nil {
		t.Fatal(err)
	}
	if len(a.Agent.History) != n {
		t.Error("a nextTurn message waits")
	}
	if _, err := a.HandleInput(context.Background(), "then this", emit); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range a.Agent.History[n:] {
		texts = append(texts, m.Content[0].Text)
	}
	if len(texts) < 2 || texts[0] != "said next" || texts[1] != "then this" {
		t.Errorf("order after nextTurn: %q", texts)
	}

	// during a run a followUp is delivered after the work in progress
	f.sc.turns, f.sc.i = [][]llm.Block{{use("1", "fire", `{}`)}}, 0
	before := len(f.sc.systems)
	if _, err := a.HandleInput(context.Background(), "use the tool", emit); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range a.Agent.History {
		if len(m.Content) > 0 && m.Content[0].Text == "followed" {
			found = true
		}
	}
	if !found || len(f.sc.systems)-before < 3 {
		t.Errorf("the follow-up message was not delivered (%d requests): %v", len(f.sc.systems)-before, found)
	}
}

func TestGetCommandsListsExtensionsTemplatesAndSkills(t *testing.T) {
	f := newRecApp(t, Options{})
	defer f.a.Close()
	var order []string
	bySource := map[string][]string{}
	for _, c := range f.a.CommandList() {
		order = append(order, c.Source)
		bySource[c.Source] = append(bySource[c.Source], c.Name)
		if c.SourceInfo.IsZero() {
			t.Errorf("%s has no source info", c.Name)
		}
	}
	if !has(bySource["extension"], "say") || !has(bySource["prompt"], "fix") || !has(bySource["skill"], "skill:greeter") {
		t.Fatalf("commands: %v", bySource)
	}
	// extensions come first, then prompt templates, then skills
	for i := 1; i < len(order); i++ {
		if rank(order[i]) < rank(order[i-1]) {
			t.Fatalf("order %v", order)
		}
	}
	if !f.a.IsExtensionCommand("/say now") || f.a.IsExtensionCommand("/fix") || f.a.IsExtensionCommand("plain text") {
		t.Error("IsExtensionCommand")
	}
	if got, ok := f.a.ExpandTemplate("/fix the bug"); !ok || !strings.Contains(got, "the bug") {
		t.Errorf("ExpandTemplate: %q %v", got, ok)
	}
	if _, ok := f.a.ExpandTemplate("/unknown"); ok {
		t.Error("an unknown template does not expand")
	}
}

func rank(source string) int { return map[string]int{"extension": 0, "prompt": 1, "skill": 2}[source] }

func TestExtensionSurfacesReachTheFrontend(t *testing.T) {
	f := newRecApp(t, Options{Mode: "tui"})
	a := f.a
	defer a.Close()
	var changed int
	var prefilled string
	a.UIChanged = func() { changed++ }
	a.PrefillEditor = func(s string) { prefilled = s }
	notices := say(t, a, "/ui")

	s := a.Surfaces()
	if strings.Join(s.Statuses, ",") != "first,second" || strings.Join(s.Above, ",") != "above" || strings.Join(s.Below, ",") != "below" {
		t.Errorf("surfaces %+v", s)
	}
	if s.Title != "my title" || s.Working != "pondering" || prefilled != "draft text" || changed == 0 {
		t.Errorf("title %q working %q prefilled %q changed %d", s.Title, s.Working, prefilled, changed)
	}
	if notices != "plain\ncareful\nbroken" { // inside a command, notify is the command's output
		t.Errorf("notices %q", notices)
	}
	a.surface(ext.UIRequest{Method: "notify", Message: "late", Extension: "x"}) // no run in progress: kept with the startup messages
	var live []string
	a.emit = func(e agent.Event) { live = append(live, e.Text) }
	a.surface(ext.UIRequest{Method: "notify", Message: "careful", NotifyType: "warning"})
	a.surface(ext.UIRequest{Method: "notify", Message: "broken", NotifyType: "error"})
	a.emit = nil
	if strings.Join(live, "|") != "warning: careful|error: broken" || !has(a.Warnings, "x: late") {
		t.Errorf("live %q warnings %q", live, a.Warnings)
	}

	// a frontend that takes requests as they are (RPC) gets every one, and shows no notice of its own
	var got []string
	var mu sync.Mutex
	a.OnSurface = func(r ext.UIRequest) { mu.Lock(); got = append(got, r.Method); mu.Unlock() }
	say(t, a, "/ui")
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 11 || !has(got, "setTitle") || !has(got, "set_editor_text") {
		t.Errorf("requests %v", got)
	}
}

func TestDialogsUseTheFrontendsPrompts(t *testing.T) {
	f := newRecApp(t, Options{Mode: "tui"})
	a := f.a
	defer a.Close()
	var asked []string
	a.Choose = func(title string, options []string) int {
		asked = append(asked, title)
		if len(options) == 2 && options[0] == "Yes" {
			return 0
		}
		return 1
	}
	a.Prompt = func(q string, _ bool) (string, error) { asked = append(asked, q); return "typed", nil }
	say(t, a, "/ask")
	// the note stores the array as the payload, which recEntry does not decode: read the raw line
	b, _ := os.ReadFile(f.rec)
	if !strings.Contains(string(b), `"n":"answers","e":["y",true,"typed","typed"]`) {
		t.Errorf("answers: %s", b)
	}
	if len(asked) != 4 || asked[1] != "sure\nreally?" || asked[2] != "name (who)" {
		t.Errorf("asked %q", asked)
	}

	// without prompts, or when the user dismisses them, a dialog is cancelled
	r := a.dialog(context.Background(), ext.UIRequest{Method: "select"})
	if !r.Cancelled {
		t.Error("select without options")
	}
	a.Choose, a.Prompt = nil, nil
	for _, m := range []string{"select", "confirm", "input", "editor"} {
		if r := a.dialog(context.Background(), ext.UIRequest{Method: m, Options: []string{"o"}}); !r.Cancelled {
			t.Errorf("%s without a frontend must be cancelled", m)
		}
	}
	a.Choose = func(string, []string) int { return -1 }
	if r := a.dialog(context.Background(), ext.UIRequest{Method: "confirm"}); !r.Cancelled {
		t.Error("a dismissed confirm is cancelled")
	}
	a.Prompt = func(string, bool) (string, error) { return "", context.Canceled }
	if r := a.dialog(context.Background(), ext.UIRequest{Method: "input"}); !r.Cancelled {
		t.Error("a failed prompt is cancelled")
	}
	a.OnDialog = func(context.Context, ext.UIRequest) ext.UIResponse { v := "rpc"; return ext.UIResponse{Value: &v} }
	if r := a.dialog(context.Background(), ext.UIRequest{Method: "select"}); r.Value == nil || *r.Value != "rpc" {
		t.Error("OnDialog takes over")
	}
}

func TestExecuteToolRunsToolsForExtensionsAndGuardsThem(t *testing.T) {
	f := newRecApp(t, Options{}, []llm.Block{use("1", "nest", `{}`)})
	a := f.a
	defer a.Close()
	say(t, a, "go")
	if len(f.sc.results) != 1 {
		t.Fatalf("results %+v", f.sc.results)
	}
	got := f.sc.results[0].Content
	for _, want := range []string{`[false,"hi\n"]`, `[true,"unknown tool \"nope\""]`, "Invalid arguments for tool \\\"read\\\"", "path"} {
		if !strings.Contains(got, want) {
			t.Errorf("nested results lack %q:\n%s", want, got)
		}
	}
	// the tool_call hook can refuse a nested call
	a.Agent.Dispatch = blocker{}
	if out, isErr := a.runNested(context.Background(), "read", json.RawMessage(`{"path":"hello.txt"}`)); !isErr || !strings.HasPrefix(out, "Blocked: ") {
		t.Errorf("%q %v", out, isErr)
	}
}

type blocker struct{}

func (blocker) Emit(_ context.Context, n string, _ any) events.Decision {
	return events.Decision{Block: n == events.ToolCall, Reason: "no"}
}

func TestAnExtensionToolReplacesABuiltInOfTheSameName(t *testing.T) {
	isolate(t)
	dir := fixture(t)
	t.Setenv("REC", filepath.Join(t.TempDir(), "unused"))
	write(t, dir, ".pi/extensions/override.mjs", `export default (pi) => {
	  pi.registerTool({ name: "read", description: "mine", parameters: { type: "object", properties: {} },
	    async execute() { return { content: [{ type: "text", text: "overridden read" }] }; } });
	}`)
	a, err := NewWithOptions(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	sc := &scripted{turns: [][]llm.Block{{use("1", "read", `{"path":"hello.txt"}`)}}}
	a.Agent.Provider = sc
	say(t, a, "go")
	if len(sc.results) != 1 || sc.results[0].Content != "overridden read" {
		t.Fatalf("results %+v", sc.results)
	}
	if tl, _ := a.Tools.Get("read"); tl.Description() != "mine" {
		t.Error("the registry holds the extension's tool")
	}
}
