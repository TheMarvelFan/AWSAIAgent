package reasoning

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloud-ai/ai-aws-architect/internal/catalog"
)

// StubLLM is a deterministic stand-in for Bedrock. It keyword-matches the
// user's last message against the catalog and returns a well-formed proposal.
//
// It exists so the entire chat, versioning, diff and revert flow can be built
// and demoed with LLM_PROVIDER=stub: no AWS credentials, no credit burn, and
// repeatable behavior in tests.
//
// It EXTENDS the running config rather than rebuilding it. The real engine does
// this because the system prompt tells it to; the stub has to be told in code,
// and without it "add a serverless api too" would swap out the blocks already
// there instead of adding to them - which makes the stub actively misleading to
// build a UI against.
type StubLLM struct{ catalog *catalog.Catalog }

func NewStubLLM(c *catalog.Catalog) *StubLLM { return &StubLLM{catalog: c} }

func (s *StubLLM) ModelID() string { return "stub" }

func (s *StubLLM) Name() string { return "stub" }

func (s *StubLLM) Invoke(_ context.Context, req Request) (*Response, error) {
	last := ""
	if n := len(req.Messages); n > 0 {
		last = strings.ToLower(req.Messages[n-1].Content)
	}

	cfg := s.baseConfig(req.CurrentConfig)

	present := make(map[string]bool, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		present[b.TemplateID] = true
	}

	// Out-of-scope trigger. The real engine reaches this by reasoning; the stub
	// needs a keyword, otherwise the not-applicable branch can never fire and
	// half the guardrail is untestable without Bedrock.
	outOfScope := containsAny(last, "kubernetes", "k8s", "kafka", "elasticsearch",
		"rds", "postgres", "mysql", "redis", "sagemaker", "blockchain")

	// The fallback applies only to an empty config, and never to an
	// out-of-scope request - bolting a Lambda onto "I need Kubernetes" would
	// disguise the very gap this branch exists to show.
	matched := s.match(last, len(cfg.Blocks) == 0 && !outOfScope)

	var added []string
	// Counted so the reply can tell "you already have this" apart from "I did
	// not understand you". Both previously produced the same sentence, and the
	// second one was a lie the user could not act on.
	alreadyPresent := 0

	for _, tpl := range matched {
		if present[tpl.ID] {
			alreadyPresent++
			continue
		}
		cfg.Blocks = append(cfg.Blocks, catalog.Block{
			// Template id doubles as the block id: unique within a config, and
			// stable across revisions so the diff reads as a change rather than
			// a delete-and-recreate.
			ID:         tpl.ID,
			TemplateID: tpl.ID,
			Purpose:    tpl.Summary,
			Parameters: stubParams(tpl),
		})
		present[tpl.ID] = true
		added = append(added, tpl.Name)
	}

	// Budget extraction, so the tighten/loosen rule is exercisable without a
	// model. Matches "under $20", "max $5", "budget of $100" and similar.
	budgetReq := stubBudgetRequest(last)

	changed := len(added) > 0
	scope := ScopeSupported
	if outOfScope {
		scope = ScopeOutOfScope
		// Recording the gap IS a config change, so a version does get written
		// and the proposal ends up stored-but-not-applicable rather than
		// discarded. That is the whole point of the branch.
		if gap, ok := newGap(cfg.OutOfCatalog, last); ok {
			cfg.OutOfCatalog = append(cfg.OutOfCatalog, gap)
			changed = true
		}
	}

	summary := "Stub revision: recorded an out-of-catalog requirement"
	if len(added) > 0 {
		summary = "Stub revision: added " + strings.Join(added, ", ")
	}

	p := Proposal{
		BudgetRequest: budgetReq,
		ChatTitle:     "Stub proposal",
		ConfigUpdated: changed,
		Scope:         scope,
		// Fixed, not computed. The stub has no model to report a confidence,
		// and a varying number here would look like signal that isn't there.
		Confidence:    0.5,
		ChangeSummary: summary,
		Rationale:     "Deterministic keyword match against the vetted catalog.",
		Config:        cfg,
	}

	const stubNote = " (Stub reasoning engine: set LLM_PROVIDER=bedrock for real proposals.)"
	switch {
	case outOfScope:
		p.Reply = "Nothing in the vetted catalog covers that, so I can show you a proposal but cannot provision it." + stubNote
	case len(added) > 0:
		p.Reply = "Added " + strings.Join(added, " + ") + "." + stubNote
	case alreadyPresent > 0:
		// Genuinely already satisfied: something matched and every match was
		// already in the config.
		p.Reply = "Nothing to add - the configuration already covers that." + stubNote
	default:
		// Nothing matched at all. Claiming the configuration already covers it
		// is false and sends the user looking for a block that is not there.
		// Say what actually happened and what would help.
		p.Reply = "I could not match that to anything in the vetted catalog, so the " +
			"configuration is unchanged. Try naming what you need more plainly - " +
			"storage, a database, an API endpoint, or a spending alert." + stubNote
	}

	buf, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return &Response{ToolInput: buf, Model: "stub", StopReason: "tool_use"}, nil
}

