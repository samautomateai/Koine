package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"koine/core/llm"
	"koine/core/tools"
)

func ofType(evs []Event, typ string) []Event {
	var out []Event
	for _, e := range evs {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func types(evs []Event) string {
	var t []string
	for _, e := range evs {
		t = append(t, e.Type)
	}
	return strings.Join(t, ",")
}

func TestManualCompactionEventsCarryPisFields(t *testing.T) {
	a := newAgent(&summariser{})
	a.KeepRecent = 250
	filled(a, 10)
	a.OnCompact = func(int, string, int) {}
	a.KeptEntryID = func() string { return "entry-9" }
	var evs []Event
	r, err := a.CompactFor(context.Background(), "", ReasonManual, func(e Event) { evs = append(evs, e) })
	if err != nil {
		t.Fatal(err)
	}
	if types(evs) != "compaction_start,compaction_end" || evs[0].Data["reason"] != "manual" {
		t.Fatalf("events: %+v", evs)
	}
	end := evs[1].Data
	res, _ := end["result"].(map[string]any)
	if end["reason"] != "manual" || end["aborted"] != false || end["willRetry"] != false || res["summary"] != "SUMMARY" ||
		res["firstKeptEntryId"] != "entry-9" || res["tokensBefore"] != r.TokensBefore || res["estimatedTokensAfter"] != r.TokensAfter {
		t.Errorf("compaction_end: %+v", end)
	}
	if _, has := end["errorMessage"]; has {
		t.Errorf("a successful compaction has no errorMessage: %+v", end)
	}
	b, _ := json.Marshal(evs[1])
	if !strings.Contains(string(b), `"firstKeptEntryId":"entry-9"`) {
		t.Errorf("json: %s", b)
	}
}

func TestCompactionEndsWithAnErrorAndNothingToCompactIsSilent(t *testing.T) {
	a := newAgent(&summariser{fail: errors.New("model down")})
	a.KeepRecent = 100
	filled(a, 6)
	var evs []Event
	if _, err := a.CompactFor(context.Background(), "", ReasonThreshold, func(e Event) { evs = append(evs, e) }); err == nil {
		t.Fatal("want an error")
	}
	if types(evs) != "compaction_start,compaction_end" || evs[1].Data["reason"] != "threshold" || evs[1].Data["aborted"] != false ||
		evs[1].Data["willRetry"] != false || !strings.Contains(evs[1].Data["errorMessage"].(string), "model down") || evs[1].Data["result"] != nil {
		t.Errorf("failed compaction: %+v", evs)
	}
	// a cancelled compaction says aborted
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evs = nil
	c := newAgent(&summariser{fail: context.Canceled})
	c.KeepRecent = 100
	filled(c, 6)
	_, _ = c.CompactFor(ctx, "", ReasonManual, func(e Event) { evs = append(evs, e) })
	if len(evs) != 2 || evs[1].Data["aborted"] != true {
		t.Errorf("aborted: %+v", evs)
	}
	// nothing to compact: no events at all
	evs = nil
	collectInto := func(e Event) { evs = append(evs, e) }
	if _, err := newAgent(&summariser{}).CompactFor(context.Background(), "", ReasonManual, collectInto); !errors.Is(err, ErrNothingToCompact) ||
		len(evs) != 0 {
		t.Errorf("empty: %v %+v", err, evs)
	}
}

func TestAutomaticCompactionReasons(t *testing.T) {
	// threshold, before the request
	p := &summariser{}
	a := newAgent(p)
	a.ContextWindow, a.Reserve, a.KeepRecent, a.AutoCompact = 2000, 500, 300, true
	filled(a, 12)
	evs, err := collect(a, "next")
	if err != nil {
		t.Fatal(err)
	}
	starts, ends := ofType(evs, "compaction_start"), ofType(evs, "compaction_end")
	if len(starts) != 1 || starts[0].Data["reason"] != "threshold" || len(ends) != 1 || ends[0].Data["willRetry"] != false {
		t.Errorf("threshold: %s", types(evs))
	}
	// overflow, after a request failed: compaction_end says the model will be asked again
	o := &overflowOnce{}
	b := newAgent(o)
	b.KeepRecent = 300
	filled(b, 12)
	evs, err = collect(b, "go")
	if err != nil {
		t.Fatal(err)
	}
	starts, ends = ofType(evs, "compaction_start"), ofType(evs, "compaction_end")
	if len(starts) != 1 || starts[0].Data["reason"] != "overflow" || len(ends) != 1 || ends[0].Data["willRetry"] != true ||
		ends[0].Data["reason"] != "overflow" {
		t.Errorf("overflow: %s %+v", types(evs), ends)
	}
}

// flakyAPI answers the first fails requests with 503, then a text reply (a summary or a normal answer alike).
func flakyAPI(fails int32) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= fails {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("down"))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	return srv, &n
}

func fastRetry(url string, attempts int) *llm.Anthropic {
	return &llm.Anthropic{APIKey: "k", BaseURL: url, Retry: llm.RetryPolicy{BaseDelay: time.Millisecond, Attempts: attempts}}
}

func TestAutoRetryEvents(t *testing.T) {
	srv, _ := flakyAPI(2)
	defer srv.Close()
	evs, err := collect(newAgent(fastRetry(srv.URL, 4)), "go")
	if err != nil {
		t.Fatal(err)
	}
	starts, ends := ofType(evs, "auto_retry_start"), ofType(evs, "auto_retry_end")
	if len(starts) != 2 || len(ends) != 1 {
		t.Fatalf("events: %s", types(evs))
	}
	d := starts[1].Data
	if starts[0].Data["attempt"] != 1 || d["attempt"] != 2 || d["maxAttempts"] != 3 || d["delayMs"] != int64(2) ||
		!strings.Contains(d["errorMessage"].(string), "HTTP 503") {
		t.Errorf("auto_retry_start: %+v", d)
	}
	if ends[0].Data["success"] != true || ends[0].Data["attempt"] != 2 {
		t.Errorf("auto_retry_end: %+v", ends[0].Data)
	}
	if _, has := ends[0].Data["finalError"]; has {
		t.Errorf("a successful retry has no finalError")
	}
	if types(evs) != "notice,auto_retry_start,notice,auto_retry_start,auto_retry_end,text,done" {
		t.Errorf("order: %s", types(evs))
	}

	// every attempt fails: the final error is reported
	bad, _ := flakyAPI(100)
	defer bad.Close()
	evs, err = collect(newAgent(fastRetry(bad.URL, 3)), "go")
	if err == nil {
		t.Fatal("want an error")
	}
	ends = ofType(evs, "auto_retry_end")
	if len(ends) != 1 || ends[0].Data["success"] != false || ends[0].Data["attempt"] != 2 || !strings.Contains(ends[0].Data["finalError"].(string), "503") {
		t.Errorf("failed retries: %s %+v", types(evs), ends)
	}
	// no retry, no events
	ok, _ := flakyAPI(0)
	defer ok.Close()
	if evs, _ = collect(newAgent(fastRetry(ok.URL, 3)), "go"); len(ofType(evs, "auto_retry_start"))+len(ofType(evs, "auto_retry_end")) != 0 {
		t.Errorf("unexpected retry events: %s", types(evs))
	}
}

func TestSummarizationRetryEvents(t *testing.T) {
	srv, _ := flakyAPI(1)
	defer srv.Close()
	a := newAgent(fastRetry(srv.URL, 3))
	a.KeepRecent = 250
	filled(a, 10)
	var evs []Event
	if _, err := a.CompactFor(context.Background(), "", ReasonThreshold, func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatal(err)
	}
	if types(evs) != "compaction_start,summarization_retry_scheduled,summarization_retry_attempt_start,summarization_retry_finished,compaction_end" {
		t.Fatalf("events: %s", types(evs))
	}
	sch, st := evs[1].Data, evs[2].Data
	if sch["attempt"] != 1 || sch["maxAttempts"] != 2 || sch["delayMs"] != int64(1) || !strings.Contains(sch["errorMessage"].(string), "503") ||
		st["source"] != "compaction" || st["reason"] != "threshold" {
		t.Errorf("scheduled %+v start %+v", sch, st)
	}
	// a branch summary names its source and has no reason
	srv2, _ := flakyAPI(1)
	defer srv2.Close()
	p := fastRetry(srv2.URL, 3)
	evs = nil
	err := Summarizing(context.Background(), func(e Event) { evs = append(evs, e) }, "branchSummary", "", func(ctx context.Context) error {
		_, e := p.Complete(ctx, llm.Request{Model: "m", MaxTokens: 5})
		return e
	})
	if err != nil || types(evs) != "summarization_retry_scheduled,summarization_retry_attempt_start,summarization_retry_finished" {
		t.Fatalf("branch summary: %v %s", err, types(evs))
	}
	if _, has := evs[1].Data["reason"]; has || evs[1].Data["source"] != "branchSummary" {
		t.Errorf("attempt_start: %+v", evs[1].Data)
	}
	if err := Summarizing(context.Background(), nil, "compaction", "manual", func(context.Context) error { return nil }); err != nil {
		t.Errorf("no emit, no retries: %v", err)
	}
}

func TestQueuedMessagesAreDeliveredAndReported(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("1", "echo", `{}`)}, {txt("first answer")}, {txt("second answer")}}}
	a := newAgent(s)
	a.Steer("change course")
	a.FollowUp("and then this")
	if st, fu := a.Queued(); len(st) != 1 || len(fu) != 1 {
		t.Fatalf("queued %v %v", st, fu)
	}
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	// steering typed before the run goes out with the prompt, as its own user message
	first := s.reqs[0].Messages
	if len(first) != 2 || first[1].Content[0].Text != "change course" {
		t.Errorf("steering not delivered with the prompt: %+v", first)
	}
	// the follow-up came after the first answer and produced a third request
	third := s.reqs[2].Messages
	if len(s.reqs) != 3 || third[len(third)-1].Content[0].Text != "and then this" {
		t.Errorf("follow-up: %d requests, last %+v", len(s.reqs), third[len(third)-1])
	}
	var shown []map[string]any
	for _, e := range ofType(evs, "queue_update") {
		shown = append(shown, e.Data)
	}
	if len(shown) != 3 {
		t.Fatalf("queue_update events: %+v from %s", shown, types(evs))
	}
	if len(shown[0]["steering"].([]string)) != 1 || len(shown[0]["followUp"].([]string)) != 1 || len(shown[1]["steering"].([]string)) != 0 ||
		len(shown[1]["followUp"].([]string)) != 1 || len(shown[2]["followUp"].([]string)) != 0 {
		t.Errorf("queue states: %+v", shown)
	}
	b, _ := json.Marshal(ofType(evs, "queue_update")[2])
	if !strings.Contains(string(b), `"steering":[]`) || !strings.Contains(string(b), `"followUp":[]`) {
		t.Errorf("empty queues must be arrays: %s", b)
	}
	// nothing queued, nothing reported
	if evs, _ = collect(newAgent(&script{}), "go"); len(ofType(evs, "queue_update")) != 0 {
		t.Errorf("unexpected queue_update: %s", types(evs))
	}
}

func TestToolsCanStreamPartialOutput(t *testing.T) {
	s := &script{turns: [][]llm.Block{{use("c1", "slow", `{"n":1}`)}}}
	a := newAgent(s)
	a.Tools.Register(&tools.Func{N: "slow", D: "d", S: json.RawMessage(`{"type":"object"}`), F: func(ctx context.Context, _ json.RawMessage) (string, error) {
		tools.Update(ctx, "half")
		tools.Update(ctx, "most")
		return "all", nil
	}})
	evs, err := collect(a, "go")
	if err != nil {
		t.Fatal(err)
	}
	ups := ofType(evs, "tool_execution_update")
	if len(ups) != 2 || ups[0].Text != "half" || ups[1].Text != "most" || ups[0].CallID != "c1" || ups[0].Tool != "slow" ||
		string(ups[0].Args) != `{"n":1}` {
		t.Errorf("updates: %+v", ups)
	}
	if got := types(evs); !strings.HasPrefix(got, "tool_start,tool_execution_update,tool_execution_update,tool_end") {
		t.Errorf("order: %s", got)
	}
}
