package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreWritesOwnerOnlyAndKeepsUnknownEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	s := Open(dir)
	if _, ok, err := s.Get("anthropic"); ok || err != nil {
		t.Fatalf("empty store: %v %v", ok, err)
	}
	if err := s.SetAPIKey("anthropic", "fake-key-1", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAPIKey("cloudflare-workers-ai", "fake-key-2", map[string]string{"CLOUDFLARE_ACCOUNT_ID": "acct"}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(s.Path()); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("auth.json mode %v, %v", fi.Mode(), err)
		}
		if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
			t.Errorf("directory mode %v", fi.Mode())
		}
	}
	// an OAuth entry written by Pi is not Koine's to understand, but a rewrite must keep it
	raw, _ := os.ReadFile(s.Path())
	withOAuth := strings.Replace(string(raw), "{", `{"openai":{"type":"oauth","refresh":"r","access":"a","expires":1},`, 1)
	if err := os.WriteFile(s.Path(), []byte(withOAuth), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAPIKey("groq", "fake-key-3", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.Path())
	if !strings.Contains(string(after), `"refresh": "r"`) {
		t.Errorf("the oauth entry was lost:\n%s", after)
	}
	c, ok, err := s.Get("cloudflare-workers-ai")
	if err != nil || !ok || c.Type != TypeAPIKey || c.Key != "fake-key-2" || c.Env["CLOUDFLARE_ACCOUNT_ID"] != "acct" {
		t.Errorf("Get: %+v %v %v", c, ok, err)
	}
	if o, _, _ := s.Get("openai"); o.Type != "oauth" {
		t.Errorf("oauth entry: %+v", o)
	}
	names, err := s.Providers()
	if err != nil || strings.Join(names, ",") != "anthropic,cloudflare-workers-ai,groq,openai" {
		t.Errorf("Providers: %v %v", names, err)
	}
}

