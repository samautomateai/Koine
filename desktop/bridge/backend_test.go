package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"koine/core/rpcclient"
)

type fakeRPC struct {
	prompts   []string
	behaviors []string
	aborts    int
	replies   []map[string]any
	err       error
}

func (f *fakeRPC) Prompt(_ context.Context, m, behavior string) (string, error) {
	f.prompts, f.behaviors = append(f.prompts, m), append(f.behaviors, behavior)
	return "started", f.err
}
func (f *fakeRPC) Abort(context.Context) error { f.aborts++; return f.err }
func (f *fakeRPC) RespondUI(_ string, fields map[string]any) error {
	f.replies = append(f.replies, fields)
	return nil
}
func (f *fakeRPC) GetState(context.Context) (rpcclient.State, error) {
	return rpcclient.State{Model: &rpcclient.Model{ID: "claude-x"}}, f.err
}
func (f *fakeRPC) GetCommands(context.Context) ([]rpcclient.Command, error) {
	return []rpcclient.Command{{Name: "skill:a"}}, f.err
}

type recorder struct {
	mu  sync.Mutex
	got []Update
}

func (r *recorder) emit(name string, data ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "koine:update" {
		r.got = append(r.got, data[0].(Update))
	}
}

func ev(s string) rpcclient.Event {
	var h struct{ Type string }
	_ = json.Unmarshal([]byte(s), &h)
	return rpcclient.Event{Type: h.Type, Raw: json.RawMessage(s)}
}

func TestEventsBecomeUpdatesForThePage(t *testing.T) {
	r := &recorder{}
	b := NewBackend(r.emit, "/proj")
	b.Handle(ev(`{"type":"agent_start"}`))
	b.Handle(ev(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hi"}}`))
	b.Handle(ev(`{"type":"extension_ui_request","id":"ui-1","method":"set_editor_text","text":"draft"}`))
	last := r.got[len(r.got)-1]
	if !last.Busy || last.Editor != "draft" {
		t.Errorf("update %+v", last)
	}
	if u := r.got[1]; len(u.Changes) != 1 || u.Changes[0].Entry.Text != "Hi" || u.Changes[0].Entry.Kind != rpcclient.KindAssistant {
		t.Errorf("change %+v", u)
	}
}

func TestSendSteersWhileBusyAndInfoReadsTheServer(t *testing.T) {
	r, f := &recorder{}, &fakeRPC{}
	b := NewBackend(r.emit, "/proj")
	b.Send("ignored: no client yet")
	b.Cancel()
	if i := b.Info(); i.Model != "" || i.Cwd != "/proj" {
		t.Errorf("info without a client: %+v", i)
	}
	events := make(chan rpcclient.Event)
	done := make(chan struct{})
	go func() { b.Attach(f, events); close(done) }()
	events <- ev(`{"type":"agent_settled"}`)
	b.Send("one")
	events <- ev(`{"type":"agent_start"}`)
	for i := 0; i < 500; i++ { // Attach handles the event on its own goroutine
		r.mu.Lock()
		busy := len(r.got) > 0 && r.got[len(r.got)-1].Busy
		r.mu.Unlock()
		if busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.Send("two")
	if len(f.prompts) != 2 || f.behaviors[0] != "" || f.behaviors[1] != "steer" {
		t.Errorf("prompts %v %v", f.prompts, f.behaviors)
	}
	if i := b.Info(); i.Model != "claude-x" || len(i.Commands) != 1 {
		t.Errorf("info %+v", i)
	}
	b.Cancel()
	if f.aborts != 1 {
		t.Error("cancel")
	}
	close(events)
	<-done
	if u := r.got[len(r.got)-1]; len(u.Changes) != 1 || u.Changes[0].Entry.Kind != rpcclient.KindError {
		t.Errorf("the end of the stream should be reported: %+v", u)
	}
}

func TestFailuresAreShownAndDialogsAnswered(t *testing.T) {
	r, f := &recorder{}, &fakeRPC{err: errors.New("nope")}
	b := NewBackend(func(string, ...any) {}, "/p")
	b.SetEmit(r.emit)
	b.Attach(f, closed())
	n := len(r.got)
	b.Send("x")
	b.Cancel()
	if len(r.got) != n+2 || r.got[n].Changes[0].Entry.Text != "nope" {
		t.Errorf("updates %+v", r.got[n:])
	}
	if i := b.Info(); i.Model != "" || len(i.Commands) != 0 {
		t.Errorf("info on errors: %+v", i)
	}

	f.err = nil
	b.Handle(ev(`{"type":"extension_ui_request","id":"ui-1","method":"confirm","title":"Sure?"}`))
	if r.got[len(r.got)-1].Dialog == nil {
		t.Fatal("dialog should reach the page")
	}
	b.Answer("", true)
	if f.replies[0]["confirmed"] != true || r.got[len(r.got)-1].Dialog != nil {
		t.Errorf("replies %v", f.replies)
	}
	b.Handle(ev(`{"type":"extension_ui_request","id":"ui-2","method":"input","title":"Name"}`))
	b.Dismiss()
	if f.replies[1]["cancelled"] != true {
		t.Errorf("replies %v", f.replies)
	}
	b.Answer("x", false) // no dialog open: nothing is sent
	b.Dismiss()
	if len(f.replies) != 2 {
		t.Errorf("replies %v", f.replies)
	}
}

func closed() <-chan rpcclient.Event {
	c := make(chan rpcclient.Event)
	close(c)
	return c
}
