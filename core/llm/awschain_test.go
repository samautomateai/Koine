package llm

import (
	"context"
	"crypto/sha1" //nolint:gosec // the AWS CLI names its SSO token cache after the SHA-1 of the session name; this is a file name, not security
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var awsNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// fakeSTS answers AssumeRole and AssumeRoleWithWebIdentity. The role's name decides the keys it returns ("AKIDROLE-<name>"), and every
// request is recorded.
type fakeSTS struct {
	mu     sync.Mutex
	forms  []url.Values
	auths  []string
	tokens []string
	expire time.Time
	fail   string // a role name that is refused
}

func (f *fakeSTS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	f.forms, f.auths = append(f.forms, r.PostForm), append(f.auths, r.Header.Get("authorization"))
	f.tokens = append(f.tokens, r.Header.Get("x-amz-security-token"))
	f.mu.Unlock()
	arn := r.PostForm.Get("RoleArn")
	name := arn[strings.LastIndex(arn, "/")+1:]
	if name == f.fail {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `<ErrorResponse><Error><Type>Sender</Type><Code>AccessDenied</Code>`+
			`<Message>not allowed to assume `+name+`</Message></Error></ErrorResponse>`)
		return
	}
	_, _ = fmt.Fprintf(w, `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials>`+
		`<AccessKeyId>AKIDROLE-%s</AccessKeyId><SecretAccessKey>secret-%s</SecretAccessKey><SessionToken>token-%s</SessionToken>`+
		`<Expiration>%s</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>`, name, name, name, f.expire.Format(time.RFC3339))
}

func (f *fakeSTS) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.forms)
}

func newSource(t *testing.T, creds, conf string, env map[string]string) *AWSSource {
	t.Helper()
	get := awsHome(t, creds, conf, env)
	s := NewAWSSource(get, nil)
	s.Now = func() time.Time { return awsNow }
	return s
}

const baseKeys = "[base]\naws_access_key_id = AKIDBASE\naws_secret_access_key = base-secret\n"

func TestAWSAssumeRoleFromSourceProfileIsCachedUntilItExpires(t *testing.T) {
	sts := &fakeSTS{expire: awsNow.Add(time.Hour)}
	srv := httptest.NewServer(sts)
	defer srv.Close()
	conf := "[profile dev]\nrole_arn = arn:aws:iam::123456789012:role/dev\nsource_profile = base\nregion = eu-west-1\n" +
		"role_session_name = koine-test\nexternal_id = ext-1\nduration_seconds = 900\n"
	s := newSource(t, baseKeys, conf, map[string]string{"AWS_PROFILE": "dev", "AWS_ENDPOINT_URL_STS": srv.URL})
	if s.Region() != "eu-west-1" {
		t.Errorf("region %q", s.Region())
	}
	c, err := s.Credentials(context.Background())
	if err != nil || c.AccessKeyID != "AKIDROLE-dev" || c.SecretAccessKey != "secret-dev" || c.SessionToken != "token-dev" {
		t.Fatalf("got %+v %v", c, err)
	}
	f := sts.forms[0]
	if f.Get("Action") != "AssumeRole" || f.Get("Version") != "2011-06-15" || f.Get("RoleSessionName") != "koine-test" || f.Get("ExternalId") != "ext-1" ||
		f.Get("DurationSeconds") != "900" || f.Get("RoleArn") != "arn:aws:iam::123456789012:role/dev" {
		t.Errorf("form %v", f)
	}
	if a := sts.auths[0]; !strings.Contains(a, "Credential=AKIDBASE/20261112/eu-west-1/sts/aws4_request") {
		t.Errorf("the request is signed with the source profile's keys for sts: %s", a)
	}
	if _, err = s.Credentials(context.Background()); err != nil || sts.count() != 1 {
		t.Errorf("cached: %v, %d calls", err, sts.count())
	}
	awsLater := awsNow.Add(56 * time.Minute) // within five minutes of the expiry
	s.Now = func() time.Time { return awsLater }
	sts.expire = awsLater.Add(time.Hour)
	if _, err = s.Credentials(context.Background()); err != nil || sts.count() != 2 {
		t.Errorf("refreshed: %v, %d calls", err, sts.count())
	}
}

