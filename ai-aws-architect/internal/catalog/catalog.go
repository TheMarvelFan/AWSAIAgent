// Package catalog holds the pre-vetted infrastructure building blocks.
//
// The catalog is the project's central guardrail: the model never authors
// infrastructure code, it only selects blocks from here and fills in parameters
// that this package then re-checks. Everything the model returns is treated as
// untrusted input.
package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed templates/*.json
var templateFS embed.FS

type ParamType string

const (
	ParamString ParamType = "string"
	ParamInt    ParamType = "int"
	ParamBool   ParamType = "bool"
	ParamEnum   ParamType = "enum"
)

type Parameter struct {
	Name        string    `json:"name"`
	Type        ParamType `json:"type"`
	Required    bool      `json:"required"`
	Default     any       `json:"default,omitempty"`
	Allowed     []any     `json:"allowed,omitempty"`
	Min         *float64  `json:"min,omitempty"`
	Max         *float64  `json:"max,omitempty"`
	Pattern     string    `json:"pattern,omitempty"`
	Description string    `json:"description,omitempty"`
}

type CostRange struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

type Template struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Category  string      `json:"category"`
	Summary   string      `json:"summary"`
	Provides  []string    `json:"provides,omitempty"`
	Requires  []string    `json:"requires,omitempty"`
	Resources []string    `json:"resources"`
	Params    []Parameter `json:"parameters"`
	Cost      CostRange   `json:"estimated_cost_usd_per_month"`
	Notes     string      `json:"notes,omitempty"`
}

func (t Template) Param(name string) (Parameter, bool) {
	for _, p := range t.Params {
		if p.Name == name {
			return p, true
		}
	}
	return Parameter{}, false
}

type Catalog struct {
	templates map[string]Template
	ordered   []Template
}

// Load reads and validates the embedded templates at startup. A malformed
// template is a boot failure, not a runtime surprise mid-demo.
func Load() (*Catalog, error) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}
	c := &Catalog{templates: make(map[string]Template, len(entries))}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := templateFS.ReadFile("templates/" + e.Name())
		if err != nil {
			return nil, err
		}
		var t Template
		dec := json.NewDecoder(strings.NewReader(string(body)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&t); err != nil {
			return nil, fmt.Errorf("template %s: %w", e.Name(), err)
		}
		if t.ID == "" {
			return nil, fmt.Errorf("template %s: missing id", e.Name())
		}
		if _, dup := c.templates[t.ID]; dup {
			return nil, fmt.Errorf("duplicate template id %q", t.ID)
		}
		c.templates[t.ID] = t
	}
	if len(c.templates) == 0 {
		return nil, fmt.Errorf("catalog is empty")
	}
	for _, t := range c.templates {
		for _, req := range t.Requires {
			if _, ok := c.templates[req]; !ok {
				return nil, fmt.Errorf("template %q requires unknown template %q", t.ID, req)
			}
		}
		c.ordered = append(c.ordered, t)
	}
	sort.Slice(c.ordered, func(i, j int) bool { return c.ordered[i].ID < c.ordered[j].ID })
	return c, nil
}

func (c *Catalog) All() []Template { return c.ordered }

func (c *Catalog) Get(id string) (Template, bool) {
	t, ok := c.templates[id]
	return t, ok
}

func (c *Catalog) IDs() []string {
	ids := make([]string, 0, len(c.ordered))
	for _, t := range c.ordered {
		ids = append(ids, t.ID)
	}
	return ids
}

// PromptJSON is the catalog as fed to the model. Small enough to sit in the
// system prompt, which is why there is no vector store anywhere in this design.
func (c *Catalog) PromptJSON() (string, error) {
	buf, err := json.MarshalIndent(c.ordered, "", "  ")
	if err != nil {
		return "", err
	}
	return string(buf), nil
}
