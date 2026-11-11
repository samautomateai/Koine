package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// imageAPI is a fake Messages API that records every request. With readPath set, its first answer asks for the read tool on that path.
type imageAPI struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []map[string]any
}

func newImageAPI(t *testing.T, readPath string) *imageAPI {
	a := &imageAPI{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		a.mu.Lock()
		a.reqs = append(a.reqs, body)
		first := len(a.reqs) == 1
		a.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		if readPath != "" && first {
			_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "tool_use", "content": []any{
				map[string]any{"type": "tool_use", "id": "call1", "name": "read", "input": map[string]any{"path": readPath}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "seen"}}})
	}))
	t.Cleanup(a.Close)
	return a
}

// firstContent is the content blocks of the first user message of request i.
func (a *imageAPI) firstContent(i int) []any {
	return a.reqs[i]["messages"].([]any)[0].(map[string]any)["content"].([]any)
}

func pngFile(t *testing.T, path string, w, h int) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 7, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	if path != "" {
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func imageBlocks(content []any) []map[string]any {
	var out []map[string]any
	for _, c := range content {
		if m := c.(map[string]any); m["type"] == "image" {
			out = append(out, m)
		}
	}
	return out
}

func sentWidth(t *testing.T, img map[string]any) int {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(img["source"].(map[string]any)["data"].(string))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width
}

func TestImageFileArgumentIsSentAsAnImageAndSaved(t *testing.T) {
	api := newImageAPI(t, "")
	isolate(t, api.URL)
	dir, sessions := t.TempDir(), t.TempDir()
	pngFile(t, filepath.Join(dir, "shot.dat"), 30, 20) // the name says nothing; the bytes do
	code, out, errs := cli("", "-C", dir, "--session-dir", sessions, "-p", "@shot.dat", "what is this?")
	if code != 0 || strings.TrimSpace(out) != "seen" || errs != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	c := api.firstContent(0)
	imgs := imageBlocks(c)
	if len(c) != 2 || len(imgs) != 1 || imgs[0]["source"].(map[string]any)["media_type"] != "image/png" ||
		!strings.Contains(c[0].(map[string]any)["text"].(string), `<file name="shot.dat"></file>`) ||
		!strings.Contains(c[0].(map[string]any)["text"].(string), "what is this?") {
		t.Fatalf("request content: %v", c)
	}
	files, _ := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files: %v", files)
	}
	saved, _ := os.ReadFile(files[0])
	if !strings.Contains(string(saved), `"type":"image"`) || !strings.Contains(string(saved), `"mimeType":"image/png"`) {
		t.Errorf("the image is stored in Pi's shape: %s", saved)
	}
}

func TestImageAloneIsAPromptAndLargeImagesAreResized(t *testing.T) {
	api := newImageAPI(t, "")
	isolate(t, api.URL)
	dir := t.TempDir()
	pngFile(t, filepath.Join(dir, "wide.png"), 2400, 8)
	code, _, errs := cli("", "-C", dir, "--no-session", "@wide.png")
	if code != 0 || !strings.Contains(errs, "image resized from 2400x8 to 2000x6") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	c := api.firstContent(0)
	if w := sentWidth(t, imageBlocks(c)[0]); w != 2000 || !strings.Contains(c[0].(map[string]any)["text"].(string), "resized from 2400x8") {
		t.Errorf("sent width %d, content %v", w, c)
	}
}

