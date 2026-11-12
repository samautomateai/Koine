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

// awsFiles is what the environment and the shared files say about which profile is in use.
type awsFiles struct {
	get              func(string) string
	profile          string
	explicit         bool // the profile was named with AWS_PROFILE, as opposed to "default"
	region           string
	home             string
	credFile         string
	confFile         string
	creds, conf      map[string]map[string]string
	envID, envSecret string
}

func readAWSFiles(get func(string) string) awsFiles {
	f := awsFiles{get: get, profile: get("AWS_PROFILE")}
	if f.profile == "" {
		f.profile = get("AWS_DEFAULT_PROFILE")
	}
	f.explicit = f.profile != ""
	if !f.explicit {
		f.profile = "default"
	}
	f.region = get("AWS_REGION")
	if f.region == "" {
		f.region = get("AWS_DEFAULT_REGION")
	}
	f.home = get("HOME")
	if f.home == "" {
		f.home, _ = os.UserHomeDir()
	}
	path := func(env, rel string) string {
		if p := get(env); p != "" {
			return p
		}
		return filepath.Join(f.home, ".aws", rel)
	}
	f.credFile, f.confFile = path("AWS_SHARED_CREDENTIALS_FILE", "credentials"), path("AWS_CONFIG_FILE", "config")
	f.creds, f.conf = readINI(f.credFile), readINI(f.confFile)
	f.envID, f.envSecret = get("AWS_ACCESS_KEY_ID"), get("AWS_SECRET_ACCESS_KEY")
	if f.region == "" {
		f.region = f.section(f.profile)["region"]
	}
	if f.region == "" {
		f.region = DefaultAWSRegion
	}
	return f
}

// section is the settings of a profile in the config file ("[profile x]", or "[default]" for the default profile).
func (f awsFiles) section(name string) map[string]string {
	sec := f.conf["profile "+name]
	if sec == nil && name == "default" {
		sec = f.conf["default"]
	}
	return sec
}

// staticKeys are the access keys written in a profile; the credentials file wins over the config file.
func (f awsFiles) staticKeys(name string) (AWSCredentials, bool) {
	for _, src := range []map[string]string{f.creds[name], f.section(name)} {
		if id, secret := src["aws_access_key_id"], src["aws_secret_access_key"]; id != "" && secret != "" {
			return AWSCredentials{AccessKeyID: id, SecretAccessKey: secret, SessionToken: src["aws_session_token"]}, true
		}
	}
	return AWSCredentials{}, false
}

// noKeys is the error for a profile that yields nothing: a named one is a mistake, the default one means nothing is configured.
func (f awsFiles) noKeys(name string) error {
	if f.explicit || name != f.profile {
		return fmt.Errorf("AWS profile %q has no access keys, role or SSO settings in %s or %s", name, f.credFile, f.confFile)
	}
	return fmt.Errorf("%w for AWS: set AWS_BEARER_TOKEN_BEDROCK, or AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, "+
		"or AWS_PROFILE for a profile with keys, a role or SSO in ~/.aws/config", ErrNoCredentials)
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
