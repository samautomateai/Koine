package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ErrorMessage extracts the human-readable message from the JSON error body of a provider, or "" when the body is not JSON or has none.
// It knows the shapes of OpenAI and compatible servers ({"error":{"message"}}, {"error":"text"}), Anthropic ({"type":"error","error":{…}}),
// Google ({"error":{"code","message","status"}}, also inside an array), OpenRouter and Mistral ({"message"}) and FastAPI servers such as vLLM
// ({"detail"}). Shapes were collected from the vendors' documentation and are tested against fake bodies, not live APIs.
func ErrorMessage(body string) string {
	body = strings.TrimSpace(body)
	if body == "" || (body[0] != '{' && body[0] != '[') {
		return ""
	}
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		return ""
	}
	if arr, ok := v.([]any); ok && len(arr) > 0 {
		v = arr[0]
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if e, has := obj["error"]; has {
		if s := errorText(e); s != "" {
			return s
		}
	}
	for _, k := range []string{"message", "detail", "error_description"} {
		if s, ok := obj[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// errorText reads the value of an "error" key: a string, or an object with a message (and a type, code or status to say what kind).
func errorText(e any) string {
	switch e := e.(type) {
	case string:
		return e
	case map[string]any:
		msg, _ := e["message"].(string)
		kind := ""
		for _, k := range []string{"type", "status", "code"} {
			switch c := e[k].(type) {
			case string:
				kind = c
			case float64:
				kind = fmt.Sprint(int(c))
			}
			if kind != "" {
				break
			}
		}
		switch {
		case msg == "":
			return kind
		case kind != "" && !strings.Contains(strings.ToLower(msg), strings.ToLower(kind)) && !isNumeric(kind):
			return msg + " (" + kind + ")"
		}
		return msg
	}
	return ""
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Hint is advice for the user when the status says what is wrong with the credentials, or "" when there is none to give.
func (e *HTTPError) Hint() string {
	where := "check the API key for " + e.Provider
	if p, ok := BuiltinProvider(e.Provider); ok && len(p.Env) > 0 {
		where = fmt.Sprintf("set %s, or run `koine auth login %s`", strings.Join(p.Env, " or "), p.ID)
	} else if ok && p.Local {
		where += " and the server's address"
	}
	switch e.Status {
	case 401:
		return "authentication failed: " + where
	case 403:
		return "access denied: the key may not be allowed to use this model or region; " + where
	case 402:
		return "payment required: the account has no credit or an unpaid invoice"
	}
	return ""
}