func TestAutoResizeAndBlockImagesSettings(t *testing.T) {
	api := newImageAPI(t, "")
	isolate(t, api.URL)
	dir := t.TempDir()
	pngFile(t, filepath.Join(dir, "wide.png"), 2400, 8)
	settings := filepath.Join(os.Getenv("HOME"), ".koine", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"images":{"autoResize":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := cli("", "-C", dir, "--no-session", "@wide.png", "go"); code != 0 || strings.Contains(errs, "resized") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if w := sentWidth(t, imageBlocks(api.firstContent(0))[0]); w != 2400 {
		t.Errorf("images.autoResize=false sends the original, got width %d", w)
	}
	if err := os.WriteFile(settings, []byte(`{"images":{"blockImages":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := cli("", "-C", dir, "--no-session", "@wide.png", "go")
	if code != 0 || !strings.Contains(errs, "1 image(s) not sent: images.blockImages is on") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	c := api.firstContent(1)
	if len(imageBlocks(c)) != 0 || !strings.Contains(c[len(c)-1].(map[string]any)["text"].(string), "[image omitted: images.blockImages is on]") {
		t.Errorf("blocked: %v", c)
	}
}

func TestPipedImageDataIsAnImage(t *testing.T) {
	api := newImageAPI(t, "")
	isolate(t, api.URL)
	code, _, errs := cli(string(pngFile(t, "", 12, 12)), "-C", t.TempDir(), "--no-session", "-p", "describe")
	if code != 0 || errs != "" {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	c := api.firstContent(0)
	if len(imageBlocks(c)) != 1 || c[0].(map[string]any)["text"] != "describe" {
		t.Errorf("content: %v", c)
	}
	if code, _, errs = cli("\x89PNG\r\n\x1a\njunk", "-C", t.TempDir(), "--no-session", "-p", "x"); code != 2 || !strings.Contains(errs, "stdin:") {
		t.Errorf("a broken piped image: code=%d err=%q", code, errs)
	}
}

func TestBadImageArgumentsAreUsageErrors(t *testing.T) {
	api := newImageAPI(t, "")
	isolate(t, api.URL)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.png"), append([]byte("\x89PNG\r\n\x1a\n"), "junk"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := cli("", "-C", dir, "--no-session", "@broken.png", "x"); code != 2 || !strings.Contains(errs, "@broken.png") {
		t.Errorf("code=%d err=%q", code, errs)
	}
	if code, _, errs := cli("", "-C", dir, "--no-session", "@gone.png", "x"); code != 2 || !strings.Contains(errs, "@gone.png") {
		t.Errorf("code=%d err=%q", code, errs)
	}
	if len(api.reqs) != 0 {
		t.Error("nothing is sent when an argument is bad")
	}
}

func TestReadToolSendsImagesBackAndJSONModeShowsThem(t *testing.T) {
	dir := t.TempDir()
	pngFile(t, filepath.Join(dir, "chart.png"), 20, 10)
	api := newImageAPI(t, "chart.png")
	isolate(t, api.URL)
	code, out, errs := cli("", "-C", dir, "--no-session", "--mode", "json", "look at chart.png")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	msgs := api.reqs[1]["messages"].([]any)
	res := msgs[len(msgs)-1].(map[string]any)["content"].([]any)[0].(map[string]any)
	parts, ok := res["content"].([]any)
	if res["type"] != "tool_result" || !ok || len(parts) != 2 || parts[1].(map[string]any)["type"] != "image" ||
		!strings.Contains(parts[0].(map[string]any)["text"].(string), "Read image file [image/png]") {
		t.Fatalf("tool result sent to the model: %v", res)
	}
	var found bool
	for _, r := range jsonLines(t, out) {
		m, _ := r["message"].(map[string]any)
		if r["type"] != "message_end" || m["role"] != "toolResult" {
			continue
		}
		c := m["content"].([]any)
		img := c[1].(map[string]any)
		found = len(c) == 2 && img["type"] == "image" && img["mimeType"] == "image/png" && img["data"] != ""
	}
	if !found {
		t.Errorf("--mode json must carry the image block of the tool result: %s", out)
	}
}

func TestJSONModeShowsUserImages(t *testing.T) {
	api := newImageAPI(t, "")
	isolate(t, api.URL)
	dir := t.TempDir()
	pngFile(t, filepath.Join(dir, "a.png"), 6, 6)
	code, out, _ := cli("", "-C", dir, "--no-session", "--mode", "json", "@a.png", "hi")
	if code != 0 {
		t.Fatal(code)
	}
	for _, r := range jsonLines(t, out) {
		m, _ := r["message"].(map[string]any)
		if r["type"] == "message_end" && m["role"] == "user" {
			c := m["content"].([]any)
			if len(c) != 2 || c[1].(map[string]any)["type"] != "image" || c[1].(map[string]any)["mimeType"] != "image/png" {
				t.Errorf("user content: %v", c)
			}
			return
		}
	}
	t.Error("no user message")
}