func TestAWSRoleChainsSignWithTheEarlierRolesKeysAndLoopsAreRefused(t *testing.T) {
	sts := &fakeSTS{expire: awsNow.Add(time.Hour)}
	srv := httptest.NewServer(sts)
	defer srv.Close()
	conf := "[profile top]\nrole_arn = arn:aws:iam::1:role/top\nsource_profile = mid\n" +
		"[profile mid]\nrole_arn = arn:aws:iam::1:role/mid\nsource_profile = base\n" +
		"[profile a]\nrole_arn = arn:aws:iam::1:role/a\nsource_profile = b\n[profile b]\nrole_arn = arn:aws:iam::1:role/b\nsource_profile = a\n"
	env := map[string]string{"AWS_PROFILE": "top", "AWS_ENDPOINT_URL_STS": srv.URL}
	c, err := newSource(t, baseKeys, conf, env).Credentials(context.Background())
	if err != nil || c.AccessKeyID != "AKIDROLE-top" || sts.count() != 2 {
		t.Fatalf("chain: %+v %v (%d calls)", c, err, sts.count())
	}
	if !strings.Contains(sts.auths[0], "AKIDBASE/") || !strings.Contains(sts.auths[1], "AKIDROLE-mid/") || sts.tokens[1] != "token-mid" {
		t.Errorf("second hop must be signed by the first role: %v %v", sts.auths, sts.tokens)
	}
	env["AWS_PROFILE"] = "a"
	if _, err = newSource(t, baseKeys, conf, env).Credentials(context.Background()); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Errorf("loop: %v", err)
	}
	env["AWS_PROFILE"] = "mid"
	conf = "[profile mid]\nrole_arn = arn:aws:iam::1:role/mid\nsource_profile = ghost\n"
	if _, err = newSource(t, baseKeys, conf, env).Credentials(context.Background()); err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("missing source profile: %v", err)
	}
}

func TestAWSAssumeRoleFromEnvironmentKeysAndErrors(t *testing.T) {
	sts := &fakeSTS{expire: awsNow.Add(time.Hour), fail: "denied"}
	srv := httptest.NewServer(sts)
	defer srv.Close()
	env := map[string]string{"AWS_PROFILE": "p", "AWS_ENDPOINT_URL_STS": srv.URL, "AWS_ACCESS_KEY_ID": "AKIDENV", "AWS_SECRET_ACCESS_KEY": "env-secret"}
	conf := "[profile p]\nrole_arn = arn:aws:iam::1:role/ok\ncredential_source = Environment\n"
	c, err := newSource(t, "", conf, env).Credentials(context.Background())
	if err != nil || c.AccessKeyID != "AKIDROLE-ok" || !strings.Contains(sts.auths[0], "AKIDENV/") {
		t.Fatalf("credential_source=Environment: %+v %v", c, err)
	}
	// an explicit AWS_PROFILE that names a role profile beats environment keys; its source profile must exist
	_, err = newSource(t, "", "[profile p]\nrole_arn = arn:aws:iam::1:role/denied\nsource_profile = base\n", env).Credentials(context.Background())
	if err == nil || !strings.Contains(err.Error(), `"base"`) {
		t.Fatalf("a missing source profile: %v", err)
	}
	delete(env, "AWS_ACCESS_KEY_ID")
	_, err = newSource(t, baseKeys, "[profile p]\nrole_arn = arn:aws:iam::1:role/denied\nsource_profile = base\n", env).Credentials(context.Background())
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") || !strings.Contains(err.Error(), "not allowed to assume denied") ||
		strings.Contains(err.Error(), "base-secret") {
		t.Errorf("STS error: %v", err)
	}
	for _, tc := range []struct{ conf, want string }{
		{"role_arn = arn:aws:iam::1:role/x\nsource_profile = base\nmfa_serial = arn:aws:iam::1:mfa/me\n", "mfa_serial"},
		{"role_arn = arn:aws:iam::1:role/x\ncredential_source = Ec2InstanceMetadata\n", "Ec2InstanceMetadata"},
		{"role_arn = arn:aws:iam::1:role/x\n", "source_profile"},
		{"credential_process = /bin/secret-helper --token abc\n", "credential_process"},
		{"role_arn = arn:aws:iam::1:role/x\nsource_profile = base\nregion = bad/../region\n", "region"},
	} {
		s := newSource(t, baseKeys, "[profile p]\n"+tc.conf, map[string]string{"AWS_PROFILE": "p", "AWS_ENDPOINT_URL_STS": srv.URL})
		_, err = s.Credentials(context.Background())
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "abc") {
			t.Errorf("%q: %v", tc.want, err)
		}
	}
}

