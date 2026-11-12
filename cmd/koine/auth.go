package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"koine/core/app"
	"koine/core/auth"
	"koine/core/auth/oauth"
	"koine/core/llm"
)

const authUsage = `usage: koine auth <command>

  login <provider> [--api-key] [--no-browser]   sign in with your account (anthropic, openai-codex, github-copilot, openrouter) or, with
                                                --api-key and for every other provider, store an API key (asked without echo).
                                                --no-browser: paste a code (anthropic) or use a device code (openai-codex)
  logout <provider>                             remove the stored credential (environment variables and models.json are unchanged)
  check (--provider P | --model M) [--json] [--credentials] [--no-refresh]
                                                print ready, not_ready or invalid; exit 0, 1 or 2
  print-api-key (--provider P | --model M)      print the API key that would be used
  print-bearer-token (--provider P | --model M) [--min-expiry 30m]
                                                print a bearer token (an account login or ANTHROPIC_AUTH_TOKEN), renewed first if it lapses within D

Credentials are stored in auth.json in the agent directory (~/.koine, or $KOINE_AGENT_DIR), mode 0600. Account logins are renewed
automatically when they expire. A key may be written as "!command" or "$VAR" in auth.json. "koine login" and "koine logout" are
shortcuts for "koine auth login" and "koine auth logout".
Order of use: --api-key, auth.json, apiKey in models.json, the provider's environment variables. print-api-key and
check --credentials write secrets to stdout.
`

// oauthClient is what the account logins use; tests replace it so that no request leaves the machine.
var oauthClient oauth.Client

// authFlags are the options of the auth commands; the maps say which options take a value.
type authFlags struct {
	provider, model, minExpiry                 string
	json, credentials, noRefresh, apiKey, noBr bool
	pos                                        []string
}

func parseAuthFlags(args []string) (authFlags, error) {
	var f authFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			f.pos = append(f.pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		value := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("option --%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "provider":
			f.provider, err = value()
		case "model":
			f.model, err = value()
		case "min-expiry":
			f.minExpiry, err = value()
		case "json", "credentials", "no-refresh", "api-key", "no-browser":
			if hasVal {
				return f, fmt.Errorf("option --%s does not take a value", name)
			}
			switch name {
			case "json":
				f.json = true
			case "credentials":
				f.credentials = true
			case "no-refresh":
				f.noRefresh = true
			case "api-key":
				f.apiKey = true
			case "no-browser":
				f.noBr = true
			}
		default:
			return f, fmt.Errorf("unknown option %q (try koine auth --help)", a)
		}
		if err != nil {
			return f, err
		}
	}
	return f, nil
}

// runAuth is `koine auth ...`. Secrets go to stdout only from the print and check --credentials commands; everything else says what it
// did on stdout and complains on stderr.
func runAuth(args []string, st streams) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		if len(args) == 0 {
			fmt.Fprint(st.Err, authUsage)
			return 2
		}
		fmt.Fprint(st.Out, authUsage)
		return 0
	}
	f, err := parseAuthFlags(args[1:])
	if err != nil {
		fmt.Fprintln(st.Err, "koine auth:", err)
		return 2
	}
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	ps := app.LoadProviderSetup(cwd, home)
	ps.OAuth = oauthClient
	ps.NoRefresh = f.noRefresh
	if d, err := time.ParseDuration(f.minExpiry); err == nil && args[0] == "print-bearer-token" {
		ps.Skew = d // a login that lapses sooner than this is renewed before it is printed
	}
	for _, w := range ps.Warnings {
		fmt.Fprintln(st.Err, "koine auth: warning:", w)
	}
	switch args[0] {
	case "login":
		return authLogin(f, ps, st)
	case "logout":
		return authLogout(f, ps, st)
	case "check":
		return authCheck(f, ps, st)
	case "print-api-key", "print-bearer-token":
		return authPrint(args[0], f, ps, st)
	}
	fmt.Fprintf(st.Err, "koine auth: unknown command %q\n%s", args[0], authUsage)
	return 2
}

// oneProvider is the provider a login or logout names, as stored.
func oneProvider(f authFlags, ps *app.ProviderSetup, st streams) (string, bool) {
	if len(f.pos) != 1 {
		fmt.Fprintln(st.Err, "koine auth: give exactly one provider")
		return "", false
	}
	p := llm.CanonicalProvider(f.pos[0])
	_, builtin := llm.BuiltinProvider(p)
	if _, custom := ps.Custom[p]; !builtin && !custom {
		fmt.Fprintf(st.Err, "koine auth: unknown provider %q\n", f.pos[0])
		return "", false
	}
	return p, true
}

func authLogin(f authFlags, ps *app.ProviderSetup, st streams) int {
	if f.apiKey && f.noBr {
		fmt.Fprintln(st.Err, "koine auth: --no-browser cannot be used with --api-key")
		return 2
	}
	p, ok := oneProvider(f, ps, st)
	if !ok {
		return 2
	}
	if !st.InTTY {
		fmt.Fprintln(st.Err, "koine auth: login needs an interactive terminal")
		return 1
	}
	info, _ := llm.BuiltinProvider(p)
	if info.OAuthOnly && f.apiKey {
		fmt.Fprintf(st.Err, "koine auth: %s has no API key; sign in without --api-key\n", p)
		return 2
	}
	if oauth.Has(p) && !f.apiKey {
		return authOAuth(p, f, ps, st)
	}
	key, err := auth.ReadSecret(st.In, st.InTTY, st.Err, "API key for "+p+": ")
	if err != nil {
		fmt.Fprintln(st.Err, "koine auth:", err)
		return 1
	}
	if err := ps.Store.SetAPIKey(p, key, nil); err != nil {
		fmt.Fprintln(st.Err, "koine auth:", err)
		return 1
	}
	fmt.Fprintf(st.Out, "Saved the API key for %s to %s\n", p, ps.Store.Path())
	return 0
}

