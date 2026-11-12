package app

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"koine/core/agent"
	"koine/core/auth"
	"koine/core/llm"
)

// readSecret asks for a secret: through Prompt when a frontend set one, else on the terminal without echo. Print and server runs have no
// user to ask, so they refuse; a key typed into a networked session must not be possible.
func (a *App) readSecret(question string) (string, error) {
	if a.Prompt != nil {
		line, err := a.Prompt(question, true)
		if err == nil && strings.TrimSpace(line) == "" {
			err = errors.New("nothing was entered")
		}
		return strings.TrimSpace(line), err
	}
	if a.NoUserBash {
		return "", errors.New("there is no terminal to ask on; run `koine auth login <provider>` instead")
	}
	return auth.ReadSecret(os.Stdin, auth.IsTerminal(os.Stdin), os.Stderr, question)
}

// loginProviders are the providers /login offers: the built-in ones and those declared in models.json.
func (a *App) loginProviders() []string {
	set := map[string]bool{}
	for _, p := range llm.Providers() {
		set[p.ID] = true
	}
	for k := range a.providers.Custom {
		set[k] = true
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// authCommand handles /login [provider] and /logout [provider]. It reports whether it handled name. Koine stores API keys only; Pi's
// browser (OAuth) logins are not implemented, so /login always asks for a key.
func (a *App) authCommand(name, args string, emit func(agent.Event)) bool {
	if name != "login" && name != "logout" {
		return false
	}
	notice := func(s string) { emit(agent.Event{Type: "notice", Text: s}) }
	fail := func(err error) { emit(agent.Event{Type: "error", Text: err.Error()}) }
	store := a.providers.Store
	provider := llm.CanonicalProvider(strings.TrimSpace(args))
	if name == "logout" {
		a.logout(provider, notice, fail)
		return true
	}
	if provider == "" {
		names := a.loginProviders()
		i := -1
		if a.Choose != nil {
			i = a.Choose("Log in to which provider?", names)
		}
		if i < 0 || i >= len(names) {
			notice("usage: /login <provider>   providers: " + strings.Join(names, ", ") + "\nThe key is stored in " + store.Path() +
				" (mode 0600). Koine stores API keys only; OAuth logins are not implemented.")
			return true
		}
		provider = names[i]
	}
	if _, builtin := llm.BuiltinProvider(provider); !builtin {
		if _, custom := a.providers.Custom[provider]; !custom {
			fail(fmt.Errorf("unknown provider %q; known: %s", provider, strings.Join(a.loginProviders(), ", ")))
			return true
		}
	}
	key, err := a.readSecret(fmt.Sprintf("API key for %s (input hidden; a !command or $VAR works too): ", provider))
	if err != nil {
		fail(err)
		return true
	}
	if err := store.SetAPIKey(provider, key, nil); err != nil {
		fail(err)
		return true
	}
	notice(fmt.Sprintf("stored the key for %s in %s", provider, store.Path()))
	return true
}

func (a *App) logout(provider string, notice func(string), fail func(error)) {
	store := a.providers.Store
	if provider == "" {
		names, err := store.Providers()
		switch {
		case err != nil:
			fail(err)
		case len(names) == 0:
			notice("no stored credentials (" + store.Path() + ")")
		default:
			notice("usage: /logout <provider>   stored: " + strings.Join(names, ", "))
		}
		return
	}
	had, err := store.Remove(provider)
	switch {
	case err != nil:
		fail(err)
	case !had:
		notice("no stored credential for " + provider)
	default:
		notice("removed the stored credential for " + provider + "; environment variables and models.json are unchanged")
	}
}
