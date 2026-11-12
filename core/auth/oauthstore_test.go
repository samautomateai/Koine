package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthLoginsAreStoredPrivatelyInPisShapeAndKeepUnknownFields(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "agent"))
	if err := s.SetAPIKey("groq", "fake-key", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOAuth("openai-codex", OAuthToken{Access: "a1", Refresh: "r1", Expires: 1893456000000,
		Extra: map[string]string{"accountId": "acct"}}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(s.Path()); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode())
		}
	}
	tok, ok, err := s.GetOAuth("openai-codex")
	if err != nil || !ok || tok.Access != "a1" || tok.Refresh != "r1" || tok.Expires != 1893456000000 || tok.Extra["accountId"] != "acct" {
		t.Fatalf("%+v %v %v", tok, ok, err)
	}
	if _, ok, _ := s.GetOAuth("groq"); ok {
		t.Error("an API key is not an OAuth login")
	}
	if _, ok, _ := s.GetOAuth("nothing"); ok {
		t.Error("missing")
	}
	// a field Pi wrote that Koine does not model survives, a stale extra goes
	raw, _ := os.ReadFile(s.Path())
	edited := strings.Replace(string(raw), `"type": "oauth"`, `"type": "oauth", "availableModelIds": ["m1"]`, 1)
	if err := os.WriteFile(s.Path(), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOAuth("openai-codex", OAuthToken{Access: "a2", Refresh: "r2", Expires: 5}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.Path())
	if !strings.Contains(string(after), "availableModelIds") || strings.Contains(string(after), "accountId") || !strings.Contains(string(after), `"a2"`) {
		t.Errorf("rewrite:\n%s", after)
	}
	// replacing an API key entry drops its key
	if err := s.SetOAuth("groq", OAuthToken{Access: "g", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := s.Get("groq"); c.Type != TypeOAuth || c.Key != "" {
		t.Errorf("%+v", c)
	}
	if err := s.SetOAuth("", OAuthToken{Access: "x"}); err == nil {
		t.Error("needs a provider")
	}
	if err := (&Store{}).SetOAuth("p", OAuthToken{Access: "x"}); err == nil {
		t.Error("no directory")
	}
}

func TestMaxSafeIntegerExpiryAndBrokenEntries(t *testing.T) {
	s := Open(t.TempDir())
	if err := os.WriteFile(s.Path(), []byte(`{"or":{"type":"oauth","access":"k","refresh":"","expires":9007199254740991},"bad":"text"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, ok, err := s.GetOAuth("or")
	if err != nil || !ok || tok.Expired(time.Now(), 24*time.Hour) {
		t.Errorf("%+v %v %v", tok, ok, err)
	}
	if _, _, err := s.GetOAuth("bad"); err == nil {
		t.Error("an entry that is not an object")
	}
	_ = os.WriteFile(s.Path(), []byte("not json"), 0o600)
	if _, _, err := s.GetOAuth("or"); err == nil {
		t.Error("invalid file")
	}
	if _, err := s.RefreshOAuth("or", nil, nil); err == nil {
		t.Error("invalid file")
	}
}

func TestExpiredCountsMissingExpiryAndSkew(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	for _, c := range []struct {
		exp  int64
		skew time.Duration
		want bool
	}{{0, 0, true}, {1_000_001, 0, false}, {1_000_000, 0, true}, {1_030_000, time.Minute, true}, {1_100_000, time.Minute, false}} {
		if got := (OAuthToken{Expires: c.exp}).Expired(now, c.skew); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

func TestRefreshOAuthRenewsOnceUnderConcurrencyAndKeepsTheEntryOnFailure(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.SetOAuth("anthropic", OAuthToken{Access: "old", Refresh: "r0", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	stale := func(t OAuthToken) bool { return t.Expires < 100 }
	var calls atomic.Int32
	renew := func(OAuthToken) (OAuthToken, error) {
		calls.Add(1)
		return OAuthToken{Access: "new", Refresh: "r1", Expires: 1000}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := s.RefreshOAuth("anthropic", stale, renew); err != nil || tok.Access != "new" {
				t.Errorf("%+v %v", tok, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("%d refreshes; the refresh token is single-use", calls.Load())
	}
	_ = s.SetOAuth("anthropic", OAuthToken{Access: "old", Refresh: "r1", Expires: 1})
	boom := errors.New("boom")
	if _, err := s.RefreshOAuth("anthropic", stale, func(OAuthToken) (OAuthToken, error) { return OAuthToken{}, boom }); !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
	if tok, _, _ := s.GetOAuth("anthropic"); tok.Access != "old" {
		t.Errorf("a failed refresh changed the entry: %+v", tok)
	}
	if _, err := s.RefreshOAuth("nothing", stale, renew); err == nil {
		t.Error("no login")
	}
	if _, err := (&Store{}).RefreshOAuth("x", stale, renew); err == nil {
		t.Error("no directory")
	}
}
