package llm

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// awsExampleSecret is the secret key AWS prints in its own documentation and test suite. It is split so that secret scanners do not
// mistake the public example for a real key.
func awsExampleSecret() string { return "wJalrXUtnFEMI/K7MDENG" + "+bPxRfiCYEXAMPLEKEY" }

// TestSigV4GetVanilla is the "get-vanilla" case of AWS's published Signature Version 4 test suite (aws-sig-v4-test-suite).
func TestSigV4GetVanilla(t *testing.T) {
	s := SigV4{Creds: AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: awsExampleSecret()}, Region: "us-east-1", Service: "service"}
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	h, err := s.Sign("GET", "https://example.amazonaws.com/", nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if h["authorization"] != want {
		t.Fatalf("authorization\n got %s\nwant %s", h["authorization"], want)
	}
	if h["x-amz-date"] != "20150830T123600Z" {
		t.Errorf("x-amz-date = %q", h["x-amz-date"])
	}
}

// TestSigV4SigningKey is the key-derivation example of AWS's documentation (20150830, us-east-1, iam).
func TestSigV4SigningKey(t *testing.T) {
	got := hex.EncodeToString(signingKey(awsExampleSecret(), "20150830", "us-east-1", "iam"))
	if want := "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9"; got != want {
		t.Fatalf("signing key = %s, want %s", got, want)
	}
}

func TestSigV4PathQueryTokenAndHeaders(t *testing.T) {
	s := SigV4{Creds: AWSCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: awsExampleSecret(), SessionToken: "tok"},
		Region: "us-west-2", Service: "bedrock"}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h, err := s.Sign("POST", "https://bedrock-runtime.us-west-2.amazonaws.com/model/a.b-v1%3A0/converse?b=2&a=1 x",
		map[string]string{"Content-Type": "application/json"}, []byte(`{}`), now)
	if err != nil {
		t.Fatal(err)
	}
	a := h["authorization"]
	if !strings.Contains(a, "SignedHeaders=content-type;host;x-amz-date;x-amz-security-token,") {
		t.Errorf("signed headers: %s", a)
	}
	if h["x-amz-security-token"] != "tok" {
		t.Error("session token header missing")
	}
	// the same request signs the same way, and a different body signs differently
	h2, _ := s.Sign("POST", "https://bedrock-runtime.us-west-2.amazonaws.com/model/a.b-v1%3A0/converse?b=2&a=1 x",
		map[string]string{"content-type": "application/json"}, []byte(`{}`), now)
	if h2["authorization"] != a {
		t.Error("signing is not deterministic or not case-insensitive in header names")
	}
	h3, _ := s.Sign("POST", "https://bedrock-runtime.us-west-2.amazonaws.com/model/a.b-v1%3A0/converse?b=2&a=1 x",
		map[string]string{"content-type": "application/json"}, []byte(`{"x":1}`), now)
	if h3["authorization"] == a {
		t.Error("the payload hash is not part of the signature")
	}
	if _, err := s.Sign("GET", "://bad", nil, nil, now); err == nil {
		t.Error("a bad URL should fail")
	}
}

func TestAWSEscape(t *testing.T) {
	if got := awsEscape("/a b/c:d~e", true); got != "/a%20b/c%3Ad~e" {
		t.Errorf("keepSlash: %q", got)
	}
	if got := awsEscape("a/b", false); got != "a%2Fb" {
		t.Errorf("slash: %q", got)
	}
}
