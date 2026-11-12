package llm

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// awsRefreshEarly is how long before its expiry temporary AWS credentials are replaced, so that a request never starts with keys that
// lapse before it is signed.
const awsRefreshEarly = 5 * time.Minute

// AWSSource finds the AWS credentials for Bedrock the way the SDKs do for the cases Koine supports, and keeps temporary ones until shortly
// before they expire. In order: AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY; the web identity variables (AWS_WEB_IDENTITY_TOKEN_FILE and
// AWS_ROLE_ARN, as set by EKS service accounts); then the profile named by AWS_PROFILE (default "default") in ~/.aws/config and
// ~/.aws/credentials, which can hold static keys, an IAM Identity Center (SSO) login, a role to assume from another profile or from the
// environment's keys (chains of any depth), or a web identity token file. A profile you name with AWS_PROFILE that is a role, SSO or
// web identity profile wins over keys in the environment, which then serve only as the source of `credential_source = Environment`.
// ECS and EC2 instance credentials, `credential_process` and MFA prompts are not supported. Errors name files, profiles and variables and
// never print keys or tokens. The services are called with the standard library only: STS with SigV4 (this package's), the SSO portal with
// the access token that `aws sso login` cached. Endpoints can be moved with AWS_ENDPOINT_URL_STS and AWS_ENDPOINT_URL_SSO (or tested
// against).
type AWSSource struct {
	// Now is the clock, replaced in tests.
	Now func() time.Time

	files awsFiles
	hc    *http.Client
	mu    sync.Mutex
	cache map[string]awsCached
}

type awsCached struct {
	creds   AWSCredentials
	expires time.Time
}

// NewAWSSource reads the environment (through get) and the shared files. hc is used for STS and the SSO portal; nil means a client with a
// 30-second timeout.
func NewAWSSource(get func(string) string, hc *http.Client) *AWSSource {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &AWSSource{Now: time.Now, files: readAWSFiles(get), hc: hc, cache: map[string]awsCached{}}
}

// Region is AWS_REGION, AWS_DEFAULT_REGION, the profile's region, else DefaultAWSRegion.
func (s *AWSSource) Region() string { return s.files.region }

// Credentials returns usable keys, calling STS or the SSO portal when the profile needs it and the cached keys have (almost) expired.
func (s *AWSSource) Credentials(ctx context.Context) (AWSCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, _, err := s.top(ctx, false)
	return c, err
}

// Describe checks that the configuration can work and names where the keys come from ("aws:profile dev (assume role, source base)"),
// without calling any AWS service. For SSO it reads the cached login token and fails when there is none or it has expired.
func (s *AWSSource) Describe() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, label, err := s.top(context.Background(), true)
	return label, err
}

// isChainProfile says whether a profile needs a service call: a role, an SSO login or a web identity token.
func (f awsFiles) isChainProfile(name string) bool {
	sec := f.section(name)
	return sec["role_arn"] != "" || sec["sso_session"] != "" || sec["sso_start_url"] != "" || sec["web_identity_token_file"] != ""
}

// top is the whole chain. dry skips the service calls (see Describe).
func (s *AWSSource) top(ctx context.Context, dry bool) (AWSCredentials, string, error) {
	f := s.files
	if f.envID != "" && f.envSecret != "" && (!f.explicit || !f.isChainProfile(f.profile)) {
		return AWSCredentials{AccessKeyID: f.envID, SecretAccessKey: f.envSecret, SessionToken: f.get("AWS_SESSION_TOKEN")}, "env:AWS_ACCESS_KEY_ID", nil
	}
	if file, arn := f.get("AWS_WEB_IDENTITY_TOKEN_FILE"), f.get("AWS_ROLE_ARN"); file != "" && arn != "" && !f.explicit {
		if dry {
			return AWSCredentials{}, "env:AWS_WEB_IDENTITY_TOKEN_FILE", nil
		}
		c, err := s.cached(ctx, "env:web-identity", func() (AWSCredentials, time.Time, error) {
			return s.webIdentity(ctx, "the environment", file, arn, f.get("AWS_ROLE_SESSION_NAME"), f.region)
		})
		return c, "env:AWS_WEB_IDENTITY_TOKEN_FILE", err
	}
	return s.profileCreds(ctx, f.profile, nil, dry)
}

