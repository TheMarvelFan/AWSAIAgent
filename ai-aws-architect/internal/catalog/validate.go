package catalog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ArchitectureConfig is the document stored as a config version. It is the
// contract between the model, the database and (later) the Terraform runner.
type ArchitectureConfig struct {
	SchemaVersion int            `json:"schema_version"`
	Name          string         `json:"name"`
	Summary       string         `json:"summary"`
	Region        string         `json:"region"`
	Blocks        []Block        `json:"blocks"`
	OutOfCatalog  []OutOfCatalog `json:"out_of_catalog"`
	OpenQuestions []string       `json:"open_questions"`
	EstimatedCost *CostEstimate  `json:"estimated_cost"`
}

type Block struct {
	ID         string         `json:"id"`
	TemplateID string         `json:"template_id"`
	Purpose    string         `json:"purpose"`
	Parameters map[string]any `json:"parameters"`
	DependsOn  []string       `json:"depends_on"`
}

// OutOfCatalog records a requirement the vetted blocks cannot satisfy. It is
// carried in the document on purpose: the UI surfaces it, and it is the thing
// that gets narrated when a request falls outside what can be provisioned.
type OutOfCatalog struct {
	Need      string `json:"need"`
	Reason    string `json:"reason"`
	Suggested string `json:"suggested_manual_step"`
}

type CostEstimate struct {
	Currency    string  `json:"currency"`
	MonthlyLow  float64 `json:"monthly_low"`
	MonthlyHigh float64 `json:"monthly_high"`
	Basis       string  `json:"basis"`
}

// Issue is a single validation finding, addressed by config path so the UI can
// point at the offending field.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Verdict is the three-way outcome of validating a proposal.
//
// The two-way version (valid / invalid) collapsed two situations that need
// opposite handling: the model hallucinating a template id is a fault and the
// output should be discarded, while a user asking for something the catalog
// does not stock is expected behavior and the proposal is still worth showing.
type Verdict string

const (
	// VerdictOK legal and provision-able. Apply is available.
	VerdictOK Verdict = "ok"
	// VerdictNotApplicable a coherent proposal that cannot be provisioned as
	// things stand - nothing in the catalog covers the request, or it exceeds
	// the stated budget. Stored and displayed with Apply disabled.
	VerdictNotApplicable Verdict = "not_applicable"
	// VerdictRejected the model produced something invalid. Discarded; the
	// running config is untouched.
	VerdictRejected Verdict = "rejected"
)

type ValidationResult struct {
	Verdict Verdict `json:"verdict"`
	// Errors are model faults: an invented template id, an out-of-range
	// parameter, an unsatisfied dependency.
	Errors []Issue `json:"errors"`
	// Blockers are legitimate reasons this cannot be provisioned. The proposal
	// is still coherent and still shown.
	Blockers []Issue `json:"blockers"`
}

func (r ValidationResult) Applicable() bool { return r.Verdict == VerdictOK }

// ValidateOptions carries the things the server decides, never the model:
// which region, and what the user said they are willing to spend.
type ValidateOptions struct {
	Region string
	// MonthlyBudgetUSD is nil when the user has not stated one.
	MonthlyBudgetUSD *float64
	// ScopeOutOfCatalog is set when the model itself reported that the request
	// falls outside what the catalog can build.
	ScopeOutOfCatalog bool
}

const CurrentSchemaVersion = 1

