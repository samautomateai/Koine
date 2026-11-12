package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// awsHome writes ~/.aws/credentials and ~/.aws/config into a temporary home and returns an environment lookup that points at it.
func awsHome(t *testing.T, credentials, config string, env map[string]string) func(string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"credentials": credentials, "config": config} {
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return func(k string) string {
		if k == "HOME" {
			return home
		}
		return env[k]
	}
}

// loadAWS resolves the credentials of a configuration that needs no service call.
func loadAWS(get func(string) string) (AWSCredentials, string, error) {
	s := NewAWSSource(get, nil)
	c, err := s.Credentials(context.Background())
	return c, s.Region(), err
}

func TestAWSCredentialsFromEnvironment(t *testing.T) {
	get := awsHome(t, "", "", map[string]string{"AWS_ACCESS_KEY_ID": "AKIDFAKE", "AWS_SECRET_ACCESS_KEY": "fake-secret",
		"AWS_SESSION_TOKEN": "fake-token", "AWS_DEFAULT_REGION": "eu-west-1"})
	c, region, err := loadAWS(get)
	if err != nil || c.AccessKeyID != "AKIDFAKE" || c.SecretAccessKey != "fake-secret" || c.SessionToken != "fake-token" || region != "eu-west-1" {
		t.Fatalf("got %+v %q %v", c, region, err)
	}
	get = awsHome(t, "", "", map[string]string{"AWS_ACCESS_KEY_ID": "AKIDFAKE", "AWS_SECRET_ACCESS_KEY": "fake-secret", "AWS_REGION": "ap-south-1",
		"AWS_DEFAULT_REGION": "eu-west-1"})
	if _, region, _ = loadAWS(get); region != "ap-south-1" {
		t.Errorf("AWS_REGION should beat AWS_DEFAULT_REGION, got %q", region)
	}
}

func TestAWSCredentialsFromSharedFiles(t *testing.T) {
	creds := "# comment\n[default]\naws_access_key_id = AKIDDEFAULT\naws_secret_access_key = default-secret\n\n" +
		"[work]\naws_access_key_id=AKIDWORK\naws_secret_access_key=work-secret\naws_session_token=work-token\n"
	conf := "[default]\nregion = us-west-2\n\n[profile work]\nregion = eu-central-1\ns3 =\n  max_concurrent_requests = 5\n\n" +
		"[profile inline]\naws_access_key_id = AKIDINLINE\naws_secret_access_key = inline-secret\n"
	c, region, err := loadAWS(awsHome(t, creds, conf, nil))
	if err != nil || c.AccessKeyID != "AKIDDEFAULT" || region != "us-west-2" {
		t.Fatalf("default profile: %+v %q %v", c, region, err)
	}
	c, region, err = loadAWS(awsHome(t, creds, conf, map[string]string{"AWS_PROFILE": "work"}))
	if err != nil || c.AccessKeyID != "AKIDWORK" || c.SessionToken != "work-token" || region != "eu-central-1" {
		t.Fatalf("work profile: %+v %q %v", c, region, err)
	}
	c, _, err = loadAWS(awsHome(t, creds, conf, map[string]string{"AWS_PROFILE": "inline"}))
	if err != nil || c.AccessKeyID != "AKIDINLINE" {
		t.Fatalf("keys in the config file: %+v %v", c, err)
	}
	// environment keys win over the profile, as in the SDKs
	c, _, _ = loadAWS(awsHome(t, creds, conf, map[string]string{"AWS_PROFILE": "work", "AWS_ACCESS_KEY_ID": "AKIDENV",
		"AWS_SECRET_ACCESS_KEY": "env-secret"}))
	if c.AccessKeyID != "AKIDENV" {
		t.Errorf("env keys should win, got %q", c.AccessKeyID)
	}
	// AWS_SHARED_CREDENTIALS_FILE and AWS_CONFIG_FILE move the files
	other := filepath.Join(t.TempDir(), "creds")
	if err := os.WriteFile(other, []byte("[default]\naws_access_key_id=AKIDOTHER\naws_secret_access_key=other-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, err = loadAWS(awsHome(t, "", "", map[string]string{"AWS_SHARED_CREDENTIALS_FILE": other}))
	if err != nil || c.AccessKeyID != "AKIDOTHER" {
		t.Fatalf("AWS_SHARED_CREDENTIALS_FILE: %+v %v", c, err)
	}
}

func TestAWSCredentialsErrors(t *testing.T) {
	_, region, err := loadAWS(awsHome(t, "", "", nil))
	if !errors.Is(err, ErrNoCredentials) || !strings.Contains(err.Error(), "AWS_BEARER_TOKEN_BEDROCK") || region != DefaultAWSRegion {
		t.Errorf("no credentials: %v %q", err, region)
	}
	_, _, err = loadAWS(awsHome(t, "[x]\naws_access_key_id=a\naws_secret_access_key=b\n", "", map[string]string{"AWS_PROFILE": "missing"}))
	if err == nil || !strings.Contains(err.Error(), `"missing"`) {
		t.Errorf("named profile without keys: %v", err)
	}
	for _, key := range []string{"credential_process"} {
		_, _, err = loadAWS(awsHome(t, "", "[profile p]\n"+key+" = something\n", map[string]string{"AWS_PROFILE": "p"}))
		if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "something") {
			t.Errorf("%s: %v", key, err)
		}
	}
}
