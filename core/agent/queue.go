package agent

import (
	"slices"
	"sync"
)

// Delivery modes for queued messages (Pi's steeringMode and followUpMode settings).
const (
	ModeOneAtATime = "one-at-a-time"
	ModeAll        = "all"
)

// queue holds the messages typed while the agent runs. Steering messages are delivered once the current assistant turn and its tool calls
// have finished, before the next model request; follow-ups only when the agent has no more tool calls and no steering messages left.
type queue struct {
	mu       sync.Mutex
	steering []string
	followUp []string
	active   func(Event) // the emit of the run in progress, nil when idle
}

// Steer queues text to be delivered after the current turn's tool calls, before the next model request. When the agent is idle it stays
// queued and goes out ahead of the next prompt's first request. It returns the queues as they now stand.
func (a *Agent) Steer(text string) (steering, followUp []string) {
	return a.enqueue(text, true)
}

// FollowUp queues text to be delivered when the agent has finished its pending work, which starts another turn.
func (a *Agent) FollowUp(text string) (steering, followUp []string) {
	return a.enqueue(text, false)
}

func (a *Agent) enqueue(text string, steer bool) ([]string, []string) {
	q := &a.queue
	q.mu.Lock()
	if steer {
		q.steering = append(q.steering, text)
	} else {
		q.followUp = append(q.followUp, text)
	}
	s, f, emit := slices.Clone(q.steering), slices.Clone(q.followUp), q.active
	q.mu.Unlock()
	if emit != nil {
		emit(queueEvent(s, f))
	}
	return s, f
}

// queueEvent is Pi's queue_update: the complete queues, as arrays even when empty.
func queueEvent(steering, followUp []string) Event {
	return Event{Type: EvQueueUpdate, Data: Data("steering", orEmpty(steering), "followUp", orEmpty(followUp))}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Queued returns copies of the two queues.
func (a *Agent) Queued() (steering, followUp []string) {
	q := &a.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.steering), slices.Clone(q.followUp)
}

// ClearQueues empties both queues and returns what they held, so a frontend can put the text back in its editor (Pi does this on abort).
func (a *Agent) ClearQueues() (steering, followUp []string) {
	q := &a.queue
	q.mu.Lock()
	steering, followUp, emit := q.steering, q.followUp, q.active
	q.steering, q.followUp = nil, nil
	q.mu.Unlock()
	if emit != nil && (len(steering) > 0 || len(followUp) > 0) {
		emit(queueEvent(nil, nil))
	}
	return steering, followUp
}

// take removes the next delivery from the steering or follow-up queue: one message, or all of them in "all" mode.
func (a *Agent) take(steer bool, emit func(Event)) []string {
	q := &a.queue
	q.mu.Lock()
	list, mode := &q.followUp, a.FollowUpMode
	if steer {
		list, mode = &q.steering, a.SteeringMode
	}
	if len(*list) == 0 {
		q.mu.Unlock()
		return nil
	}
	n := 1
	if mode == ModeAll {
		n = len(*list)
	}
	out := slices.Clone((*list)[:n])
	*list = slices.Clone((*list)[n:])
	s, f := slices.Clone(q.steering), slices.Clone(q.followUp)
	q.mu.Unlock()
	emit(queueEvent(s, f))
	return out
}

// setActive records the emit of the run in progress, so Steer and FollowUp can report queue changes, and returns a function to undo it.
func (a *Agent) setActive(emit func(Event)) func() {
	q := &a.queue
	q.mu.Lock()
	q.active = emit
	q.mu.Unlock()
	return func() {
		q.mu.Lock()
		q.active = nil
		q.mu.Unlock()
	}
}

// deliver adds queued messages to the conversation as user messages and tells the frontend.
func (a *Agent) deliver(texts []string, emit func(Event)) bool {
	for _, t := range texts {
		t = a.ExpandInput(t)
		a.add(userText(t))
		emit(Event{Type: "user_message", Text: t})
	}
	return len(texts) > 0
}
