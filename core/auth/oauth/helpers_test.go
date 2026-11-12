package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a fake time source: Sleep moves it forward instead of waiting, so polling loops finish at once.
type clock struct {
	mu    sync.Mutex
	t     time.Time
	slept []time.Duration
}

func newClock() *clock { return &clock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.slept = append(c.slept, d)
	c.mu.Unlock()
	return nil
}

func (c *clock) client() Client { return Client{Now: c.Now, Sleep: c.Sleep} }

// challengeOf is the S256 challenge of a verifier, to check what a flow sent against what it later proves.
func challengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// browser stands in for the user's browser: given the sign-in address it calls the redirect with a code (and the state, if asked).
func browser(t *testing.T, code string, withState bool, seen *url.Values) func(string) error {
	t.Helper()
	return func(target string) error {
		u, err := url.Parse(target)
		if err != nil {
			t.Error(err)
			return err
		}
		q := u.Query()
		if seen != nil {
			*seen = q
		}
		redirect := q.Get("redirect_uri")
		if redirect == "" {
			redirect = q.Get("callback_url")
		}
		args := url.Values{"code": {code}}
		if withState {
			args.Set("state", q.Get("state"))
		}
		go func() {
			resp, err := http.Get(redirect + "?" + args.Encode())
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
}

// tokenServer answers token requests with a fixed JSON body and records the requests.
type tokenServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []map[string]string
	hdrs []http.Header
}

func newTokenServer(t *testing.T, status int, body string) *tokenServer {
	t.Helper()
	ts := &tokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := map[string]string{"_path": r.URL.Path, "_method": r.Method}
		raw := make([]byte, 1<<16)
		n, _ := r.Body.Read(raw)
		if strings.Contains(r.Header.Get("Content-Type"), "json") {
			var j map[string]string
			_ = json.Unmarshal(raw[:n], &j)
			for k, v := range j {
				m[k] = v
			}
		} else if v, err := url.ParseQuery(string(raw[:n])); err == nil {
			for k := range v {
				m[k] = v.Get(k)
			}
		}
		ts.mu.Lock()
		ts.reqs = append(ts.reqs, m)
		ts.hdrs = append(ts.hdrs, r.Header.Clone())
		ts.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *tokenServer) last(t *testing.T) map[string]string {
	t.Helper()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.reqs) == 0 {
		t.Fatal("the server got no request")
	}
	return ts.reqs[len(ts.reqs)-1]
}

func quickUI(t *testing.T, notes *[]string) UI {
	return UI{Notify: func(m string) {
		if notes != nil {
			*notes = append(*notes, m)
		}
	}, Timeout: 5 * time.Second}
}

func getQuiet(target string) error {
	resp, err := http.Get(target)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}