func TestAWSWebIdentityFromEnvironmentAndProfile(t *testing.T) {
	sts := &fakeSTS{expire: awsNow.Add(time.Hour)}
	srv := httptest.NewServer(sts)
	defer srv.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("jwt-from-kubernetes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"AWS_WEB_IDENTITY_TOKEN_FILE": tokenFile, "AWS_ROLE_ARN": "arn:aws:iam::1:role/irsa", "AWS_ENDPOINT_URL_STS": srv.URL,
		"AWS_REGION": "us-west-2"}
	c, err := newSource(t, "", "", env).Credentials(context.Background())
	if err != nil || c.AccessKeyID != "AKIDROLE-irsa" {
		t.Fatalf("env web identity: %+v %v", c, err)
	}
	f := sts.forms[0]
	if f.Get("Action") != "AssumeRoleWithWebIdentity" || f.Get("WebIdentityToken") != "jwt-from-kubernetes" || sts.auths[0] != "" {
		t.Errorf("web identity is an unsigned call carrying the token: %v %q", f, sts.auths[0])
	}
	conf := "[profile wi]\nrole_arn = arn:aws:iam::1:role/prof\nweb_identity_token_file = " + tokenFile + "\n"
	c, err = newSource(t, "", conf, map[string]string{"AWS_PROFILE": "wi", "AWS_ENDPOINT_URL_STS": srv.URL}).Credentials(context.Background())
	if err != nil || c.AccessKeyID != "AKIDROLE-prof" {
		t.Fatalf("profile web identity: %+v %v", c, err)
	}
	conf = "[profile wi]\nrole_arn = arn:aws:iam::1:role/prof\nweb_identity_token_file = " + tokenFile + ".missing\n"
	if _, err = newSource(t, "", conf, map[string]string{"AWS_PROFILE": "wi", "AWS_ENDPOINT_URL_STS": srv.URL}).Credentials(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "web identity token") {
		t.Errorf("missing token file: %v", err)
	}
}

// fakePortal answers the SSO GetRoleCredentials call and records what it was asked.
type fakePortal struct {
	mu      sync.Mutex
	queries []url.Values
	bearers []string
	status  int
}

func (f *fakePortal) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.queries, f.bearers = append(f.queries, r.URL.Query()), append(f.bearers, r.Header.Get("x-amz-sso_bearer_token"))
	f.mu.Unlock()
	if r.URL.Path != "/federation/credentials" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = fmt.Fprint(w, `{"message":"The security token included in the request is invalid"}`)
		return
	}
	_, _ = fmt.Fprintf(w, `{"roleCredentials":{"accessKeyId":"AKIDSSO","secretAccessKey":"sso-secret","sessionToken":"sso-token","expiration":%d}}`,
		awsNow.Add(time.Hour).UnixMilli())
}