// cached returns the keys stored under key while they are valid, else makes new ones with fetch.
func (s *AWSSource) cached(_ context.Context, key string, fetch func() (AWSCredentials, time.Time, error)) (AWSCredentials, error) {
	if c, ok := s.cache[key]; ok && (c.expires.IsZero() || s.Now().Add(awsRefreshEarly).Before(c.expires)) {
		return c.creds, nil
	}
	creds, exp, err := fetch()
	if err != nil {
		return AWSCredentials{}, err
	}
	s.cache[key] = awsCached{creds, exp}
	return creds, nil
}

// profileCreds resolves one profile. chain holds the profiles above it, to refuse loops.
func (s *AWSSource) profileCreds(ctx context.Context, name string, chain []string, dry bool) (AWSCredentials, string, error) {
	f := s.files
	for _, p := range chain {
		if p == name {
			return AWSCredentials{}, "", fmt.Errorf("AWS profiles %s loop back to %q", strings.Join(append(chain, name), " -> "), name)
		}
	}
	sec := f.section(name)
	label := "aws:profile " + name
	switch {
	case sec["role_arn"] != "" && sec["web_identity_token_file"] != "":
		if dry {
			return AWSCredentials{}, label + " (web identity)", nil
		}
		c, err := s.cached(ctx, "profile:"+name, func() (AWSCredentials, time.Time, error) {
			return s.webIdentity(ctx, "profile "+strconv.Quote(name), sec["web_identity_token_file"], sec["role_arn"],
				sec["role_session_name"], s.regionOf(sec))
		})
		return c, label + " (web identity)", err
	case sec["role_arn"] != "":
		return s.assumeRole(ctx, name, sec, chain, dry)
	case sec["sso_session"] != "" || sec["sso_start_url"] != "":
		if dry {
			err := s.checkSSO(name, sec)
			return AWSCredentials{}, label + " (sso)", err
		}
		c, err := s.cached(ctx, "profile:"+name, func() (AWSCredentials, time.Time, error) { return s.ssoCredentials(ctx, name, sec) })
		return c, label + " (sso)", err
	}
	if c, ok := f.staticKeys(name); ok {
		return c, label, nil
	}
	if sec["credential_process"] != "" {
		return AWSCredentials{}, "", fmt.Errorf("AWS profile %q uses credential_process, which Koine does not support: use SSO or a role, "+
			"set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or set AWS_BEARER_TOKEN_BEDROCK", name)
	}
	return AWSCredentials{}, "", f.noKeys(name)
}

func (s *AWSSource) regionOf(sec map[string]string) string {
	if r := sec["region"]; r != "" {
		return r
	}
	return s.files.region
}

// assumeRole calls STS AssumeRole with the keys of the profile's source.
func (s *AWSSource) assumeRole(ctx context.Context, name string, sec map[string]string, chain []string, dry bool) (AWSCredentials, string, error) {
	f := s.files
	if sec["mfa_serial"] != "" {
		return AWSCredentials{}, "", fmt.Errorf("AWS profile %q uses mfa_serial, which Koine does not support (it cannot ask for a code)", name)
	}
	var src AWSCredentials
	var srcLabel string
	switch {
	case sec["source_profile"] != "":
		sp := sec["source_profile"]
		if f.section(sp) == nil && f.creds[sp] == nil {
			return AWSCredentials{}, "", fmt.Errorf("AWS profile %q has source_profile %q, which is not in %s or %s", name, sp, f.credFile, f.confFile)
		}
		c, l, err := s.profileCreds(ctx, sp, append(chain, name), dry)
		if err != nil {
			return AWSCredentials{}, "", err
		}
		src, srcLabel = c, strings.TrimPrefix(l, "aws:profile ")
	case sec["credential_source"] == "Environment":
		if f.envID == "" || f.envSecret == "" {
			return AWSCredentials{}, "", fmt.Errorf("AWS profile %q has credential_source = Environment but AWS_ACCESS_KEY_ID and "+
				"AWS_SECRET_ACCESS_KEY are not set", name)
		}
		src, srcLabel = AWSCredentials{AccessKeyID: f.envID, SecretAccessKey: f.envSecret, SessionToken: f.get("AWS_SESSION_TOKEN")}, "environment"
	case sec["credential_source"] != "":
		return AWSCredentials{}, "", fmt.Errorf("AWS profile %q uses credential_source = %s, which Koine does not support (only Environment)", name,
			sec["credential_source"])
	default:
		return AWSCredentials{}, "", fmt.Errorf("AWS profile %q has role_arn but no source_profile or credential_source", name)
	}
	label := "aws:profile " + name + " (assume role, source " + srcLabel + ")"
	if dry {
		return AWSCredentials{}, label, nil
	}
	// the key includes the source's access key so that a replaced login does not keep serving the role made from the old one
	c, err := s.cached(ctx, "profile:"+name+":"+src.AccessKeyID, func() (AWSCredentials, time.Time, error) {
		form := url.Values{"Action": {"AssumeRole"}, "Version": {"2011-06-15"}, "RoleArn": {sec["role_arn"]},
			"RoleSessionName": {s.sessionName(sec["role_session_name"])}}
		if v := sec["external_id"]; v != "" {
			form.Set("ExternalId", v)
		}
		if v := sec["duration_seconds"]; v != "" {
			form.Set("DurationSeconds", v)
		}
		return s.callSTS(ctx, "profile "+strconv.Quote(name), s.regionOf(sec), &src, form)
	})
	return c, label, err
}

