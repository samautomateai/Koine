package app

import (
	"context"
	"fmt"

	"koine/core/auth/oauth"
	"koine/core/mcp"
)

// LoginOAuth signs in to a provider with its account flow and stores the result in auth.json: tokens for a subscription, or the API key
// OpenRouter hands out. It returns a one-line description of what was stored. Both `koine auth login` and `/login` call it; they differ
// only in the UI they pass.
func (ps *ProviderSetup) LoginOAuth(ctx context.Context, provider string, ui oauth.UI, opt oauth.Options) (string, error) {
	flow, ok := oauth.Lookup(provider, ps.OAuth)
	if !ok {
		return "", fmt.Errorf("there is no account sign-in for %s (it has one for: %v); store an API key instead", provider, oauth.IDs())
	}
	if n := flow.Notice(); n != "" && ui.Notify != nil {
		ui.Notify(n)
	}
	res, err := flow.Login(ctx, ui, opt)
	if err != nil {
		return "", fmt.Errorf("signing in to %s: %w", flow.Name(), err)
	}
	if res.APIKey != "" {
		if err := ps.Store.SetAPIKey(provider, res.APIKey, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("signed in to %s; stored its API key in %s", flow.Name(), ps.Store.Path()), nil
	}
	if err := ps.Store.SetOAuth(provider, res.Token); err != nil {
		return "", err
	}
	return fmt.Sprintf("signed in to %s; stored the login in %s (it is renewed automatically)", flow.Name(), ps.Store.Path()), nil
}

// BrowserOpener opens a page in the user's browser; only http(s) addresses are passed on. A variable so that tests do not start one.
var BrowserOpener = mcp.OpenBrowser
