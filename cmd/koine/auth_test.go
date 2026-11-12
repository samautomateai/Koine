package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"koine/core/llm"
)

// authEnv isolates HOME and clears the credential variables of every provider.
func authEnv(t *testing.T) (agentDir string) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("KOINE_AGENT_DIR", "")
	t.Setenv("KOINE_TRUST", "")
	t.Setenv("XDG_CACHE_HOME", h)
	for _, p := range llm.Providers() {
		for _, v := range p.Env {
			t.Setenv(v, "")
		}
	}
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	return filepath.Join(h, ".koine")
}

func authCLI(t *testing.T, stdin string, tty bool, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := run(append([]string{"auth"}, args...), streams{In: strings.NewReader(stdin), Out: &o, Err: &e, InTTY: tty})
	return code, o.String(), e.String()
}

func TestAuthLoginStoresAKeyAndLogoutRemovesIt(t *testing.T) {
	dir := authEnv(t)
	code, out, errs := authCLI(t, "  fake-groq-key \n", true, "login", "groq", "--api-key")
	if code != 0 || !strings.Contains(out, "Saved the API key for groq") || strings.Contains(out+errs, "fake-groq-key") {
		t.Fatalf("login: %d %q %q", code, out, errs)
	}
	file := filepath.Join(dir, "auth.json")
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), `"key": "fake-groq-key"`) || !strings.Contains(string(b), `"type": "api_key"`) {
		t.Errorf("auth.json: %s", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode())
		}
	}
	if code, out, _ := authCLI(t, "", false, "check", "--provider", "groq"); code != 0 || out != "ready\n" {
		t.Errorf("check after login: %d %q", code, out)
	}
	code, out, errs = authCLI(t, "", false, "logout", "groq")
	if code != 0 || !strings.Contains(out, "Removed the stored credential for groq") {
		t.Errorf("logout: %d %q %q", code, out, errs)
	}
	// logging out again is safe to repeat
	if code, out, _ := authCLI(t, "", false, "logout", "groq"); code != 0 || !strings.Contains(out, "No stored credential") {
		t.Errorf("repeat: %d %q", code, out)
	}
	if code, out, _ := authCLI(t, "", false, "check", "--provider", "groq"); code != 1 || out != "not_ready\n" {
		t.Errorf("check after logout: %d %q", code, out)
	}
}

func TestAuthLoginUsageErrors(t *testing.T) {
	authEnv(t)
	for name, c := range map[string]struct {
		stdin string
		tty   bool
		args  []string
		code  int
		want  string
	}{
		"not a terminal":       {"k\n", false, []string{"login", "groq"}, 1, "interactive terminal"},
		"no provider":          {"k\n", true, []string{"login"}, 2, "exactly one provider"},
		"two providers":        {"k\n", true, []string{"login", "groq", "xai"}, 2, "exactly one provider"},
		"unknown provider":     {"k\n", true, []string{"login", "nosuch"}, 2, `unknown provider "nosuch"`},
		"no-browser and key":   {"k\n", true, []string{"login", "groq", "--api-key", "--no-browser"}, 2, "--no-browser cannot be used"},
		"empty key":            {"\n", true, []string{"login", "groq", "--api-key"}, 1, "nothing was entered"},
		"unknown option":       {"", false, []string{"check", "--nope"}, 2, "unknown option"},
		"option without value": {"", false, []string{"check", "--provider"}, 2, "needs a value"},
		"boolean with a value": {"", false, []string{"check", "--json=1"}, 2, "does not take a value"},
		"unknown subcommand":   {"", false, []string{"frobnicate"}, 2, `unknown command "frobnicate"`},
		"nothing to check":     {"", false, []string{"check"}, 2, "give --provider or --model"},
		"bad model":            {"", false, []string{"print-api-key", "--model", "nosuch/x"}, 2, "unknown provider prefix"},
		"bad logout provider":  {"", false, []string{"logout", "nosuch"}, 2, "unknown provider"},
		"bad min-expiry":       {"", false, []string{"print-bearer-token", "--provider", "anthropic", "--min-expiry", "soon"}, 2, "not a duration"},
		"no subcommand":        {"", false, nil, 2, "usage: koine auth"},
		"login of a provider without account sign-in": {"fake-key\n", true, []string{"login", "groq"}, 0, "API key for groq"},
	} {
		code, _, errs := authCLI(t, c.stdin, c.tty, c.args...)
		if code != c.code || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d, stderr %q (want %d and %q)", name, code, errs, c.code, c.want)
		}
	}
	if code, out, _ := authCLI(t, "", false, "--help"); code != 0 || !strings.Contains(out, "print-bearer-token") {
		t.Errorf("--help: %d %q", code, out)
	}
	if code, out, _ := cli("", "--help"); code != 0 || !strings.Contains(out, "koine auth login|logout|check") {
		t.Errorf("main usage should mention auth: %d", code)
	}
}

