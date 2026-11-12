package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// CostConfig is a model's price in models.json, USD per million tokens. Pointers tell "not set" from 0, which matters for modelOverrides,
// where the fields merge with the model's existing price one by one.
type CostConfig struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cacheRead,omitempty"`
	CacheWrite *float64 `json:"cacheWrite,omitempty"`
}

// ModelConfig is one entry of a provider's `models` (or `modelOverrides`) in models.json.
type ModelConfig struct {
	ID             string                     `json:"id,omitempty"`
	Name           string                     `json:"name,omitempty"`
	API            string                     `json:"api,omitempty"`
	BaseURL        string                     `json:"baseUrl,omitempty"`
	Reasoning      *Reasoning                 `json:"reasoning,omitempty"`
	Input          []string                   `json:"input,omitempty"`
	ContextWindow  *int                       `json:"contextWindow,omitempty"`
	MaxTokens      *int                       `json:"maxTokens,omitempty"`
	Cost           *CostConfig                `json:"cost,omitempty"`
	Headers        map[string]string          `json:"headers,omitempty"`
	Compat         map[string]json.RawMessage `json:"compat,omitempty"`
	SamplingParams map[string]any             `json:"samplingParams,omitempty"`
}

// Pi's defaults for a model that models.json declares without them.
const (
	DefaultContextWindow = 128000
	DefaultMaxTokens     = 16384
)

var (
	providerKeysActed = []string{"$schema", "name", "api", "baseUrl", "apiKey", "headers", "authHeader", "compat", "models", "modelOverrides",
		"type", "apiKeyEnv"}
	modelKeysActed = []string{"id", "name", "api", "baseUrl", "reasoning", "input", "contextWindow", "maxTokens", "cost", "headers", "compat",
		"samplingParams"}
)

// ReadModelsFile reads a models.json (Pi's schema). A missing file is empty, not an error. The warnings say what in the file Koine does
// not act on or cannot use; they never stop the rest of the file from working.
func ReadModelsFile(path string) (map[string]ProviderConfig, []string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Providers map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(stripJSONC(b), &doc); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	var warns []string
	out := map[string]ProviderConfig{}
	names := make([]string, 0, len(doc.Providers))
	for n := range doc.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := doc.Providers[name]
		var pc ProviderConfig
		if err := json.Unmarshal(raw, &pc); err != nil {
			return nil, nil, fmt.Errorf("%s: providers.%s: %w", path, name, err)
		}
		warns = append(warns, checkProvider(path, name, raw, &pc)...)
		out[name] = pc
	}
	return out, warns, nil
}

// stripJSONC removes whole-line // comments, which Pi's editors allow in config files; strings keep their text.
func stripJSONC(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			lines[i] = ""
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func unknownKeys(raw json.RawMessage, acted []string) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return UnusedCompat(m, acted)
}