func TestStoreRemoveIsIdempotentAndRefusesBrokenFiles(t *testing.T) {
	s := Open(t.TempDir())
	if had, err := s.Remove("nothing"); had || err != nil {
		t.Errorf("removing from an empty store: %v %v", had, err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Error("removing nothing must not create the file")
	}
	_ = s.SetAPIKey("a", "fake-key", nil)
	if had, err := s.Remove("a"); !had || err != nil {
		t.Errorf("Remove: %v %v", had, err)
	}
	if _, ok, _ := s.Get("a"); ok {
		t.Error("still there")
	}
	if err := os.WriteFile(s.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get("a"); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("broken file: %v", err)
	}
	if err := s.SetAPIKey("b", "fake-key", nil); err == nil {
		t.Error("a broken file must not be overwritten")
	}
	if b, _ := os.ReadFile(s.Path()); string(b) != "{not json" {
		t.Errorf("file was changed: %q", b)
	}
	if _, err := s.Providers(); err == nil {
		t.Error("Providers on a broken file")
	}
	_ = os.WriteFile(s.Path(), []byte(`{"a":"text"}`), 0o600)
	if _, _, err := s.Get("a"); err == nil {
		t.Error("an entry that is not an object")
	}
	_ = os.WriteFile(s.Path(), nil, 0o600)
	if names, err := s.Providers(); err != nil || len(names) != 0 {
		t.Errorf("empty file: %v %v", names, err)
	}
}

func TestStoreWithoutADirectoryHoldsNothingAndRefusesToSave(t *testing.T) {
	s := Open("")
	if _, ok, err := s.Get("x"); ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
	if err := s.SetAPIKey("x", "fake-key", nil); err == nil {
		t.Error("saving without a directory")
	}
	if err := s.SetAPIKey("", "fake-key", nil); err == nil {
		t.Error("saving without a provider")
	}
	if err := Open(t.TempDir()).SetAPIKey("x", "", nil); err == nil {
		t.Error("saving an empty key")
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolveTemplatesAndEscapes(t *testing.T) {
	r := &Resolver{Getenv: env(map[string]string{"KEY": "k1", "OTHER": "o2"})}
	for in, want := range map[string]string{
		"literal-key":          "literal-key",
		"$KEY":                 "k1",
		"${KEY}":               "k1",
		"pre-${KEY}-$OTHER!":   "pre-k1-o2!",
		"cost$$":               "cost$",
		"$$KEY":                "$KEY",
		"$!not-a-command":      "!not-a-command",
		"a$":                   "a$",
		"a$ b":                 "a$ b",
		"${unterminated":       "${unterminated",
		"price 5$":             "price 5$",
		"$1abc":                "$1abc",
		"mixed_$KEY_suffix":    "", // $KEY_suffix is one name, and it is not set
		"mixed_${KEY}_suffix":  "mixed_k1_suffix",
		"":                     "",
		"no dollar, no change": "no dollar, no change",
	} {
		got, err := r.Resolve(in, false)
		if in == "mixed_$KEY_suffix" {
			if err == nil {
				t.Errorf("%q should name the unset variable KEY_suffix", in)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"$MISSING", "${MISSING}", "x-${}"} {
		if _, err := r.Resolve(bad, false); err == nil {
			t.Errorf("Resolve(%q) should fail", bad)
		}
	}
	if _, err := r.Resolve("$MISSING", false); err == nil || !strings.Contains(err.Error(), "MISSING is not set") {
		t.Errorf("error: %v", err)
	}
	if got, err := (&Resolver{}).Resolve("$PATH", false); err != nil || got != os.Getenv("PATH") {
		t.Errorf("default environment: %q %v", got, err)
	}
}

func TestCommandSecretsAreCachedOnlyWhenAskedAndFailuresAreKept(t *testing.T) {
	var runs atomic.Int32
	out := "  secret-value\n"
	r := &Resolver{Run: func(_ context.Context, cmd string) (string, error) {
		runs.Add(1)
		if cmd == "boom" {
			return "", errors.New("exit status 3")
		}
		return out, nil
	}}
	for i := 0; i < 3; i++ {
		if v, err := r.Resolve("!pass show x", true); err != nil || v != "secret-value" {
			t.Fatalf("cached: %q %v", v, err)
		}
	}
	if runs.Load() != 1 {
		t.Errorf("a cached command ran %d times", runs.Load())
	}
	_, _ = r.Resolve("!pass show x", false)
	_, _ = r.Resolve("!pass show x", false)
	if runs.Load() != 3 {
		t.Errorf("an uncached command ran %d times in total, want 3", runs.Load())
	}
	for i := 0; i < 2; i++ {
		if _, err := r.Resolve("!boom", true); err == nil || !strings.Contains(err.Error(), "exit status 3") {
			t.Errorf("failure: %v", err)
		}
	}
	if runs.Load() != 4 {
		t.Errorf("a failed cached command ran again: %d", runs.Load())
	}
	out = "   \n"
	if _, err := r.Resolve("!empty", false); err == nil || !strings.Contains(err.Error(), "printed nothing") {
		t.Errorf("empty output: %v", err)
	}
	if _, err := r.Resolve("!  ", false); err == nil {
		t.Error("an empty command")
	}
}

func TestUncachedCommandsAreReusedOnlyWithinTheBurstWindow(t *testing.T) {
	var runs atomic.Int32
	r := &Resolver{Burst: 40 * time.Millisecond, Run: func(context.Context, string) (string, error) {
		return fmt.Sprint("token-", runs.Add(1)), nil
	}}
	a, _ := r.Resolve("!gettoken", false)
	b, _ := r.Resolve("!gettoken", false)
	if a != "token-1" || b != "token-1" {
		t.Errorf("within the window: %q %q", a, b)
	}
	if c, _ := r.Resolve("!gettoken", true); c != "token-2" {
		t.Errorf("a cached lookup keeps its own entry: %q", c)
	}
	time.Sleep(60 * time.Millisecond)
	if d, _ := r.Resolve("!gettoken", false); d != "token-3" {
		t.Errorf("after the window the command runs again: %q", d)
	}
}

func TestCommandSecretsTimeOut(t *testing.T) {
	r := &Resolver{Timeout: 20 * time.Millisecond, Run: func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	if _, err := r.Resolve("!sleep", false); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout: %v", err)
	}
}

func TestShellCommandsReallyRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	r := &Resolver{}
	if v, err := r.Resolve("!echo from-the-shell", false); err != nil || v != "from-the-shell" {
		t.Errorf("%q %v", v, err)
	}
	if _, err := r.Resolve("!echo oops >&2; exit 4", false); err == nil || !strings.Contains(err.Error(), "oops") {
		t.Errorf("stderr should explain the failure: %v", err)
	}
	if _, err := r.Resolve("!exit 1", false); err == nil {
		t.Error("a failing command")
	}
	if got := firstLine(strings.Repeat("x", 300) + "\nsecond"); len(got) > 210 || strings.Contains(got, "second") {
		t.Errorf("firstLine: %d", len(got))
	}
}

func TestReadSecretReadsALineAndHidesEchoOnTerminals(t *testing.T) {
	var out strings.Builder
	if v, err := ReadSecret(strings.NewReader("  fake-key \nmore\n"), false, &out, "Key: "); err != nil || v != "fake-key" || out.String() != "Key: " {
		t.Errorf("piped: %q %v %q", v, err, out.String())
	}
	if v, err := ReadSecret(strings.NewReader("no-newline"), false, &out, ""); err != nil || v != "no-newline" {
		t.Errorf("no newline: %q %v", v, err)
	}
	for _, in := range []string{"", "\n", "   \n"} {
		if _, err := ReadSecret(strings.NewReader(in), false, &out, ""); err == nil {
			t.Errorf("%q should be an error", in)
		}
	}

	pr, pw, err := os.Pipe() // an *os.File standing in for the terminal
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Close() }()
	_, _ = pw.WriteString("typed-key\n")
	_ = pw.Close()
	var calls []string
	old := sttyRun
	defer func() { sttyRun = old }()
	sttyRun = func(_ *os.File, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "saved-state", nil
	}
	out.Reset()
	if v, err := ReadSecret(pr, true, &out, "Key: "); err != nil || v != "typed-key" {
		t.Errorf("tty: %q %v", v, err)
	}
	if strings.Join(calls, "|") != "-g|-echo|saved-state" {
		t.Errorf("stty calls: %v", calls)
	}

	pr2, pw2, _ := os.Pipe()
	defer func() { _ = pr2.Close() }()
	_, _ = pw2.WriteString("visible-key\n")
	_ = pw2.Close()
	sttyRun = func(*os.File, ...string) (string, error) { return "", errors.New("no stty") }
	out.Reset()
	if v, err := ReadSecret(pr2, true, &out, "Key: "); err != nil || v != "visible-key" || !strings.Contains(out.String(), "visible") {
		t.Errorf("no stty: %q %v %q", v, err, out.String())
	}
}

func TestDefaultSttyRunnerReportsAFailure(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if out, err := sttyRun(f, "-g"); err == nil {
		t.Logf("stty accepted /dev/null: %q", out) // some systems print nothing; either way ReadSecret treats "" as unavailable
	}
}
