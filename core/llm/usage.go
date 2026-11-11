package llm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Usage is the tokens one model response used. Input excludes cached tokens, which are counted separately, for every provider.
type Usage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

// Total is every token the call involved.
func (u Usage) Total() int { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

// Prompt is the tokens that were sent to the model, cached or not.
func (u Usage) Prompt() int { return u.Input + u.CacheRead + u.CacheWrite }

// Add accumulates o into u.
func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
}

// Price is dollars per million tokens.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// LongContext raises every rate by Multiplier when a prompt is longer than Threshold tokens.
type LongContext struct {
	Threshold  int     `json:"threshold"`
	Multiplier float64 `json:"multiplier"`
}

// Model is what Koine knows about a model. Zero fields mean "not known".
type Model struct {
	ID            string       `json:"id"`
	Provider      string       `json:"provider,omitempty"`
	Name          string       `json:"name,omitempty"`
	ContextWindow int          `json:"contextWindow,omitempty"`
	MaxOutput     int          `json:"maxOutput,omitempty"`
	Cost          *Price       `json:"cost,omitempty"`
	LongContext   *LongContext `json:"longContext,omitempty"`
	Reasoning     *Reasoning   `json:"reasoning,omitempty"` // present when the model thinks (see Reasoning)
}

// Cost is a price breakdown in dollars.
type Cost struct {
	Input, Output, CacheRead, CacheWrite, Total float64
}

// Of prices u with m's rates. ok is false when m has no price.
func (m Model) Of(u Usage) (c Cost, ok bool) {
	if m.Cost == nil {
		return c, false
	}
	k := 1.0
	if lc := m.LongContext; lc != nil && lc.Threshold > 0 && u.Prompt() > lc.Threshold {
		k = lc.Multiplier
	}
	per := func(tokens int, rate float64) float64 { return float64(tokens) * rate * k / 1e6 }
	c.Input, c.Output = per(u.Input, m.Cost.Input), per(u.Output, m.Cost.Output)
	c.CacheRead, c.CacheWrite = per(u.CacheRead, m.Cost.CacheRead), per(u.CacheWrite, m.Cost.CacheWrite)
	c.Total = c.Input + c.Output + c.CacheRead + c.CacheWrite
	return c, true
}

//go:embed models.json
var modelsRaw []byte

// Catalog is the set of known models.
type Catalog struct {
	Models []Model `json:"models"`
}

// DefaultCatalog is the catalog built into Koine.
func DefaultCatalog() *Catalog {
	var c Catalog
	_ = json.Unmarshal(modelsRaw, &c) // the embedded file is checked by TestEmbeddedCatalogIsValid
	return &c
}

// Merge adds the models of a catalog file. Entries with the same id replace the built-in ones. A missing file is not an error.
func (c *Catalog) Merge(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var extra Catalog
	if err := json.Unmarshal(b, &extra); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, m := range extra.Models {
		if m.ID == "" {
			return fmt.Errorf("%s: a model needs an id", path)
		}
		replaced := false
		for i := range c.Models {
			if c.Models[i].ID == m.ID {
				c.Models[i], replaced = m, true
			}
		}
		if !replaced {
			c.Models = append(c.Models, m)
		}
	}
	return nil
}

// Lookup finds a model by id, accepting a "provider/" prefix and a dated suffix ("claude-sonnet-5-5-20261001").
func (c *Catalog) Lookup(id string) (Model, bool) {
	if c == nil {
		return Model{}, false
	}
	if i := strings.LastIndex(id, "/"); i >= 0 && !c.has(id) {
		id = id[i+1:]
	}
	best := -1
	for i, m := range c.Models {
		if m.ID == id {
			return m, true
		}
		if strings.HasPrefix(id, m.ID+"-") && isDate(id[len(m.ID)+1:]) && (best < 0 || len(m.ID) > len(c.Models[best].ID)) {
			best = i
		}
	}
	if best >= 0 {
		return c.Models[best], true
	}
	return Model{}, false
}

func (c *Catalog) has(id string) bool {
	for _, m := range c.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func isDate(s string) bool {
	if len(s) < 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