// checkProvider reports what is wrong with or ignored in one provider entry, and drops model entries without an id.
func checkProvider(path, name string, raw json.RawMessage, pc *ProviderConfig) []string {
	var warns []string
	warn := func(format string, a ...any) { warns = append(warns, path+": "+fmt.Sprintf(format, a...)) }
	for _, k := range unknownKeys(raw, providerKeysActed) {
		warn("providers.%s.%s is not acted on", name, k)
	}
	_, builtin := BuiltinProvider(name)
	if !builtin && pc.BaseURL == "" {
		for _, m := range pc.Models {
			if m.BaseURL == "" {
				warn("providers.%s has no baseUrl, so model %q cannot be reached", name, m.ID)
			}
		}
		if len(pc.Models) == 0 {
			warn("providers.%s has no baseUrl and no models", name)
		}
	}
	if pc.API != "" && !knownAPI(pc.API) {
		warn("providers.%s: api %q is not supported (supported: %s)", name, pc.API, strings.Join(APIs, ", "))
	}
	if pc.API == "" || pc.API == APICompletions {
		for _, k := range UnusedCompat(pc.Compat, openAIFlagKeys) {
			warn("providers.%s.compat.%s is not acted on", name, k)
		}
	} else if len(pc.Compat) > 0 {
		warn("providers.%s.compat is not acted on for api %s", name, pc.API)
	}
	var rawModels struct {
		Models []json.RawMessage `json:"models"`
	}
	_ = json.Unmarshal(raw, &rawModels)
	kept := pc.Models[:0]
	for i, m := range pc.Models {
		if m.ID == "" {
			warn("providers.%s.models[%d] has no id and was skipped", name, i)
			continue
		}
		if i < len(rawModels.Models) {
			for _, k := range unknownKeys(rawModels.Models[i], modelKeysActed) {
				warn("providers.%s.models[%q].%s is not acted on", name, m.ID, k)
			}
		}
		if m.API != "" && !knownAPI(m.API) {
			warn("providers.%s.models[%q]: api %q is not supported", name, m.ID, m.API)
		}
		kept = append(kept, m)
	}
	pc.Models = kept
	return warns
}

func knownAPI(api string) bool {
	for _, a := range APIs {
		if a == api {
			return true
		}
	}
	return false
}

// AddProviders adds the models that providers declare to the catalog, replacing entries with the same id, and applies modelOverrides to
// the entries they name. A model declared without a context window, output limit or inputs gets Pi's defaults (128000, 16384, text only);
// one declared without a cost has no price, so its cost is reported as unknown.
func (c *Catalog) AddProviders(providers map[string]ProviderConfig) {
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		pc := providers[name]
		for _, m := range pc.Models {
			entry := Model{ID: m.ID, Provider: name, Name: m.Name, ContextWindow: DefaultContextWindow, MaxOutput: DefaultMaxTokens,
				Input: []string{"text"}, Reasoning: m.Reasoning}
			if m.ContextWindow != nil {
				entry.ContextWindow = *m.ContextWindow
			}
			if m.MaxTokens != nil {
				entry.MaxOutput = *m.MaxTokens
			}
			if len(m.Input) > 0 {
				entry.Input = m.Input
			}
			if m.Cost != nil {
				entry.Cost = mergeCost(nil, m.Cost)
			}
			c.put(entry)
		}
		for id, ov := range pc.ModelOverrides {
			c.override(id, ov)
		}
	}
}

func (c *Catalog) put(m Model) {
	for i := range c.Models {
		if c.Models[i].ID == m.ID {
			c.Models[i] = m
			return
		}
	}
	c.Models = append(c.Models, m)
}

// override changes the catalog entries with this id; an id the catalog does not know is ignored, as in Pi.
func (c *Catalog) override(id string, ov ModelConfig) {
	for i := range c.Models {
		m := &c.Models[i]
		if m.ID != id {
			continue
		}
		if ov.Name != "" {
			m.Name = ov.Name
		}
		if ov.Reasoning != nil {
			m.Reasoning = ov.Reasoning
		}
		if len(ov.Input) > 0 {
			m.Input = ov.Input
		}
		if ov.ContextWindow != nil {
			m.ContextWindow = *ov.ContextWindow
		}
		if ov.MaxTokens != nil {
			m.MaxOutput = *ov.MaxTokens
		}
		if ov.Cost != nil {
			m.Cost = mergeCost(m.Cost, ov.Cost)
		}
	}
}

// mergeCost lays the fields that are set in over a copy of base.
func mergeCost(base *Price, over *CostConfig) *Price {
	var p Price
	if base != nil {
		p = *base
	}
	set := func(dst *float64, v *float64) {
		if v != nil {
			*dst = *v
		}
	}
	set(&p.Input, over.Input)
	set(&p.Output, over.Output)
	set(&p.CacheRead, over.CacheRead)
	set(&p.CacheWrite, over.CacheWrite)
	return &p
}
