package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TypeOAuth is the type of a subscription login, in Pi's shape: {"type":"oauth","access","refresh","expires"} plus provider extras.
const TypeOAuth = "oauth"

// OAuthToken is a stored subscription login. Expires is a Unix time in milliseconds, as Pi writes it. Extra holds the other string
// fields of the entry (accountId for ChatGPT, enterpriseUrl for GitHub Copilot).
type OAuthToken struct {
	Access  string
	Refresh string
	Expires int64
	Extra   map[string]string
}

// Expired says whether the access token is, or within skew will be, past its expiry. A token without an expiry counts as expired, so
// that it is refreshed rather than trusted.
func (t OAuthToken) Expired(now time.Time, skew time.Duration) bool {
	return t.Expires == 0 || now.Add(skew).UnixMilli() >= t.Expires
}

var oauthKnown = map[string]bool{"type": true, "access": true, "refresh": true, "expires": true}

// decodeOAuth reads an entry of type oauth; ok is false for any other type.
func decodeOAuth(raw json.RawMessage) (t OAuthToken, ok bool, err error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return t, false, err
	}
	var typ string
	_ = json.Unmarshal(m["type"], &typ)
	if typ != TypeOAuth {
		return t, false, nil
	}
	_ = json.Unmarshal(m["access"], &t.Access)
	_ = json.Unmarshal(m["refresh"], &t.Refresh)
	var f float64 // Pi's own OpenRouter login writes Number.MAX_SAFE_INTEGER, so read a float and convert
	_ = json.Unmarshal(m["expires"], &f)
	t.Expires = int64(f)
	for k, v := range m {
		var s string
		if !oauthKnown[k] && json.Unmarshal(v, &s) == nil {
			if t.Extra == nil {
				t.Extra = map[string]string{}
			}
			t.Extra[k] = s
		}
	}
	return t, true, nil
}

// encodeOAuth writes t over prev (the old raw entry, or nil), so that fields Koine does not know, such as Pi's availableModelIds, survive.
func encodeOAuth(prev json.RawMessage, t OAuthToken) (json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if len(prev) > 0 {
		if old, ok, _ := decodeOAuth(prev); ok {
			_ = json.Unmarshal(prev, &m)
			for k := range old.Extra { // extras the new token no longer has are dropped
				delete(m, k)
			}
		}
	}
	set := func(k string, v any) { b, _ := json.Marshal(v); m[k] = b }
	set("type", TypeOAuth)
	set("access", t.Access)
	set("refresh", t.Refresh)
	set("expires", t.Expires)
	for k, v := range t.Extra {
		if !oauthKnown[k] && v != "" {
			set(k, v)
		}
	}
	return json.Marshal(m)
}

// GetOAuth returns the subscription login stored for provider; ok is false when there is none or the entry is an API key.
func (s *Store) GetOAuth(provider string) (OAuthToken, bool, error) {
	mu.Lock()
	defer mu.Unlock()
	all, err := s.read()
	if err != nil {
		return OAuthToken{}, false, err
	}
	raw, found := all[provider]
	if !found {
		return OAuthToken{}, false, nil
	}
	t, ok, err := decodeOAuth(raw)
	if err != nil {
		return OAuthToken{}, false, fmt.Errorf("%s: the entry for %q is not an object: %w", s.path, provider, err)
	}
	return t, ok, nil
}

// SetOAuth stores a subscription login for provider, replacing its entry.
func (s *Store) SetOAuth(provider string, t OAuthToken) error {
	if provider == "" || t.Access == "" {
		return errors.New("auth: a provider and an access token are needed")
	}
	return s.update(func(all map[string]json.RawMessage) bool {
		raw, err := encodeOAuth(all[provider], t)
		if err != nil {
			return false
		}
		all[provider] = raw
		return true
	})
}

// RefreshOAuth returns the stored login of provider, renewed when stale says it must be. The store stays locked (in this process) while
// refresh runs, so concurrent callers share one refresh instead of each spending a single-use refresh token; a second Koine process can
// still race, and the loser then needs a new login. A failed refresh leaves the entry as it was.
func (s *Store) RefreshOAuth(provider string, stale func(OAuthToken) bool, refresh func(OAuthToken) (OAuthToken, error)) (OAuthToken, error) {
	if s.path == "" {
		return OAuthToken{}, errors.New("auth: the agent directory is unknown")
	}
	mu.Lock()
	defer mu.Unlock()
	all, err := s.read()
	if err != nil {
		return OAuthToken{}, err
	}
	cur, ok, err := decodeOAuth(all[provider])
	if err != nil || !ok {
		return OAuthToken{}, fmt.Errorf("auth: no stored login for %s", provider)
	}
	if !stale(cur) {
		return cur, nil
	}
	next, err := refresh(cur)
	if err != nil {
		return OAuthToken{}, err
	}
	err = s.updateLocked(func(all map[string]json.RawMessage) bool {
		raw, e := encodeOAuth(all[provider], next)
		if e != nil {
			return false
		}
		all[provider] = raw
		return true
	})
	return next, err
}
