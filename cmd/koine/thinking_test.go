package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// systemText reads the "system" field of an Anthropic request, which is a string or an array of text blocks.
func systemText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var parts []string
	arr, _ := v.([]any)
	for _, b := range arr {
		parts = append(parts, b.(map[string]any)["text"].(string))
	}
	return strings.Join(parts, "")
}

// thinkingAPI answers every request with a thinking block and an answer.
func thinkingAPI(seen *[]map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		*seen = append(*seen, req)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"thinking","thinking":"weighing options","signature":"sg"},{"type":"text","text":"the answer"}]}`))
	}))
}

func TestThinkingFlagParsing(t *testing.T) {
	c, err := parseArgs([]string{"--thinking", "high", "hi"})
	if err != nil || c.Thinking != "high" || len(c.Message) != 1 {
		t.Errorf("%+v %v", c, err)
	}
	if c, err = parseArgs([]string{"--thinking=xhigh"}); err != nil || c.Thinking != "xhigh" {
		t.Errorf("%+v %v", c, err)
	}
	if _, err = parseArgs([]string{"--thinking", "sideways"}); err == nil || !strings.Contains(err.Error(), "off, minimal, low, medium, high, xhigh, max") {
		t.Errorf("an invalid level must be a usage error: %v", err)
	}
	if _, err = parseArgs([]string{"--thinking"}); err == nil {
		t.Error("the flag needs a value")
	}
	if !strings.Contains(usage, "--thinking LEVEL") || !strings.Contains(usage, "--model ID[:LEVEL]") {
		t.Error("the usage text should describe the flag and the suffix")
	}
}

func TestThinkingFlagAndModelSuffixReachTheRequest(t *testing.T) {
	var seen []map[string]any
	api := thinkingAPI(&seen)
	defer api.Close()
	isolate(t, api.URL)
	dir := t.TempDir()
	code, out, errs := cli("", "-C", dir, "--thinking", "high", "hi")
	if code != 0 || strings.TrimSpace(out) != "the answer" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	req := seen[0]
	if req["thinking"].(map[string]any)["type"] != "adaptive" || req["output_config"].(map[string]any)["effort"] != "high" {
		t.Errorf("--thinking high: %v", req)
	}
	if !strings.Contains(errs, "weighing options") || strings.Contains(out, "weighing") {
		t.Errorf("thinking goes to stderr, never stdout: out=%q err=%q", out, errs)
	}
	// the suffix on --model; the flag beats it
	if code, _, errs = cli("", "-C", dir, "--model", "claude-haiku-5-5:off", "hi"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if seen[1]["model"] != "claude-haiku-5-5" || seen[1]["thinking"].(map[string]any)["type"] != "disabled" || seen[1]["output_config"] != nil {
		t.Errorf("suffix off: %v", seen[1])
	}
	if code, _, errs = cli("", "-C", dir, "--model", "claude-haiku-5-5:off", "--thinking", "low", "hi"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if seen[2]["output_config"].(map[string]any)["effort"] != "low" {
		t.Errorf("the flag beats the suffix: %v", seen[2])
	}
	// an invalid level is a usage error: exit 2, nothing sent
	n := len(seen)
	if code, _, errs = cli("", "-C", dir, "--thinking", "sideways", "hi"); code != 2 || !strings.Contains(errs, "--thinking must be one of") || len(seen) != n {
		t.Errorf("invalid level: code %d %q", code, errs)
	}
	// json mode carries the thinking block in Pi's thinking_* records
	code, out, _ = cli("", "-C", dir, "--mode", "json", "hi")
	if code != 0 || !strings.Contains(out, `"type":"thinking_delta"`) || !strings.Contains(out, `"delta":"weighing options"`) {
		t.Errorf("json mode: %d %q", code, out)
	}
}

func writeSettings(dir, body string) error {
	if err := os.MkdirAll(filepath.Join(dir, ".koine"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".koine", "settings.json"), []byte(body), 0o600)
}

func TestHideThinkingBlockSettingKeepsThinkingOutOfPrintMode(t *testing.T) {
	var seen []map[string]any
	api := thinkingAPI(&seen)
	defer api.Close()
	isolate(t, api.URL)
	dir := t.TempDir()
	if err := writeSettings(dir, `{"hideThinkingBlock":true}`); err != nil {
		t.Fatal(err)
	}
	code, out, errs := cli("", "-C", dir, "hi")
	if code != 0 || strings.TrimSpace(out) != "the answer" || strings.Contains(errs, "weighing") {
		t.Errorf("%d %q %q", code, out, errs)
	}
}