func TestAuthCheckStatesExitCodesAndJSON(t *testing.T) {
	dir := authEnv(t)
	check := func(args ...string) (int, string, string) {
		return authCLI(t, "", false, append([]string{"check"}, args...)...)
	}

	code, out, errs := check("--provider", "anthropic")
	if code != 1 || out != "not_ready\n" || !strings.Contains(errs, "ANTHROPIC_API_KEY") || !strings.Contains(errs, "koine auth login anthropic") {
		t.Errorf("not ready: %d %q %q", code, out, errs)
	}
	t.Setenv("ANTHROPIC_API_KEY", "fake-env-key")
	code, out, errs = check("--model", "claude-sonnet-5-5")
	if code != 0 || out != "ready\n" || !strings.Contains(errs, "env:ANTHROPIC_API_KEY") {
		t.Errorf("ready: %d %q %q", code, out, errs)
	}
	code, out, _ = check("--provider", "anthropic", "--json", "--credentials", "--no-refresh")
	var r checkResult
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 0 || r.Status != "ready" || r.Provider != "anthropic" ||
		r.Source != "env:ANTHROPIC_API_KEY" || r.Credentials == nil || r.Credentials.Key != "fake-env-key" || r.Credentials.Type != "api_key" {
		t.Errorf("json: %d %s %v", code, out, err)
	}
	_, out, _ = check("--provider", "xai", "--json")
	if strings.Contains(out, "fake-env-key") || strings.Contains(out, "credentials") {
		t.Errorf("credentials must be opt-in: %s", out)
	}
	if code, out, _ := check("--provider", "anthropic", "--credentials"); code != 0 || out != "ready\nfake-env-key\n" {
		t.Errorf("text with credentials: %d %q", code, out)
	}
	// a local server needs no credential
	if code, out, errs := check("--provider", "ollama"); code != 0 || out != "ready\n" || !strings.Contains(errs, "none needed") {
		t.Errorf("local: %d %q %q", code, out, errs)
	}
	if code, out, _ := check("--provider", "ollama", "--credentials"); code != 0 || out != "ready\n" {
		t.Errorf("local credentials: %d %q", code, out)
	}
	// invalid: a credential exists and cannot be used
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	stored := `{"xai":{"type":"oauth","access":"a"},"groq":{"type":"api_key","key":"$NO_SUCH_VARIABLE_X"}}`
	_ = os.WriteFile(filepath.Join(dir, "auth.json"), []byte(stored), 0o600)
	code, out, errs = check("--provider", "xai", "--json")
	if code != 2 || !strings.Contains(out, `"status":"invalid"`) || !strings.Contains(out, "Koine cannot use") {
		t.Errorf("invalid oauth: %d %s %s", code, out, errs)
	}
	if code, out, errs := check("--provider", "groq"); code != 2 || out != "invalid\n" || !strings.Contains(errs, "NO_SUCH_VARIABLE_X is not set") {
		t.Errorf("invalid template: %d %q %q", code, out, errs)
	}
	if code, out, _ := check("--provider", "nosuch"); code != 2 || out != "invalid\n" {
		t.Errorf("unknown provider: %d %q", code, out)
	}
}

func TestAuthPrintCommands(t *testing.T) {
	authEnv(t)
	print := func(cmd string, args ...string) (int, string, string) {
		return authCLI(t, "", false, append([]string{cmd}, args...)...)
	}
	if code, out, errs := print("print-api-key", "--provider", "xai"); code != 1 || out != "" || !strings.Contains(errs, "XAI_API_KEY") {
		t.Errorf("no key: %d %q %q", code, out, errs)
	}
	t.Setenv("XAI_API_KEY", "fake-xai-key")
	if code, out, _ := print("print-api-key", "--model", "xai/grok-4"); code != 0 || out != "fake-xai-key\n" {
		t.Errorf("print-api-key: %d %q", code, out)
	}
	code, out, errs := print("print-bearer-token", "--provider", "xai", "--min-expiry", "30m")
	if code != 1 || out != "" || !strings.Contains(errs, "not a bearer token") {
		t.Errorf("bearer for a key: %d %q %q", code, out, errs)
	}
	if code, _, errs := print("print-api-key", "--provider", "ollama"); code != 1 || !strings.Contains(errs, "needs no credential") {
		t.Errorf("local: %d %q", code, errs)
	}
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "fake-bearer")
	if code, out, _ := print("print-bearer-token", "--provider", "anthropic"); code != 0 || out != "fake-bearer\n" {
		t.Errorf("bearer: %d %q", code, out)
	}
	if code, out, errs := print("print-api-key", "--provider", "anthropic"); code != 1 || out != "" || !strings.Contains(errs, "use print-bearer-token") {
		t.Errorf("api key for a bearer credential: %d %q %q", code, out, errs)
	}
}

func TestAuthSeesModelsJSONProvidersAndTheirWarnings(t *testing.T) {
	dir := authEnv(t)
	t.Setenv("CORP_KEY", "fake-corp-key")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "models.json"),
		[]byte(`{"providers":{"corp":{"baseUrl":"http://corp.test/v1","apiKey":"$CORP_KEY","extra":1,"models":[{"id":"m"}]}}}`), 0o600)
	code, out, errs := authCLI(t, "", false, "print-api-key", "--model", "corp/m")
	if code != 0 || out != "fake-corp-key\n" || !strings.Contains(errs, "providers.corp.extra is not acted on") {
		t.Errorf("%d %q %q", code, out, errs)
	}
	if code, out, _ := authCLI(t, "k\n", true, "login", "corp", "--api-key"); code != 0 || !strings.Contains(out, "Saved the API key for corp") {
		t.Errorf("a provider from models.json can be logged in to: %d %q", code, out)
	}
	if code, out, _ := authCLI(t, "", false, "check", "--model", "corp/m", "--json"); code != 0 || !strings.Contains(out, `"source":"auth.json"`) {
		t.Errorf("stored key beats models.json: %d %q", code, out)
	}
}