func (s *AWSSource) sessionName(configured string) string {
	if configured != "" {
		return configured
	}
	return "koine-" + strconv.FormatInt(s.Now().Unix(), 10)
}

// webIdentity exchanges the token in a file for role keys (AssumeRoleWithWebIdentity, which is not signed).
func (s *AWSSource) webIdentity(ctx context.Context, where, file, arn, session, region string) (AWSCredentials, time.Time, error) {
	b, err := os.ReadFile(file) //nolint:gosec // the path comes from the user's AWS configuration or environment
	if err != nil {
		return AWSCredentials{}, time.Time{}, fmt.Errorf("the web identity token of %s cannot be read: %w", where, errors.Unwrap(err))
	}
	form := url.Values{"Action": {"AssumeRoleWithWebIdentity"}, "Version": {"2011-06-15"}, "RoleArn": {arn},
		"RoleSessionName": {s.sessionName(session)}, "WebIdentityToken": {strings.TrimSpace(string(b))}}
	return s.callSTS(ctx, where, region, nil, form)
}

type stsDoc struct {
	Result struct {
		Credentials struct {
			AccessKeyID     string `xml:"AccessKeyId"`
			SecretAccessKey string `xml:"SecretAccessKey"`
			SessionToken    string `xml:"SessionToken"`
			Expiration      string `xml:"Expiration"`
		} `xml:"Credentials"`
	} `xml:",any"`
	Error struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
}

// callSTS posts a form to STS, signed with signWith when given, and reads the credentials of the answer.
func (s *AWSSource) callSTS(ctx context.Context, where, region string, signWith *AWSCredentials, form url.Values) (AWSCredentials, time.Time, error) {
	if !validRegion(region) {
		return AWSCredentials{}, time.Time{}, fmt.Errorf("%s: %q is not an AWS region name", where, region)
	}
	endpoint := s.files.get("AWS_ENDPOINT_URL_STS")
	if endpoint == "" {
		endpoint = "https://sts." + region + ".amazonaws.com/"
	}
	body := []byte(form.Encode())
	headers := map[string]string{"content-type": "application/x-www-form-urlencoded; charset=utf-8"}
	if signWith != nil {
		signed, err := SigV4{Creds: *signWith, Region: region, Service: "sts"}.Sign("POST", endpoint, headers, body, s.Now())
		if err != nil {
			return AWSCredentials{}, time.Time{}, fmt.Errorf("%s: %w", where, err)
		}
		for k, v := range signed {
			headers[k] = v
		}
	}
	data, status, err := s.send(ctx, http.MethodPost, endpoint, headers, body)
	if err != nil {
		return AWSCredentials{}, time.Time{}, fmt.Errorf("STS %s for %s: %w", form.Get("Action"), where, err)
	}
	var doc stsDoc
	parseErr := xml.Unmarshal(data, &doc)
	if status != http.StatusOK || parseErr != nil || doc.Result.Credentials.AccessKeyID == "" {
		msg := strings.TrimSpace(doc.Error.Code + " " + doc.Error.Message)
		if msg == "" {
			msg = "unexpected answer"
		}
		return AWSCredentials{}, time.Time{}, fmt.Errorf("STS %s for %s failed (HTTP %d): %s", form.Get("Action"), where, status, msg)
	}
	c := doc.Result.Credentials
	exp, _ := time.Parse(time.RFC3339, c.Expiration) // a missing or odd expiry means the keys are fetched again every time
	if exp.IsZero() {
		exp = s.Now()
	}
	return AWSCredentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken}, exp, nil
}

// send makes one HTTP request and returns the body (at most 1 MiB) and the status.
func (s *AWSSource) send(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) ([]byte, int, error) {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return data, resp.StatusCode, err
}
