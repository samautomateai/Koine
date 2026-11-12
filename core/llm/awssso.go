package llm

import (
	"context"
	"crypto/sha1" //nolint:gosec // the AWS CLI names its SSO token cache after the SHA-1 of the session name or start URL; a file name, not security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ssoSettings are where an SSO profile's login lives: the cache key the AWS CLI names its token file by (the sso-session name, or the start
// URL for the older profile-only form), the region of the portal, and the account and role to use.
type ssoSettings struct {
	key, region, account, role string
}

func (s *AWSSource) ssoSettingsOf(name string, sec map[string]string) (ssoSettings, error) {
	f := s.files
	st := ssoSettings{key: sec["sso_start_url"], region: sec["sso_region"], account: sec["sso_account_id"], role: sec["sso_role_name"]}
	if sess := sec["sso_session"]; sess != "" {
		ss := f.conf["sso-session "+sess]
		if ss == nil {
			return st, fmt.Errorf("AWS profile %q uses sso_session %q, which is not defined as [sso-session %s] in %s", name, sess, sess, f.confFile)
		}
		st.key = sess
		if ss["sso_region"] != "" {
			st.region = ss["sso_region"]
		}
		if ss["sso_start_url"] == "" {
			return st, fmt.Errorf("[sso-session %s] in %s has no sso_start_url", sess, f.confFile)
		}
	}
	for _, miss := range []struct{ key, v string }{{"sso_region", st.region}, {"sso_account_id", st.account}, {"sso_role_name", st.role}} {
		if miss.v == "" {
			return st, fmt.Errorf("AWS profile %q uses SSO but has no %s", name, miss.key)
		}
	}
	if !validRegion(st.region) {
		return st, fmt.Errorf("AWS profile %q: sso_region %q is not an AWS region name", name, st.region)
	}
	return st, nil
}

// ssoLoginHint is what to tell the user when the cached login is missing, expired or refused.
func ssoLoginHint(profile string) string {
	return `run "aws sso login --profile ` + profile + `" and try again`
}

// ssoToken reads the access token that `aws sso login` cached in ~/.aws/sso/cache and checks that it has not expired.
func (s *AWSSource) ssoToken(name string, st ssoSettings) (string, error) {
	sum := sha1.Sum([]byte(st.key)) //nolint:gosec // see the import
	path := filepath.Join(s.files.home, ".aws", "sso", "cache", hex.EncodeToString(sum[:])+".json")
	b, err := os.ReadFile(path) //nolint:gosec // the AWS CLI's own cache, found by AWS's naming rule
	if err != nil {
		return "", fmt.Errorf("AWS profile %q: there is no SSO login (%s is missing): %s", name, path, ssoLoginHint(name))
	}
	var tok struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   string `json:"expiresAt"`
	}
	if json.Unmarshal(b, &tok) != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("AWS profile %q: %s holds no access token: %s", name, path, ssoLoginHint(name))
	}
	var exp time.Time
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05UTC"} { // older CLI versions wrote "UTC" for the zone
		if t, err := time.Parse(layout, tok.ExpiresAt); err == nil {
			exp = t
			break
		}
	}
	if exp.IsZero() || !s.Now().Before(exp) {
		return "", fmt.Errorf("AWS profile %q: the SSO login has expired: %s", name, ssoLoginHint(name))
	}
	return tok.AccessToken, nil
}

// checkSSO validates an SSO profile and its cached login without calling the portal.
func (s *AWSSource) checkSSO(name string, sec map[string]string) error {
	st, err := s.ssoSettingsOf(name, sec)
	if err != nil {
		return err
	}
	_, err = s.ssoToken(name, st)
	return err
}

// ssoCredentials trades the cached login token for the role's keys at the SSO portal (GetRoleCredentials).
func (s *AWSSource) ssoCredentials(ctx context.Context, name string, sec map[string]string) (AWSCredentials, time.Time, error) {
	st, err := s.ssoSettingsOf(name, sec)
	if err != nil {
		return AWSCredentials{}, time.Time{}, err
	}
	token, err := s.ssoToken(name, st)
	if err != nil {
		return AWSCredentials{}, time.Time{}, err
	}
	base := s.files.get("AWS_ENDPOINT_URL_SSO")
	if base == "" {
		base = "https://portal.sso." + st.region + ".amazonaws.com"
	}
	u := strings.TrimRight(base, "/") + "/federation/credentials?" + url.Values{"account_id": {st.account}, "role_name": {st.role}}.Encode()
	data, status, err := s.send(ctx, http.MethodGet, u, map[string]string{"x-amz-sso_bearer_token": token, "accept": "application/json"}, nil)
	if err != nil {
		return AWSCredentials{}, time.Time{}, fmt.Errorf("AWS SSO portal for profile %q: %w", name, err)
	}
	var out struct {
		Message         string `json:"message"`
		RoleCredentials struct {
			AccessKeyID     string `json:"accessKeyId"`
			SecretAccessKey string `json:"secretAccessKey"`
			SessionToken    string `json:"sessionToken"`
			Expiration      int64  `json:"expiration"` // milliseconds since the epoch
		} `json:"roleCredentials"`
	}
	_ = json.Unmarshal(data, &out)
	rc := out.RoleCredentials
	if status != http.StatusOK || rc.AccessKeyID == "" {
		detail := ""
		if out.Message != "" {
			detail = " (" + out.Message + ")"
		}
		return AWSCredentials{}, time.Time{}, fmt.Errorf("AWS SSO portal for profile %q answered HTTP %d%s: %s", name, status, detail, ssoLoginHint(name))
	}
	return AWSCredentials{AccessKeyID: rc.AccessKeyID, SecretAccessKey: rc.SecretAccessKey, SessionToken: rc.SessionToken}, time.UnixMilli(rc.Expiration), nil
}