// authOAuth signs in with an account. The terminal both shows the address and can take a pasted code while the browser callback is
// awaited (ui.Race), which is how a login over SSH finishes.
func authOAuth(p string, f authFlags, ps *app.ProviderSetup, st streams) int {
	in := bufio.NewReader(st.In)
	ui := oauth.UI{
		Notify: func(m string) { fmt.Fprintln(st.Out, m) },
		Prompt: func(q string, secret bool) (string, error) {
			if secret {
				return auth.ReadSecret(in, st.InTTY, st.Err, q)
			}
			fmt.Fprint(st.Err, q)
			line, err := in.ReadString('\n')
			if err != nil && line == "" {
				return "", fmt.Errorf("no input: %w", err)
			}
			return strings.TrimSpace(line), nil
		},
		OpenBrowser: app.BrowserOpener,
		Race:        true,
	}
	msg, err := ps.LoginOAuth(context.Background(), p, ui, oauth.Options{NoBrowser: f.noBr})
	if err != nil {
		fmt.Fprintln(st.Err, "koine auth:", err)
		return 1
	}
	fmt.Fprintln(st.Out, msg)
	return 0
}

func authLogout(f authFlags, ps *app.ProviderSetup, st streams) int {
	p, ok := oneProvider(f, ps, st)
	if !ok {
		return 2
	}
	had, err := ps.Store.Remove(p)
	switch {
	case err != nil:
		fmt.Fprintln(st.Err, "koine auth:", err)
		return 1
	case had:
		fmt.Fprintf(st.Out, "Removed the stored credential for %s (environment variables and models.json are unchanged)\n", p)
	default:
		fmt.Fprintf(st.Out, "No stored credential for %s\n", p)
	}
	return 0
}

// target is the provider that --provider or --model names. A model may be given with a provider; the provider then decides.
func (f authFlags) target(ps *app.ProviderSetup) (string, error) {
	switch {
	case f.provider != "":
		return llm.CanonicalProvider(f.provider), nil
	case f.model != "":
		p, _, err := ps.Router.ProviderOf(f.model)
		return p, err
	}
	return "", errors.New("give --provider or --model")
}

// checkResult is the JSON of `auth check`. Pi documents the three states and the exit codes but not the JSON shape, so this one is
// Koine's own.
type checkResult struct {
	Status      string           `json:"status"`
	Provider    string           `json:"provider"`
	Source      string           `json:"source,omitempty"`
	Reason      string           `json:"reason,omitempty"`
	Credentials *checkCredential `json:"credentials,omitempty"`
}

type checkCredential struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

func authCheck(f authFlags, ps *app.ProviderSetup, st streams) int {
	p, err := f.target(ps)
	if err != nil {
		fmt.Fprintln(st.Err, "koine auth:", err)
		return 2
	}
	res := checkResult{Provider: p}
	code := 0
	c, cerr := ps.Router.Credential(p)
	var ce *llm.CredentialError
	switch {
	case cerr == nil:
		res.Status, res.Source = "ready", c.Source
		if res.Source == "" {
			res.Source = "none needed"
		}
		if f.credentials && c.Key != "" {
			typ := auth.TypeAPIKey
			if c.OAuth {
				typ = auth.TypeOAuth
			} else if c.Bearer {
				typ = "bearer"
			}
			res.Credentials = &checkCredential{Type: typ, Key: c.Key}
		}
	case errors.As(cerr, &ce) && ce.Missing:
		res.Status, res.Reason, code = "not_ready", cerr.Error(), 1
	default:
		res.Status, res.Reason, code = "invalid", cerr.Error(), 2
	}
	if f.json {
		enc := json.NewEncoder(st.Out)
		_ = enc.Encode(res)
		return code
	}
	fmt.Fprintln(st.Out, res.Status)
	if res.Credentials != nil {
		fmt.Fprintln(st.Out, res.Credentials.Key)
	}
	switch {
	case res.Reason != "":
		fmt.Fprintln(st.Err, res.Reason)
	case res.Source != "":
		fmt.Fprintln(st.Err, "credential from", res.Source)
	}
	return code
}

func authPrint(cmd string, f authFlags, ps *app.ProviderSetup, st streams) int {
	if cmd == "print-bearer-token" && f.minExpiry != "" {
		if _, err := time.ParseDuration(f.minExpiry); err != nil {
			fmt.Fprintf(st.Err, "koine auth: --min-expiry %q is not a duration such as 30m\n", f.minExpiry)
			return 2
		}
	}
	p, err := f.target(ps)
	if err != nil {
		fmt.Fprintln(st.Err, "koine auth:", err)
		return 2
	}
	c, err := ps.Router.Credential(p)
	switch {
	case err != nil:
		fmt.Fprintln(st.Err, "koine auth:", err)
		return 1
	case c.Key == "":
		fmt.Fprintf(st.Err, "koine auth: %s needs no credential, so there is nothing to print\n", p)
		return 1
	case cmd == "print-api-key" && c.Bearer:
		fmt.Fprintf(st.Err, "koine auth: the credential for %s is a bearer token (%s); use print-bearer-token\n", p, c.Source)
		return 1
	case cmd == "print-bearer-token" && !c.Bearer:
		fmt.Fprintf(st.Err, "koine auth: %s has an API key (%s), not a bearer token\n", p, c.Source)
		return 1
	}
	fmt.Fprintln(st.Out, c.Key)
	return 0
}