// Validate re-checks the model's output against the catalog and normalizes it:
// defaults are filled in, unknown parameters are rejected, numeric ranges and
// enums are enforced, and the cost estimate is recomputed from the templates
// rather than trusted from the model.
//
// Region is stamped from the caller's AWS connection rather than validated
// from model output. The target account is resolved from the authenticated
// session and never from anything the model produced; region has to follow the
// same rule or the principle is only half applied.
//
// This runs on every proposed config. A model that hallucinates a template ID
// or a cpu value of 16384 produces a validation failure, not a config version.
func (c *Catalog) Validate(cfg *ArchitectureConfig, opts ValidateOptions) ValidationResult {
	res := ValidationResult{Verdict: VerdictOK, Errors: []Issue{}, Blockers: []Issue{}}
	add := func(path, msg string) {
		res.Errors = append(res.Errors, Issue{Path: path, Message: msg})
	}
	block := func(path, msg string) {
		res.Blockers = append(res.Blockers, Issue{Path: path, Message: msg})
	}

	if cfg == nil {
		return ValidationResult{
			Verdict: VerdictRejected,
			Errors:  []Issue{{Path: "/", Message: "config is empty"}},
		}
	}
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = CurrentSchemaVersion
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		add("/schema_version", fmt.Sprintf("unsupported schema version %d", cfg.SchemaVersion))
	}
	// Overwritten unconditionally: whatever the model put here is discarded.
	cfg.Region = opts.Region

	// Zero blocks is only a fault when the model also failed to say why. If it
	// recorded what the catalog cannot cover, this is the graceful fallback the
	// spec asks for: a proposal that is shown and explained, not applied.
	if len(cfg.Blocks) == 0 {
		if len(cfg.OutOfCatalog) > 0 {
			block("/blocks", "nothing in the vetted catalog covers this request, so there is nothing to provision")
		} else {
			add("/blocks", "at least one block is required")
		}
	}
	if len(cfg.Blocks) > 6 {
		add("/blocks", "a proposal is limited to 6 blocks")
	}

	seenIDs := map[string]bool{}
	present := map[string]bool{}
	var low, high float64

	for i, b := range cfg.Blocks {
		base := fmt.Sprintf("/blocks/%d", i)
		if b.ID == "" {
			add(base+"/id", "block id is required")
		} else if seenIDs[b.ID] {
			add(base+"/id", fmt.Sprintf("duplicate block id %q", b.ID))
		}
		seenIDs[b.ID] = true

		tpl, ok := c.Get(b.TemplateID)
		if !ok {
			add(base+"/template_id", fmt.Sprintf(
				"%q is not in the vetted catalog (available: %s)", b.TemplateID, strings.Join(c.IDs(), ", ")))
			continue
		}
		present[tpl.ID] = true
		low += tpl.Cost.Low
		high += tpl.Cost.High

		if b.Parameters == nil {
			cfg.Blocks[i].Parameters = map[string]any{}
			b.Parameters = cfg.Blocks[i].Parameters
		}
		for name := range b.Parameters {
			if _, known := tpl.Param(name); !known {
				add(base+"/parameters/"+name, fmt.Sprintf("unknown parameter for template %q", tpl.ID))
			}
		}
		for _, p := range tpl.Params {
			val, provided := b.Parameters[p.Name]
			if !provided {
				if p.Required {
					add(base+"/parameters/"+p.Name, "required parameter is missing")
				} else if p.Default != nil {
					b.Parameters[p.Name] = p.Default
				}
				continue
			}
			if msg := checkParam(p, val); msg != "" {
				add(base+"/parameters/"+p.Name, msg)
			}
		}
	}

	for _, b := range cfg.Blocks {
		tpl, ok := c.Get(b.TemplateID)
		if !ok {
			continue
		}
		for _, req := range tpl.Requires {
			if !present[req] {
				add("/blocks", fmt.Sprintf("template %q requires %q, which is not in the proposal", tpl.ID, req))
			}
		}
		for _, dep := range b.DependsOn {
			if !seenIDs[dep] {
				add("/blocks", fmt.Sprintf("block %q depends on unknown block %q", b.ID, dep))
			}
		}
	}

	// Cost is derived, never taken from the model.
	cfg.EstimatedCost = &CostEstimate{
		Currency:    "USD",
		MonthlyLow:  round2(low),
		MonthlyHigh: round2(high),
		Basis:       "sum of static per-template estimates from the vetted catalog",
	}

	// Budget is enforced here rather than trusted to the prompt. The model can
	// be TOLD to stay under a number and will mostly comply; "mostly" is not a
	// guarantee, and the cost figure is already computed on this side.
	//
	// Compared against the high end deliberately: a range that straddles the
	// ceiling is over budget, because the user should not discover the top of
	// the range on their bill.
	if opts.MonthlyBudgetUSD != nil && len(cfg.Blocks) > 0 {
		if cfg.EstimatedCost.MonthlyHigh > *opts.MonthlyBudgetUSD {
			block("/estimated_cost", fmt.Sprintf(
				"estimated at up to $%.2f/month, above the stated budget of $%.2f/month",
				cfg.EstimatedCost.MonthlyHigh, *opts.MonthlyBudgetUSD))
		}
	}

	if opts.ScopeOutOfCatalog {
		block("/", "the request falls outside what the vetted catalog can provision")
	}

	// Normalize every collection to non-nil before the document is stored.
	//
	// Dropping `omitempty` is not enough on its own: a nil slice still marshals
	// to `null`, so a client still has to treat absent, null and empty as three
	// states. Every stored document passes through here, which makes this the
	// one place the guarantee can actually be made.
	if cfg.Blocks == nil {
		cfg.Blocks = []Block{}
	}
	if cfg.OutOfCatalog == nil {
		cfg.OutOfCatalog = []OutOfCatalog{}
	}
	if cfg.OpenQuestions == nil {
		cfg.OpenQuestions = []string{}
	}
	for i := range cfg.Blocks {
		if cfg.Blocks[i].Parameters == nil {
			cfg.Blocks[i].Parameters = map[string]any{}
		}
		if cfg.Blocks[i].DependsOn == nil {
			cfg.Blocks[i].DependsOn = []string{}
		}
	}

	switch {
	case len(res.Errors) > 0:
		res.Verdict = VerdictRejected
	case len(res.Blockers) > 0:
		res.Verdict = VerdictNotApplicable
	default:
		res.Verdict = VerdictOK
	}
	return res
}

func checkParam(p Parameter, val any) string {
	switch p.Type {
	case ParamString:
		s, ok := val.(string)
		if !ok {
			return "expected a string"
		}
		if p.Pattern != "" {
			re, err := regexp.Compile(p.Pattern)
			if err != nil {
				return "template pattern is invalid"
			}
			if !re.MatchString(s) {
				return fmt.Sprintf("must match %s", p.Pattern)
			}
		}
	case ParamBool:
		if _, ok := val.(bool); !ok {
			return "expected true or false"
		}
	case ParamInt:
		f, ok := toFloat(val)
		if !ok {
			return "expected a number"
		}
		if f != float64(int64(f)) {
			return "expected a whole number"
		}
		if p.Min != nil && f < *p.Min {
			return fmt.Sprintf("must be >= %g", *p.Min)
		}
		if p.Max != nil && f > *p.Max {
			return fmt.Sprintf("must be <= %g", *p.Max)
		}
	case ParamEnum:
		for _, allowed := range p.Allowed {
			if equalJSONValue(allowed, val) {
				return ""
			}
		}
		return fmt.Sprintf("must be one of %v", p.Allowed)
	default:
		return fmt.Sprintf("unsupported parameter type %q", p.Type)
	}
	return ""
}

// equalJSONValue compares values that have been through JSON, where every
// number is a float64 regardless of how it was written.
func equalJSONValue(a, b any) bool {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
