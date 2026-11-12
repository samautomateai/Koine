package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// truthy reads the on/off environment variables of Bedrock ("1" or "true").
func truthy(v string) bool { return v == "1" || strings.EqualFold(v, "true") }

// bedrock builds the Amazon Bedrock provider. The credential is, in order: a bearer token (AWS_BEARER_TOKEN_BEDROCK, --api-key, auth.json
// or models.json), then AWS keys from the environment or a shared-file profile, SSO login or role (AWSSource). The endpoint is the provider's
// baseUrl in models.json, else AWS_ENDPOINT_URL_BEDROCK_RUNTIME, else the regional bedrock-runtime address. Settings may also come from
// the `env` object of the provider's auth.json entry. AWS_BEDROCK_SKIP_AUTH and AWS_BEDROCK_FORCE_CACHE are Pi's switches for proxies
// and application inference profiles; AWS_BEDROCK_FORCE_HTTP1 is not acted on.
func (r *Router) bedrock(s setup, c Credential, headers map[string]string) (Provider, error) {
	get := func(k string) string { return r.envFor(c.Env, k) }
	src := NewAWSSource(get, r.HTTP)
	region := src.Region()
	if !validRegion(region) {
		return nil, fmt.Errorf("%s: %q is not an AWS region name", s.name, region)
	}
	base := s.base
	if base == "" {
		base = get("AWS_ENDPOINT_URL_BEDROCK_RUNTIME")
	}
	if base == "" {
		base = "https://bedrock-runtime." + region + ".amazonaws.com"
	}
	return &Bedrock{Name: s.name, BaseURL: strings.TrimRight(base, "/"), Region: region, Bearer: c.Key, SkipAuth: truthy(get("AWS_BEDROCK_SKIP_AUTH")),
		ForceCache: truthy(get("AWS_BEDROCK_FORCE_CACHE")), Creds: func() (AWSCredentials, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // Creds takes no context; STS and the SSO portal need a bound
			defer cancel()
			return src.Credentials(ctx)
		}, Headers: headers,
		HTTP: r.HTTP, Retry: r.Retry}, nil
}

// validRegion accepts what a region name looks like (letters, digits, hyphens) so that it cannot rewrite the host it is put into.
func validRegion(s string) bool {
	return s != "" && onlyChars(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-")
}

func onlyChars(s, allowed string) bool {
	for _, r := range s {
		if !strings.ContainsRune(allowed, r) {
			return false
		}
	}
	return true
}

// vertex builds the Google Vertex AI provider for Gemini models. With an API key (GOOGLE_CLOUD_API_KEY, --api-key, auth.json or models.json)
// it uses Vertex's express mode at aiplatform.googleapis.com. Otherwise it signs in with Application Default Credentials (a service
// account key file or an authorized-user file, see GoogleTokenSource) and needs a project (GOOGLE_CLOUD_PROJECT, GCLOUD_PROJECT, or the
// credentials file's) and a location (GOOGLE_CLOUD_LOCATION). The provider's baseUrl in models.json replaces the address.
func (r *Router) vertex(s setup, c Credential, headers map[string]string) (Provider, error) {
	get := func(k string) string { return r.envFor(c.Env, k) }
	g := &Gemini{Name: s.name, BaseURL: strings.TrimRight(s.base, "/"), Headers: headers, HTTP: r.HTTP, Retry: r.Retry}
	if c.Key != "" {
		if g.BaseURL == "" {
			g.BaseURL = "https://aiplatform.googleapis.com/v1"
		}
		g.APIKey, g.ModelPath = c.Key, "publishers/google/models/"
		return g, nil
	}
	src, err := LoadGoogleCredentials(get, r.HTTP)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}
	project := get("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		project = get("GCLOUD_PROJECT")
	}
	if project == "" {
		project = src.Project()
	}
	location := get("GOOGLE_CLOUD_LOCATION")
	switch {
	case project == "" || location == "":
		return nil, errors.New(s.name + ": set GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION (for example us-central1), or GOOGLE_CLOUD_API_KEY")
	case !onlyChars(project, "abcdefghijklmnopqrstuvwxyz0123456789-_.:") || !validRegion(location):
		return nil, fmt.Errorf("%s: %q or %q is not a valid Google Cloud project id or location", s.name, project, location)
	}
	if g.BaseURL == "" {
		g.BaseURL = "https://" + location + "-aiplatform.googleapis.com/v1"
		if location == "global" {
			g.BaseURL = "https://aiplatform.googleapis.com/v1"
		}
	}
	g.ModelPath = "projects/" + project + "/locations/" + location + "/publishers/google/models/"
	g.Token = src.Token
	return g, nil
}

// ambientCredential checks a cloud provider's sign-in without contacting it, for `koine auth check`. found is the source it would use.
func (r *Router) ambientCredential(s setup, c Credential) (Credential, error) {
	get := func(k string) string { return r.envFor(c.Env, k) }
	fail := func(err error) (Credential, error) {
		return Credential{}, &CredentialError{Provider: s.name, Missing: errors.Is(err, ErrNoCredentials), Reason: s.name + ": " + err.Error()}
	}
	switch s.api {
	case APIBedrock:
		if truthy(get("AWS_BEDROCK_SKIP_AUTH")) {
			c.Source = "env:AWS_BEDROCK_SKIP_AUTH"
			return c, nil
		}
		label, err := NewAWSSource(get, r.HTTP).Describe()
		if err != nil {
			return fail(err)
		}
		c.Source = label
	case APIVertex:
		if _, err := LoadGoogleCredentials(get, nil); err != nil {
			return fail(err)
		}
		c.Source = "adc:" + googleCredentialsPath(get)
	}
	return c, nil
}