func writeSSOToken(t *testing.T, get func(string) string, key, token string, expires time.Time) {
	t.Helper()
	sum := sha1.Sum([]byte(key)) //nolint:gosec // see the import
	dir := filepath.Join(get("HOME"), ".aws", "sso", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"startUrl":"https://corp.awsapps.com/start","region":"us-east-1","accessToken":%q,"expiresAt":%q}`,
		token, expires.Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAWSSSOUsesTheCachedLoginTokenForASessionAndALegacyProfile(t *testing.T) {
	portal := &fakePortal{}
	srv := httptest.NewServer(portal)
	defer srv.Close()
	conf := "[sso-session corp]\nsso_start_url = https://corp.awsapps.com/start\nsso_region = us-east-1\n" +
		"[profile new]\nsso_session = corp\nsso_account_id = 111122223333\nsso_role_name = Dev Role\nregion = us-east-2\n" +
		"[profile old]\nsso_start_url = https://corp.awsapps.com/start\nsso_region = us-east-1\nsso_account_id = 444455556666\nsso_role_name = Ops\n"
	get := awsHome(t, "", conf, map[string]string{"AWS_PROFILE": "new", "AWS_ENDPOINT_URL_SSO": srv.URL})
	writeSSOToken(t, get, "corp", "token-for-session", awsNow.Add(time.Hour))
	writeSSOToken(t, get, "https://corp.awsapps.com/start", "token-for-url", awsNow.Add(time.Hour))
	s := NewAWSSource(get, nil)
	s.Now = func() time.Time { return awsNow }
	c, err := s.Credentials(context.Background())
	if err != nil || c.AccessKeyID != "AKIDSSO" || c.SecretAccessKey != "sso-secret" || c.SessionToken != "sso-token" {
		t.Fatalf("sso session: %+v %v", c, err)
	}
	if q := portal.queries[0]; q.Get("account_id") != "111122223333" || q.Get("role_name") != "Dev Role" || portal.bearers[0] != "token-for-session" {
		t.Errorf("portal request %v %v", portal.queries, portal.bearers)
	}
	if _, err = s.Credentials(context.Background()); err != nil || len(portal.queries) != 1 {
		t.Errorf("cached: %v %d", err, len(portal.queries))
	}
	legacy := func(k string) string {
		if k == "AWS_PROFILE" {
			return "old"
		}
		return get(k)
	}
	ls := NewAWSSource(legacy, nil)
	ls.Now = func() time.Time { return awsNow }
	if c, err = ls.Credentials(context.Background()); err != nil || c.AccessKeyID != "AKIDSSO" || portal.bearers[1] != "token-for-url" {
		t.Errorf("legacy sso: %+v %v %v", c, err, portal.bearers)
	}
}

func TestAWSSSOErrorsTellTheUserToLogInAgain(t *testing.T) {
	portal := &fakePortal{}
	srv := httptest.NewServer(portal)
	defer srv.Close()
	conf := "[sso-session corp]\nsso_start_url = https://corp.awsapps.com/start\nsso_region = us-east-1\n" +
		"[profile new]\nsso_session = corp\nsso_account_id = 111122223333\nsso_role_name = Dev\n[profile incomplete]\nsso_session = corp\n" +
		"[profile nosession]\nsso_session = nowhere\nsso_account_id = 1\nsso_role_name = r\n"
	env := map[string]string{"AWS_PROFILE": "new", "AWS_ENDPOINT_URL_SSO": srv.URL}
	get := awsHome(t, "", conf, env)
	src := func(profile string) *AWSSource {
		s := NewAWSSource(func(k string) string {
			if k == "AWS_PROFILE" {
				return profile
			}
			return get(k)
		}, nil)
		s.Now = func() time.Time { return awsNow }
		return s
	}
	if _, err := src("new").Credentials(context.Background()); err == nil || !strings.Contains(err.Error(), "aws sso login --profile new") {
		t.Errorf("no token cached: %v", err)
	}
	writeSSOToken(t, get, "corp", "tok-expired", awsNow.Add(-time.Minute))
	if _, err := src("new").Credentials(context.Background()); err == nil || !strings.Contains(err.Error(), "expired") ||
		!strings.Contains(err.Error(), "aws sso login --profile new") || strings.Contains(err.Error(), "tok-expired") {
		t.Errorf("expired token: %v", err)
	}
	writeSSOToken(t, get, "corp", "tok-live", awsNow.Add(time.Hour))
	portal.status = http.StatusUnauthorized
	if _, err := src("new").Credentials(context.Background()); err == nil || !strings.Contains(err.Error(), "401") ||
		!strings.Contains(err.Error(), "aws sso login") ||
		strings.Contains(err.Error(), "tok-live") {
		t.Errorf("portal refused: %v", err)
	}
	for _, p := range []string{"incomplete", "nosession"} {
		if _, err := src(p).Credentials(context.Background()); err == nil || !strings.Contains(err.Error(), "sso_") {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestAWSRoleOverSSOProfileAndDescribeWithoutNetwork(t *testing.T) {
	portal, sts := &fakePortal{}, &fakeSTS{expire: awsNow.Add(time.Hour)}
	ps, ss := httptest.NewServer(portal), httptest.NewServer(sts)
	defer ps.Close()
	defer ss.Close()
	conf := "[sso-session corp]\nsso_start_url = https://corp.awsapps.com/start\nsso_region = us-east-1\n" +
		"[profile hub]\nsso_session = corp\nsso_account_id = 1\nsso_role_name = Hub\n" +
		"[profile spoke]\nrole_arn = arn:aws:iam::2:role/spoke\nsource_profile = hub\nregion = us-east-1\n"
	get := awsHome(t, "", conf, map[string]string{"AWS_PROFILE": "spoke", "AWS_ENDPOINT_URL_SSO": ps.URL, "AWS_ENDPOINT_URL_STS": ss.URL})
	s := NewAWSSource(get, nil)
	s.Now = func() time.Time { return awsNow }
	if _, err := s.Describe(); err == nil || !strings.Contains(err.Error(), "aws sso login --profile hub") {
		t.Errorf("describe without a login token: %v", err)
	}
	writeSSOToken(t, get, "corp", "tok", awsNow.Add(time.Hour))
	label, err := s.Describe()
	if err != nil || label != "aws:profile spoke (assume role, source hub (sso))" || len(portal.queries) != 0 || sts.count() != 0 {
		t.Fatalf("describe %q %v: no service may be called (%d %d)", label, err, len(portal.queries), sts.count())
	}
	c, err := s.Credentials(context.Background())
	if err != nil || c.AccessKeyID != "AKIDROLE-spoke" || !strings.Contains(sts.auths[0], "AKIDSSO/") || sts.tokens[0] != "sso-token" {
		t.Errorf("sso then role: %+v %v %v", c, err, sts.auths)
	}
}

func TestBedrockRouterUsesTheAWSChain(t *testing.T) {
	sts := &fakeSTS{expire: time.Now().Add(time.Hour)}
	srv := httptest.NewServer(sts)
	defer srv.Close()
	conf := "[profile dev]\nrole_arn = arn:aws:iam::1:role/dev\nsource_profile = base\nregion = eu-west-1\n"
	get := awsHome(t, baseKeys, conf, map[string]string{"AWS_PROFILE": "dev", "AWS_ENDPOINT_URL_STS": srv.URL})
	r := &Router{Getenv: get}
	b, _ := mustResolve(t, r, "amazon-bedrock/m").(*Bedrock)
	if b == nil || b.Region != "eu-west-1" {
		t.Fatalf("provider %#v", b)
	}
	if c, err := b.Creds(); err != nil || c.AccessKeyID != "AKIDROLE-dev" {
		t.Errorf("creds %+v %v", c, err)
	}
	cred, err := r.Credential("amazon-bedrock")
	if err != nil || cred.Source != "aws:profile dev (assume role, source base)" || sts.count() != 1 {
		t.Errorf("auth check names the source and calls no service: %+v %v (%d calls)", cred, err, sts.count())
	}
	r = &Router{Getenv: awsHome(t, "", "[profile s]\nsso_session = c\nsso_account_id = 1\nsso_role_name = r\n"+
		"[sso-session c]\nsso_start_url = https://x\nsso_region = us-east-1\n", map[string]string{"AWS_PROFILE": "s"})}
	var ce *CredentialError
	if _, err = r.Credential("amazon-bedrock"); !errors.As(err, &ce) || ce.Missing || !strings.Contains(err.Error(), "aws sso login") {
		t.Errorf("an SSO profile without a login is invalid, not missing: %v", err)
	}
}
