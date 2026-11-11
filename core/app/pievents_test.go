package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koine/core/agent"
	"koine/core/llm"
)

// capture runs one input and returns every event it produced.
func runEvents(t *testing.T, a *App, line string) []agent.Event {
	t.Helper()
	var evs []agent.Event
	if _, err := a.HandleInput(context.Background(), line, func(e agent.Event) { evs = append(evs, e) }); err != nil {
		t.Fatal(err)
	}
	return evs
}

func evTypes(evs []agent.Event) string {
	var t []string
	for _, e := range evs {
		t = append(t, e.Type)
	}
	return strings.Join(t, ",")
}

func only(evs []agent.Event, typ string) []agent.Event {
	var out []agent.Event
	for _, e := range evs {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// entryOf decodes the entry an entry_appended event carries.
func entryOf(t *testing.T, e agent.Event) map[string]any {
	t.Helper()
	raw, ok := e.Data["entry"].(json.RawMessage)
	if !ok {
		t.Fatalf("entry is %T", e.Data["entry"])
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNameAndThinkingChangesAreReportedAndRecorded(t *testing.T) {
	isolate(t)
	t.Setenv("KOINE_MODEL", "")
	a := newApp(t, t.TempDir(), RunOptions{})

	evs := runEvents(t, a, "/name my work")
	ch := only(evs, "session_info_changed")
	app := only(evs, "entry_appended")
	if len(ch) != 1 || ch[0].Data["name"] != "my work" || len(app) != 1 {
		t.Fatalf("/name: %s %+v", evTypes(evs), evs)
	}
	if e := entryOf(t, app[0]); e["type"] != "session_info" || e["name"] != "my work" || e["id"] == "" || e["parentId"] != nil && e["parentId"] == "" {
		t.Errorf("entry: %v", e)
	}
	if evs = runEvents(t, a, "/name --clear"); len(only(evs, "session_info_changed")) != 1 {
		t.Fatalf("clear: %s", evTypes(evs))
	} else if _, has := only(evs, "session_info_changed")[0].Data["name"]; has || a.Session.Name() != "" {
		t.Errorf("a cleared name is absent: %+v %q", evs, a.Session.Name())
	}
	if evs = runEvents(t, a, "/name"); len(only(evs, "session_info_changed")) != 0 {
		t.Errorf("showing the name is not a change: %s", evTypes(evs))
	}

	evs = runEvents(t, a, "/thinking high")
	lv, app := only(evs, "thinking_level_changed"), only(evs, "entry_appended")
	if len(lv) != 1 || lv[0].Data["level"] != "high" || len(app) != 1 || entryOf(t, app[0])["type"] != "thinking_level_change" ||
		entryOf(t, app[0])["thinkingLevel"] != "high" {
		t.Fatalf("/thinking: %s %+v", evTypes(evs), evs)
	}
	if evs = runEvents(t, a, "/thinking"); len(only(evs, "thinking_level_changed")) != 0 {
		t.Errorf("showing the level is not a change")
	}
	if evs = runEvents(t, a, "/thinking bogus"); len(only(evs, "thinking_level_changed")) != 0 {
		t.Errorf("a refused level is not a change")
	}
	evs = runEvents(t, a, "/model claude-sonnet-5-5:low")
	if lv = only(evs, "thinking_level_changed"); len(lv) != 1 || lv[0].Data["level"] != "low" {
		t.Errorf("/model id:level: %s", evTypes(evs))
	}
	if env := strings.Join(a.shellEnv(), "\n"); !strings.Contains(env, "KOINE_SESSION_REASONING_LEVEL=low") {
		t.Errorf("the shell should know the level:\n%s", env)
	}
	// without a session nothing is recorded, but the change is still reported
	b := newApp(t, t.TempDir(), RunOptions{NoSession: true})
	if evs = runEvents(t, b, "/thinking low"); len(only(evs, "thinking_level_changed")) != 1 || len(only(evs, "entry_appended")) != 0 {
		t.Errorf("--no-session: %s", evTypes(evs))
	}
	if evs = runEvents(t, b, "/name x"); len(only(evs, "session_info_changed")) != 0 {
		t.Errorf("no session, no name: %s", evTypes(evs))
	}
}

func TestManualCompactionIsReportedWithTheKeptEntry(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	a := newApp(t, dir, RunOptions{})
	a.Agent.Provider = &summarising{}
	a.Agent.KeepRecent = 300
	for i := 0; i < 8; i++ {
		say(t, a, strings.Repeat("question ", 60))
	}
	evs := runEvents(t, a, "/compact")
	if got := evTypes(evs); !strings.HasPrefix(got, "compaction_start,entry_appended,compaction_end") {
		t.Fatalf("events: %s", got)
	}
	start, end, entry := evs[0].Data, evs[2].Data, entryOf(t, evs[1])
	res := end["result"].(map[string]any)
	if start["reason"] != "manual" || end["reason"] != "manual" || end["aborted"] != false || end["willRetry"] != false ||
		entry["type"] != "compaction" || res["firstKeptEntryId"] != entry["firstKeptEntryId"] || res["firstKeptEntryId"] == "" ||
		res["summary"] != "SUMMARY" || entry["tokensBefore"] != float64(res["tokensBefore"].(int)) {
		t.Errorf("start %v end %v entry %v", start, end, entry)
	}
	// without a session the kept entry is unknown, and that is reported as empty
	b := newApp(t, t.TempDir(), RunOptions{NoSession: true})
	b.Agent.Provider = &summarising{}
	b.Agent.KeepRecent = 300
	for i := 0; i < 8; i++ {
		say(t, b, strings.Repeat("question ", 60))
	}
	evs = runEvents(t, b, "/compact")
	if end := only(evs, "compaction_end"); len(end) != 1 || end[0].Data["result"].(map[string]any)["firstKeptEntryId"] != "" {
		t.Errorf("--no-session: %s", evTypes(evs))
	}
}

const eventsExt = `
export default function (pi) {
  pi.registerTool({
    name: "stream", description: "d", parameters: { type: "object", properties: {} },
    execute: async (id, args, signal, onUpdate) => {
      onUpdate({ content: [{ type: "text", text: "half way" }] });
      return { content: [{ type: "text", text: "finished" }] };
    },
  });
  pi.registerCommand("remember", { description: "r", handler: async () => { pi.appendEntry("my-ext", { seen: 2 }); } });
  pi.registerCommand("later", { description: "l", handler: async () => { pi.sendUserMessage("do this afterwards", { deliverAs: "followUp" }); } });
}`

func TestExtensionEntriesQueuedMessagesAndToolUpdates(t *testing.T) {
	isolate(t)
	t.Setenv("KOINE_MODEL", "")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".koine", "extensions"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".koine", "extensions", "ev.mjs"), eventsExt)
	a := newApp(t, dir, RunOptions{})

	// pi.appendEntry: a custom entry in the session, reported as entry_appended, and never part of the conversation
	evs := runEvents(t, a, "/remember")
	app := only(evs, "entry_appended")
	if len(app) != 1 {
		t.Fatalf("/remember: %s %v", evTypes(evs), a.Warnings)
	}
	if e := entryOf(t, app[0]); e["type"] != "custom" || e["customType"] != "my-ext" || e["data"].(map[string]any)["seen"] != float64(2) {
		t.Errorf("custom entry: %v", e)
	}
	if len(a.Session.Messages()) != 0 {
		t.Errorf("a custom entry must not become a message")
	}

	// pi.sendUserMessage queues; the message is delivered after the run and the queue is reported
	p := &scripted{turns: [][]llm.Block{{use("1", "stream", `{}`)}}}
	a.Agent.Provider = p
	runEvents(t, a, "/later")
	if _, fu := a.Agent.Queued(); len(fu) != 1 {
		t.Fatalf("queued follow-ups: %v", fu)
	}
	evs = runEvents(t, a, "start")
	q := only(evs, "queue_update")
	if len(q) != 2 || len(q[0].Data["followUp"].([]string)) != 1 || len(q[1].Data["followUp"].([]string)) != 0 {
		t.Fatalf("queue_update: %s %+v", evTypes(evs), q)
	}
	var sawLater bool
	for _, m := range a.Agent.History {
		if m.Role == "user" && len(m.Content) > 0 && m.Content[0].Text == "do this afterwards" {
			sawLater = true
		}
	}
	if !sawLater {
		t.Errorf("the queued message never reached the conversation")
	}

	// a running extension tool streams partial output
	ups := only(evs, "tool_execution_update")
	if len(ups) != 1 || ups[0].Text != "half way" || ups[0].Tool != "stream" || ups[0].CallID != "1" {
		t.Errorf("tool_execution_update: %s %+v", evTypes(evs), ups)
	}
	// every message of the run was reported as it was written
	if n := len(only(evs, "entry_appended")); n < 4 {
		t.Errorf("entry_appended events: %d in %s", n, evTypes(evs))
	}
}

func TestBranchSummaryAndSessionSwitchesKeepReportingEntries(t *testing.T) {
	isolate(t)
	a := newApp(t, t.TempDir(), RunOptions{})
	a.Agent.Provider = &summarising{}
	say(t, a, "first")
	say(t, a, "second")
	// a fork replaces the session; entries written to the new one are still reported
	runEvents(t, a, "/fork")
	if evs := runEvents(t, a, "/name forked"); len(only(evs, "entry_appended")) != 1 {
		t.Errorf("after /fork: %s", evTypes(evs))
	}
	runEvents(t, a, "/new")
	if evs := runEvents(t, a, "/name fresh"); len(only(evs, "entry_appended")) != 1 {
		t.Errorf("after /new: %s", evTypes(evs))
	}
	// leaving a branch with a summary reports it as an entry too
	say(t, a, "one")
	say(t, a, "two")
	row := a.Session.Rows("all")
	if len(row) < 2 {
		t.Fatalf("rows: %v", row)
	}
	evs := runEvents(t, a, "/tree "+row[0].Entry.ID+" --summary")
	if len(only(evs, "entry_appended")) == 0 {
		t.Errorf("/tree --summary: %s", evTypes(evs))
	}
}
