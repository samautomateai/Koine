package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const remoteCatalog = `{"schema":1,"models":[
 {"id":"claude-x","provider":"anthropic","contextWindow":200000,"maxOutput":8000,
  "cost":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75},"extra":"kept"},
 {"id":"new-model","provider":"acme","contextWindow":64000,"input":["text","image"]}]}`

func serveCatalog(t *testing.T, status int, body, etag string) (*httptest.Server, *[]http.Header) {
	t.Helper()
	var seen []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Clone())
		if etag != "" {
			w.Header().Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	return m
}

var catNow = func() time.Time { return time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC) }

func TestRefreshCatalogWritesMergesAndKeepsABackup(t *testing.T) {
	srv, _ := serveCatalog(t, 200, remoteCatalog, `"v1"`)
	path := filepath.Join(t.TempDir(), "agent", "model-catalog.json")
	res, err := RefreshCatalog(context.Background(), RefreshOptions{URL: srv.URL, Path: path, Now: catNow})
	if err != nil || res.Added != 2 || res.Updated != 0 || res.Models != 2 {
		t.Fatalf("first refresh: %+v %v", res, err)
	}
	m := readJSONFile(t, path)
	if m["source"] != srv.URL || m["fetched"] != "2026-11-12T10:00:00Z" || m["etag"] != `"v1"` || len(m["models"].([]any)) != 2 {
		t.Errorf("file: %v", m)
	}
	if _, err = os.Stat(path + ".bak"); err == nil {
		t.Error("nothing to back up the first time")
	}
	// the user's own entries stay, an entry the remote also lists is replaced, and the previous file is kept as .bak
	user := `{"note":"mine","models":[{"id":"corp-large","contextWindow":99000},{"id":"claude-x","contextWindow":1,"customField":true}]}`
	if err = os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = RefreshCatalog(context.Background(), RefreshOptions{URL: srv.URL, Path: path, Now: catNow})
	if err != nil || res.Added != 1 || res.Updated != 1 || res.Kept != 1 || res.Models != 3 {
		t.Fatalf("merge: %+v %v", res, err)
	}
	m = readJSONFile(t, path)
	ids := []string{}
	for _, e := range m["models"].([]any) {
		ids = append(ids, e.(map[string]any)["id"].(string))
	}
	if strings.Join(ids, ",") != "corp-large,claude-x,new-model" || m["note"] != "mine" {
		t.Errorf("merged: %v %v", ids, m)
	}
	if b, _ := os.ReadFile(path + ".bak"); string(b) != user {
		t.Errorf("backup: %s", b)
	}
	c := &Catalog{}
	if err = c.Merge(path); err != nil || len(c.Models) != 3 {
		t.Errorf("the written file must load: %v %d", err, len(c.Models))
	}
}

func TestRefreshCatalogIsConditionalAndReportsUnchanged(t *testing.T) {
	srv, seen := serveCatalog(t, 200, remoteCatalog, `"v1"`)
	path := filepath.Join(t.TempDir(), "model-catalog.json")
	opt := RefreshOptions{URL: srv.URL, Path: path, Now: catNow}
	if _, err := RefreshCatalog(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	res, err := RefreshCatalog(context.Background(), opt)
	if err != nil || !res.NotModified || (*seen)[1].Get("If-None-Match") != `"v1"` {
		t.Errorf("second refresh: %+v %v %v", res, err, (*seen)[1])
	}
	opt.Force = true
	res, err = RefreshCatalog(context.Background(), opt)
	if err != nil || res.NotModified || res.Unchanged != 2 || res.Added != 0 || (*seen)[2].Get("If-None-Match") != "" {
		t.Errorf("forced refresh: %+v %v", res, err)
	}
}

func TestRefreshCatalogNeverOverwritesOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-catalog.json")
	const original = `{"models":[{"id":"mine","contextWindow":5}]}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"not json":          `<html>`,
		"no models":         `{"models":[]}`,
		"empty id":          `{"models":[{"id":""}]}`,
		"duplicate id":      `{"models":[{"id":"a"},{"id":"a"}]}`,
		"negative window":   `{"models":[{"id":"a","contextWindow":-1}]}`,
		"absurd window":     `{"models":[{"id":"a","contextWindow":99999999999}]}`,
		"negative price":    `{"models":[{"id":"a","cost":{"input":-1}}]}`,
		"absurd price":      `{"models":[{"id":"a","cost":{"output":1e9}}]}`,
		"bad input kind":    `{"models":[{"id":"a","input":["smell"]}]}`,
		"future schema":     `{"schema":2,"models":[{"id":"a"}]}`,
		"multiplier below1": `{"models":[{"id":"a","longContext":{"threshold":1,"multiplier":0.5}}]}`,
		"control in id":     `{"models":[{"id":"a\u0007b"}]}`,
	}
	for name, body := range bad {
		srv, _ := serveCatalog(t, 200, body, "")
		if _, err := RefreshCatalog(context.Background(), RefreshOptions{URL: srv.URL, Path: path, Now: catNow}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	srv, _ := serveCatalog(t, 500, "boom", "")
	if _, err := RefreshCatalog(context.Background(), RefreshOptions{URL: srv.URL, Path: path}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("http error: %v", err)
	}
	big, _ := serveCatalog(t, 200, `{"models":[{"id":"a","name":"`+strings.Repeat("x", maxCatalogBytes)+`"}]}`, "")
	if _, err := RefreshCatalog(context.Background(), RefreshOptions{URL: big.URL, Path: path}); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("oversize: %v", err)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close() // offline: nothing listens
	if _, err := RefreshCatalog(context.Background(), RefreshOptions{URL: dead.URL, Path: path}); err == nil {
		t.Error("an unreachable server must be an error")
	}
	if b, _ := os.ReadFile(path); string(b) != original {
		t.Errorf("the file changed: %s", b)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("a failed refresh must not leave a backup either")
	}
	// an existing file that is not valid JSON is never replaced
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, _ := serveCatalog(t, 200, remoteCatalog, "")
	if _, err := RefreshCatalog(context.Background(), RefreshOptions{URL: ok.URL, Path: path}); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("broken existing file: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{broken" {
		t.Errorf("broken file overwritten: %s", b)
	}
}

func TestRefreshCatalogURLRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	for _, u := range []string{"", "ftp://x/y", "http://example.com/catalog.json", "https://", "file:///etc/passwd", "http://127.0.0.1.evil.example/c.json"} {
		if _, err := RefreshCatalog(context.Background(), RefreshOptions{URL: u, Path: path}); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
	if _, err := RefreshCatalog(context.Background(), RefreshOptions{URL: "https://example.com/c.json"}); err == nil {
		t.Error("a path is required")
	}
	// a redirect from https-or-loopback to plain http elsewhere is refused
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/catalog.json", http.StatusFound)
	}))
	defer srv.Close()
	if _, err := RefreshCatalog(context.Background(), RefreshOptions{URL: srv.URL, Path: path}); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Errorf("downgrade redirect: %v", err)
	}
}
