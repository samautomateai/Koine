package session

import (
	"encoding/json"
	"errors"
)

// AppendCustom stores extension state that is not part of the conversation: a `custom` entry with the extension's customType and data
// (Pi's pi.appendEntry). It never reaches the model.
func (s *Session) AppendCustom(customType string, data json.RawMessage) error {
	if customType == "" {
		return errors.New("customType is required")
	}
	if len(data) > 0 && !json.Valid(data) {
		return errors.New("data is not valid JSON")
	}
	return s.appendEntry("custom", func(e *Entry) { e.CustomType, e.Data = customType, data })
}

// RecordThinkingLevel notes a change of the thinking level (a thinking_level_change entry).
func (s *Session) RecordThinkingLevel(level string) error {
	return s.appendEntry("thinking_level_change", func(e *Entry) { e.ThinkingLevel = level })
}

// JSON is the entry as the session file holds it, for OnAppend observers.
func (e Entry) JSON() json.RawMessage {
	b, _ := json.Marshal(e)
	return b
}

// AppendCustomMessage stores a message an extension added to the conversation (Pi's custom_message entry). Unlike a custom entry it
// reaches the model, as a user message. display says whether frontends show it.
func (s *Session) AppendCustomMessage(customType, content string, display bool, details json.RawMessage) error {
	if customType == "" {
		return errors.New("customType is required")
	}
	if len(details) > 0 && !json.Valid(details) {
		return errors.New("details is not valid JSON")
	}
	text, _ := json.Marshal(content)
	return s.appendEntry("custom_message", func(e *Entry) {
		e.CustomType, e.Content, e.Display, e.Details = customType, text, &display, details
	})
}
