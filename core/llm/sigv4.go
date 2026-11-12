package llm

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AWSCredentials are the keys a request is signed with. SessionToken belongs to temporary credentials (empty for long-lived keys).
type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// SigV4 signs HTTP requests with AWS Signature Version 4, written against the standard library
// (https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv4-create-signed-request.html). It signs the host, the date, the session
// token and the headers the caller names, which is all a JSON POST to Bedrock needs.
type SigV4 struct {
	Creds   AWSCredentials
	Region  string
	Service string
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(data))
	return m.Sum(nil)
}

func hexSHA256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// signingKey derives the key for one day, region and service from the secret access key.
func signingKey(secret, date, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, "aws4_request")
}

// awsEscape percent-encodes s as SigV4 wants: every byte except A-Z a-z 0-9 - _ . ~ (and "/" when keepSlash).
func awsEscape(s string, keepSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~', c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// canonicalQuery sorts the query parameters by encoded name, then value.
func canonicalQuery(q url.Values) string {
	var pairs []string
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, awsEscape(k, false)+"="+awsEscape(v, false))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// Sign returns the headers to add to a request: x-amz-date, x-amz-security-token (when there is a session token) and authorization.
// headers holds the request headers that are signed besides host and the date (for example content-type), exactly as they will be sent.
// rawURL is the address as it will be sent: its path is already percent-encoded once, and Sign encodes it a second time for the canonical
// request, as AWS requires of every service but S3.
func (s SigV4) Sign(method, rawURL string, headers map[string]string, payload []byte, now time.Time) (map[string]string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	stamp, date := now.Format("20060102T150405Z"), now.Format("20060102")
	signed := map[string]string{"host": u.Host, "x-amz-date": stamp}
	if s.Creds.SessionToken != "" {
		signed["x-amz-security-token"] = s.Creds.SessionToken
	}
	for k, v := range headers {
		signed[strings.ToLower(k)] = strings.Join(strings.Fields(v), " ")
	}
	names := make([]string, 0, len(signed))
	for k := range signed {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + signed[k] + "\n")
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	signedList := strings.Join(names, ";")
	canonical := strings.Join([]string{method, awsEscape(path, true), canonicalQuery(u.Query()), canonHeaders.String(), signedList,
		hexSHA256(payload)}, "\n")
	scope := date + "/" + s.Region + "/" + s.Service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hexSHA256([]byte(canonical))
	sig := hex.EncodeToString(hmacSHA256(signingKey(s.Creds.SecretAccessKey, date, s.Region, s.Service), toSign))
	out := map[string]string{
		"x-amz-date":    stamp,
		"authorization": "AWS4-HMAC-SHA256 Credential=" + s.Creds.AccessKeyID + "/" + scope + ", SignedHeaders=" + signedList + ", Signature=" + sig,
	}
	if s.Creds.SessionToken != "" {
		out["x-amz-security-token"] = s.Creds.SessionToken
	}
	return out, nil
}
