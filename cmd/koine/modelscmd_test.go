package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catalogServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateModelsRefreshesTheUserCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KOINE_AGENT_DIR", "")
	t.Setenv("KOINE_OFFLINE", "")
	t.Setenv("KOINE_MODEL_CATALOG_URL", "")
	srv := catalogServer(t, `{"models":[{"id":"acme-1","contextWindow":1000}]}`)
	file := filepath.Join(home, ".koine", "model-catalog.json")

	if code, _, errs := cli("", "update", "--models"); code != 2 || !strings.Contains(errs, "modelCatalogUrl") {
		t.Errorf("no URL: %d %s", code, errs)
	}
	code, out, errs := cli("", "update", "--models", "--url", srv.URL)
	if code != 0 || !strings.Contains(out, "1 added") || !strings.Contains(out, file) {
		t.Fatalf("flag URL: %d %q %q", code, out, errs)
	}
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), "acme-1") {
		t.Errorf("file: %s", b)
	}
	// the URL can come from the environment or the user's settings; a second run of the same catalog changes nothing
	t.Setenv("KOINE_MODEL_CATALOG_URL", srv.URL)
	if code, out, _ = cli("", "update", "--models"); code != 0 || !strings.Contains(out, "1 unchanged") {
		t.Errorf("env URL: %d %q", code, out)
	}
	t.Setenv("KOINE_MODEL_CATALOG_URL", "")
	if err := os.WriteFile(filepath.Join(home, ".koine", "settings.json"), []byte(`{"modelCatalogUrl":"`+srv.URL+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ = cli("", "update", "--models"); code != 0 || !strings.Contains(out, "1 unchanged") {
		t.Errorf("settings URL: %d %q", code, out)
	}
	// offline mode refuses, and a failing refresh leaves the file as it was and says so
	t.Setenv("KOINE_OFFLINE", "1")
	if code, _, errs = cli("", "update", "--models"); code != 1 || !strings.Contains(errs, "KOINE_OFFLINE") {
		t.Errorf("offline: %d %s", code, errs)
	}
	t.Setenv("KOINE_OFFLINE", "")
	before, _ := os.ReadFile(file)
	bad := catalogServer(t, `{"models":[]}`)
	if code, _, errs = cli("", "update", "--models", "--url", bad.URL); code != 1 || !strings.Contains(errs, "was not changed") {
		t.Errorf("bad catalog: %d %s", code, errs)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Error("the file changed")
	}
	if code, _, errs = cli("", "update", "--models", "npm:x"); code != 2 || !strings.Contains(errs, "--models") {
		t.Errorf("sources: %d %s", code, errs)
	}
	if code, out, _ = cli("", "update", "--models", "--url", srv.URL, "--force"); code != 0 || !strings.Contains(out, "1 unchanged") {
		t.Errorf("force: %d %q", code, out)
	}
	if code, _, errs = cli("", "update", "--url"); code != 2 || !strings.Contains(errs, "needs") {
		t.Errorf("--url without value: %d %s", code, errs)
	}
	if code, _, errs = cli("", "update", "--url", "http://x"); code != 2 || !strings.Contains(errs, "--models") {
		t.Errorf("--url without --models: %d %s", code, errs)
	}
}
