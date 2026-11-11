package app

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"koine/core/llm"
)

// candidate is a model the user can name: an entry of the catalog with its provider filled in.
type candidate struct{ Provider, ID, Name string }

func (c candidate) full() string {
	if c.Provider == "" {
		return c.ID
	}
	return c.Provider + "/" + c.ID
}

// inferProvider is the provider of a catalog entry that does not say, by the same bare-name rules the router uses.
func inferProvider(id string) string {
	switch {
	case strings.HasPrefix(id, "claude"):
		return "anthropic"
	case strings.HasPrefix(id, "gpt-"), strings.HasPrefix(id, "chatgpt-"), strings.HasPrefix(id, "o1"), strings.HasPrefix(id, "o3"), strings.HasPrefix(id, "o4"):
		return "openai"
	}
	return ""
}

func catalogCandidates(c *llm.Catalog) []candidate {
	var out []candidate
	if c == nil {
		return nil
	}
	for _, m := range c.Models {
		p := m.Provider
		if p == "" {
			p = inferProvider(m.ID)
		}
		out = append(out, candidate{Provider: p, ID: m.ID, Name: m.Name})
	}
	return out
}

// norm lower-cases s and drops everything but letters and digits, so "Sonnet 5.5", "sonnet-5-5" and "sonnet55" compare equal.
func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tier ranks how well a pattern names a candidate: 0 is an exact id, then equal after normalising, an id that starts with the pattern, an id
// that contains it, a display name that contains it. -1 means no match.
func tier(pattern string, c candidate) int {
	p, n := norm(pattern), norm(c.ID)
	switch {
	case p == "":
		return -1
	case strings.EqualFold(pattern, c.ID):
		return 0
	case p == n:
		return 1
	case strings.HasPrefix(n, p):
		return 2
	case strings.Contains(n, p):
		return 3
	case strings.Contains(norm(c.Name), p):
		return 4
	}
	return -1
}

