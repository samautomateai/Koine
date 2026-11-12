package llm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoCredentials is wrapped by the errors that say no ambient cloud credential (AWS keys, a Google credentials file) was found anywhere,
// as opposed to one that was found and is unusable.
var ErrNoCredentials = errors.New("no credentials found")

// DefaultAWSRegion is used when neither the environment nor the profile names a region (Pi: "defaults to us-east-1").
const DefaultAWSRegion = "us-east-1"

// unsupportedProfileKeys are settings of a shared-config profile that need AWS services or helper programs to turn into keys. Koine's
// credential chain stops at static keys, so such a profile is refused with a message instead of being half-read.
var unsupportedProfileKeys = []string{"role_arn", "sso_session", "sso_start_url", "sso_account_id", "credential_process", "web_identity_token_file",
	"source_profile", "credential_source"}

// LoadAWSCredentials finds AWS keys the way the SDKs begin their default chain, and no further: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY
// (and AWS_SESSION_TOKEN) from get; then the profile named by AWS_PROFILE (default "default") in the shared credentials file
// (AWS_SHARED_CREDENTIALS_FILE, else ~/.aws/credentials) and the shared config file (AWS_CONFIG_FILE, else ~/.aws/config). It also returns
// the region: AWS_REGION, AWS_DEFAULT_REGION, the profile's region, else DefaultAWSRegion. SSO, assumed roles, credential_process, web
// identity, ECS and EC2 instance credentials are not supported. The errors name files and variables, never values.
func LoadAWSCredentials(get func(string) string) (AWSCredentials, string, error) {
	profile := get("AWS_PROFILE")
	if profile == "" {
		profile = get("AWS_DEFAULT_PROFILE")
	}
	explicit := profile != ""
	if profile == "" {
		profile = "default"
	}
	region := get("AWS_REGION")
	if region == "" {
		region = get("AWS_DEFAULT_REGION")
	}
	home := get("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	path := func(env, rel string) string {
		if p := get(env); p != "" {
			return p
		}
		return filepath.Join(home, ".aws", rel)
	}
	credFile, confFile := path("AWS_SHARED_CREDENTIALS_FILE", "credentials"), path("AWS_CONFIG_FILE", "config")
	creds, conf := readINI(credFile), readINI(confFile)
	section := conf["profile "+profile]
	if section == nil && profile == "default" {
		section = conf["default"]
	}
	if region == "" {
		region = section["region"]
	}
	if region == "" {
		region = DefaultAWSRegion
	}
	if id, secret := get("AWS_ACCESS_KEY_ID"), get("AWS_SECRET_ACCESS_KEY"); id != "" && secret != "" {
		return AWSCredentials{AccessKeyID: id, SecretAccessKey: secret, SessionToken: get("AWS_SESSION_TOKEN")}, region, nil
	}
	for _, key := range unsupportedProfileKeys {
		if section[key] != "" {
			return AWSCredentials{}, region, fmt.Errorf("AWS profile %q uses %s, which Koine does not support: put static keys in the profile, "+
				"set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or set AWS_BEARER_TOKEN_BEDROCK", profile, key)
		}
	}
	for _, src := range []map[string]string{creds[profile], section} { // the credentials file wins over the config file
		if id, secret := src["aws_access_key_id"], src["aws_secret_access_key"]; id != "" && secret != "" {
			return AWSCredentials{AccessKeyID: id, SecretAccessKey: secret, SessionToken: src["aws_session_token"]}, region, nil
		}
	}
	if explicit {
		return AWSCredentials{}, region, fmt.Errorf("AWS profile %q has no access keys in %s or %s", profile, credFile, confFile)
	}
	return AWSCredentials{}, region, fmt.Errorf("%w for AWS: set AWS_BEARER_TOKEN_BEDROCK, or AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, "+
		"or AWS_PROFILE for a profile with keys in ~/.aws/credentials", ErrNoCredentials)
}

// readINI reads an AWS shared file into section -> key -> value. A missing or unreadable file is empty. Indented lines (the nested
// settings of a key such as s3) are skipped; so are comments starting with # or ;.
func readINI(path string) map[string]map[string]string {
	out := map[string]map[string]string{}
	b, err := os.ReadFile(path) //nolint:gosec // the path is the user's AWS configuration, chosen by AWS's own variables
	if err != nil {
		return out
	}
	var cur map[string]string
	for _, line := range strings.Split(string(b), "\n") {
		raw := strings.TrimRight(line, "\r")
		l := strings.TrimSpace(raw)
		switch {
		case l == "" || l[0] == '#' || l[0] == ';':
		case l[0] == '[' && strings.HasSuffix(l, "]"):
			name := strings.Join(strings.Fields(l[1:len(l)-1]), " ")
			if out[name] == nil {
				out[name] = map[string]string{}
			}
			cur = out[name]
		case cur != nil && raw[0] != ' ' && raw[0] != '\t':
			if k, v, ok := strings.Cut(l, "="); ok {
				cur[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
			}
		}
	}
	return out
}