// baseConfig starts from the running config when there is one, so additions
// accumulate across turns.
func (s *StubLLM) baseConfig(current json.RawMessage) *catalog.ArchitectureConfig {
	if len(current) > 0 {
		var cfg catalog.ArchitectureConfig
		if err := json.Unmarshal(current, &cfg); err == nil {
			return &cfg
		}
	}
	return &catalog.ArchitectureConfig{
		SchemaVersion: catalog.CurrentSchemaVersion,
		Name:          "Stub architecture",
		Summary:       "Generated by the stub engine.",
	}
}

func (s *StubLLM) match(text string, allowFallback bool) []catalog.Template {
	var out []catalog.Template
	for _, tpl := range s.catalog.All() {
		if matchesTemplate(text, tpl) {
			out = append(out, tpl)
		}
	}
	if len(out) == 0 && allowFallback {
		if tpl, ok := s.catalog.Get("serverless-api-lambda"); ok {
			out = append(out, tpl)
		}
	}
	// Pull in anything the matched blocks require, or validation will reject
	// the result.
	seen := map[string]bool{}
	for _, t := range out {
		seen[t.ID] = true
	}
	for _, t := range out {
		for _, req := range t.Requires {
			if !seen[req] {
				if dep, ok := s.catalog.Get(req); ok {
					out = append(out, dep)
					seen[req] = true
				}
			}
		}
	}
	return out
}

// matchesTemplate checks the declared keywords and the words in the template
// id. "static site" matches static-site-s3-cloudfront via the id even though no
// `provides` entry contains that exact phrase - the real engine handles this by
// understanding the request; the stub needs the extra surface.
func matchesTemplate(text string, tpl catalog.Template) bool {
	for _, kw := range tpl.Provides {
		if strings.Contains(text, kw) {
			return true
		}
	}
	// Fragments shorter than four characters (s3, api) would match almost
	// anything.
	for _, word := range strings.Split(tpl.ID, "-") {
		if len(word) > 3 && strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func stubParams(t catalog.Template) map[string]any {
	params := map[string]any{}
	for _, p := range t.Params {
		switch {
		case p.Default != nil:
			params[p.Name] = p.Default
		case !p.Required:
			continue
		case p.Type == catalog.ParamString:
			params[p.Name] = stubString(p, t)
		case p.Type == catalog.ParamInt:
			params[p.Name] = 1
		case p.Type == catalog.ParamBool:
			params[p.Name] = false
		}
	}
	return params
}

// stubString invents a value for a required string parameter.
//
// The generic "demo-<something>" is fine for names but fails any parameter with
// a real format - an email address, most obviously. Rather than teaching the
// stub to satisfy arbitrary regexes, it special-cases the shapes the catalog
// actually contains. A value that fails catalog validation would make the stub
// produce rejected proposals, which defeats the point of having one.
func stubString(p catalog.Parameter, t catalog.Template) string {
	if strings.Contains(strings.ToLower(p.Name), "email") {
		return "demo@example.com"
	}
	return "demo-" + strings.Split(t.ID, "-")[0]
}

// stubBudgetRequest pulls a dollar figure out of a sentence that reads like a
// limit. Deliberately narrow: it requires a limit word near the number, so
// "we spent $500 last month" is not read as an instruction - the same mistake
// the real engine is told to avoid.
func stubBudgetRequest(text string) *BudgetRequest {
	if containsAny(text, "no limit", "remove the limit", "remove the budget", "unlimited") {
		return &BudgetRequest{Clear: true, Quote: text}
	}

	m := stubBudgetPattern.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	amount, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return nil
	}
	return &BudgetRequest{AmountUSD: &amount, Quote: strings.TrimSpace(text)}
}

// A limit word, then a dollar figure within a few words of it.
var stubBudgetPattern = regexp.MustCompile(
	`(?i)(?:under|below|max|maximum|limit|budget|ceiling|at most|less than|cap)[^0-9$]{0,12}\$?\s*([0-9]+(?:\.[0-9]{1,2})?)`)

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// newGap returns a gap note unless an identical one is already recorded.
// Without the guard, repeating the same out-of-scope request would append a
// duplicate every turn and manufacture a new version each time.
func newGap(existing []catalog.OutOfCatalog, need string) (catalog.OutOfCatalog, bool) {
	need = strings.TrimSpace(need)
	for _, g := range existing {
		if g.Need == need {
			return catalog.OutOfCatalog{}, false
		}
	}
	return catalog.OutOfCatalog{
		Need:   need,
		Reason: "no vetted block covers this, so it cannot be provisioned from here",
	}, true
}