func isDated(id string) bool {
	i := strings.LastIndex(id, "-")
	if i < 0 || len(id)-i-1 < 6 {
		return false
	}
	for _, r := range id[i+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// providerKnown says whether name is a provider the router can route to (built in or declared in the settings).
func (a *App) providerKnown(name string) bool {
	_, _, err := a.router.Resolve(name + "/x")
	return err == nil
}

// hasCredentials says whether a request to this model would find its API key.
func (a *App) hasCredentials(model string) bool {
	p, _, err := a.router.Resolve(model)
	if err != nil {
		return false
	}
	_, missing := p.(llm.Missing)
	return !missing
}

// bestMatch picks the candidates that name pattern best. With several providers offering equally good matches it prefers those whose key is
// set, and reports an error when that does not settle it. Among models of one provider it prefers undated names, then the latest.
func (a *App) bestMatch(pattern string, cands []candidate) (candidate, error) {
	best, bt := []candidate(nil), 99
	for _, c := range cands {
		switch t := tier(pattern, c); {
		case t < 0:
		case t < bt:
			best, bt = []candidate{c}, t
		case t == bt:
			best = append(best, c)
		}
	}
	if len(best) == 0 {
		return candidate{}, errors.New("no match")
	}
	providers := map[string]bool{}
	for _, c := range best {
		providers[c.Provider] = true
	}
	if len(providers) > 1 {
		var withKey []candidate
		for _, c := range best {
			if a.hasCredentials(c.full()) {
				withKey = append(withKey, c)
			}
		}
		if len(withKey) > 0 {
			best = withKey
		}
		providers = map[string]bool{}
		for _, c := range best {
			providers[c.Provider] = true
		}
		if len(providers) > 1 {
			var names []string
			for p := range providers {
				names = append(names, p)
			}
			sort.Strings(names)
			return candidate{}, fmt.Errorf("%q matches models from several providers (%s): add --provider", pattern, strings.Join(names, ", "))
		}
	}
	sort.SliceStable(best, func(i, j int) bool {
		if di, dj := isDated(best[i].ID), isDated(best[j].ID); di != dj {
			return dj
		}
		return best[i].ID > best[j].ID
	})
	return best[0], nil
}

// resolveModel turns what --model says into a model Koine can route to. An id the catalog knows exactly stays as written. Otherwise
// "provider/pattern" and "pattern" are matched against the catalog by id and name, ignoring case and punctuation. A model the catalog
// does not list (a local model, a custom provider) is taken as written when the router can send it somewhere. provider, from --provider,
// narrows the search to that provider and prefixes the result.
func (a *App) resolveModel(pattern, provider string) (string, error) {
	pattern = strings.TrimSpace(pattern)
	cands := catalogCandidates(a.Agent.Catalog)
	if pre, rest, ok := strings.Cut(pattern, "/"); ok && a.providerKnown(pre) {
		if provider != "" && provider != pre {
			return "", fmt.Errorf("--provider %s conflicts with the provider in --model %s", provider, pattern)
		}
		provider, pattern = pre, rest
	}
	if provider != "" && !a.providerKnown(provider) {
		return "", fmt.Errorf("unknown provider %q", provider)
	}
	var pool []candidate
	for _, c := range cands {
		if provider == "" || c.Provider == provider {
			pool = append(pool, c)
		}
	}
	for _, c := range pool {
		if c.ID == pattern { // exact id: keep the user's spelling
			return a.checked(c.full())
		}
	}
	if c, err := a.bestMatch(pattern, pool); err == nil {
		return c.full(), nil
	} else if !strings.Contains(err.Error(), "no match") {
		return "", err
	}
	if provider != "" {
		return a.checked(provider + "/" + pattern)
	}
	return a.checked(pattern)
}

// checked returns model when the router can route it, otherwise the router's own explanation.
func (a *App) checked(model string) (string, error) {
	if _, _, err := a.router.Resolve(model); err != nil {
		return "", err
	}
	return model, nil
}

// expandScope turns --models or enabledModels patterns into models, in order and without repeats. A pattern with * ? or [ is a
// case-insensitive glob over "provider/id" and the bare id; any other pattern names an exact id (every provider offering it) or else the
// best fuzzy match. Patterns that match nothing, and ids several providers offer, are reported as warnings.
func (a *App) expandScope(patterns []string) (models, warnings []string) {
	cands := catalogCandidates(a.Agent.Catalog)
	seen := map[string]bool{}
	add := func(c candidate, level string) {
		m := c.full()
		if level != "" {
			m += ":" + level
		}
		if !seen[m] {
			seen[m] = true
			models = append(models, m)
		}
	}
	for _, p := range patterns {
		p, level := llm.SplitThinkingSuffix(strings.TrimSpace(p)) // "sonnet:high" matches "sonnet" and keeps the level
		if p == "" {
			continue
		}
		var hits []candidate
		switch {
		case strings.ContainsAny(p, "*?["):
			lp := strings.ToLower(p)
			for _, c := range cands {
				ok1, _ := path.Match(lp, strings.ToLower(c.full()))
				ok2, _ := path.Match(lp, strings.ToLower(c.ID))
				if ok1 || ok2 {
					hits = append(hits, c)
				}
			}
		default:
			bare := p
			if pre, rest, ok := strings.Cut(p, "/"); ok && a.providerKnown(pre) {
				bare = rest
				for _, c := range cands {
					if c.Provider == pre && strings.EqualFold(c.ID, bare) {
						hits = append(hits, c)
					}
				}
				if len(hits) == 0 {
					if m, err := a.resolveModel(p, ""); err == nil {
						hits = append(hits, candidate{Provider: pre, ID: strings.TrimPrefix(m, pre+"/")})
					}
				}
				break
			}
			for _, c := range cands {
				if strings.EqualFold(c.ID, bare) {
					hits = append(hits, c)
				}
			}
			if len(hits) > 1 {
				warnings = append(warnings, fmt.Sprintf("model %q is offered by %d providers; all were added", p, len(hits)))
			}
			if len(hits) == 0 {
				if c, err := a.bestMatch(bare, cands); err == nil {
					hits = append(hits, c)
				} else if m, rerr := a.resolveModel(p, ""); rerr == nil {
					hits = append(hits, candidate{ID: m})
				}
			}
		}
		if len(hits) == 0 {
			warnings = append(warnings, fmt.Sprintf("models: nothing matches %q", p))
		}
		for _, c := range hits {
			add(c, level)
		}
	}
	return models, warnings
}

// ListModels renders the catalog for --list-models, optionally narrowed to the models whose provider, id or name contains every word of
// search (ignoring case and punctuation).
func ListModels(home, search string) string {
	cat := loadCatalog(home)
	cands := catalogCandidates(cat)
	words := strings.Fields(search)
	var rows [][]string
	for _, c := range cands {
		hay := norm(c.Provider + " " + c.ID + " " + c.Name)
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, norm(w)) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		ctx, out := "", ""
		if m, found := cat.Lookup(c.ID); found {
			if m.ContextWindow > 0 {
				ctx = tokens(m.ContextWindow)
			}
			if m.MaxOutput > 0 {
				out = tokens(m.MaxOutput)
			}
		}
		rows = append(rows, []string{c.Provider, c.ID, ctx, out, c.Name})
	}
	if len(rows) == 0 {
		return "no models match"
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i][0]+"/"+rows[i][1] < rows[j][0]+"/"+rows[j][1] })
	rows = append([][]string{{"provider", "model", "context", "max-out", "name"}}, rows...)
	w := make([]int, 5)
	for _, r := range rows {
		for i, c := range r {
			w[i] = max(w[i], len(c))
		}
	}
	var b strings.Builder
	for _, r := range rows {
		for i, c := range r {
			if i == 4 {
				b.WriteString(c)
			} else {
				b.WriteString(c + strings.Repeat(" ", w[i]-len(c)+2))
			}
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// tokens abbreviates a token count: 200000 is "200K", 1000000 is "1M".
func tokens(n int) string {
	switch {
	case n >= 1_000_000 && n%1_000_000 == 0:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1000 && n%1000 == 0:
		return fmt.Sprintf("%dK", n/1000)
	}
	return fmt.Sprint(n)
}

// getenv is the router's view of the environment: the process's, except that --api-key stands in for the key of the provider it was given
// for.
func (a *App) getenv(k string) string {
	if a.apiKey != "" && k == a.apiKeyEnv {
		return a.apiKey
	}
	return os.Getenv(k)
}

// keyEnvOf finds the environment variable that holds the API key of model's provider. The router asks for the key last, so a probe that
// records what is asked tells us without a second table of provider names.
func (a *App) keyEnvOf(model string) string {
	asked := ""
	probe := &llm.Router{Custom: a.Cfg.Providers, Getenv: func(k string) string { asked = k; return "x" }}
	if _, _, err := probe.Resolve(model); err != nil {
		return ""
	}
	return asked
}
