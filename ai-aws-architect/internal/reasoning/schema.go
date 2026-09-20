package reasoning

import "github.com/cloud-ai/ai-aws-architect/internal/catalog"

// Proposal is what the model returns through the forced tool call.
type Proposal struct {
	// Reply is the chat message: what changed and why. The UI renders this.
	Reply string `json:"reply"`
	// ChatTitle is used only to name a brand-new chat.
	ChatTitle string `json:"chat_title"`
	// ConfigUpdated distinguishes "I changed the architecture" from "I answered
	// a question". Only the former appends a version.
	ConfigUpdated bool `json:"config_updated"`
	// Scope is the model's own read on whether it can serve the request at all.
	// Distinct from validation, which only answers "is this output legal".
	// Validation cannot tell you whether the model understood the question.
	Scope Scope `json:"scope"`
	// Confidence is SELF-REPORTED and uncalibrated: 0.9 does not mean 90% of
	// these are right. Surfaced to the user as a hint, never used as a
	// threshold in code - a cutoff on an uncalibrated number is false
	// precision.
	Confidence    float64                     `json:"confidence"`
	ChangeSummary string                      `json:"change_summary"`
	Rationale     string                      `json:"rationale"`
	Decisions     []Decision                  `json:"decisions"`
	Config        *catalog.ArchitectureConfig `json:"config"`
	// BudgetRequest is a spending ceiling the user stated in this turn. Nil on
	// most turns. The model only reports what it read; the service decides
	// whether it takes effect, because tightening applies immediately and
	// loosening needs a human.
	BudgetRequest *BudgetRequest `json:"budget_request"`
}

// Decision is the model's own account of a choice it made, kept alongside the
// mechanical diff. The diff says what changed; this says why.
type Decision struct {
	Field    string `json:"field"`
	Change   string `json:"change"`
	Why      string `json:"why"`
	Tradeoff string `json:"tradeoff,omitempty"`
}

// Scope classifies whether the request can be served, in three states rather
// than a number. A float invites a threshold nobody can justify.
type Scope string

const (
	// ScopeSupported understood, and the catalog covers it.
	ScopeSupported Scope = "supported"
	// ScopeNeedsClarification buildable, but an answer would change the shape.
	// Still applicable - the human approval gate is the check, and blocking
	// apply here would be doing the human's job for them.
	ScopeNeedsClarification Scope = "needs_clarification"
	// ScopeOutOfScope the catalog cannot provision this. The proposal is still
	// shown and explained; Apply is disabled.
	ScopeOutOfScope Scope = "out_of_scope"
)

// BudgetRequest is a spending ceiling extracted from what the user said.
type BudgetRequest struct {
	// AmountUSD is the monthly ceiling asked for. Nil with Clear set means the
	// user asked to remove the limit entirely.
	AmountUSD *float64 `json:"amount_usd"`
	Clear     bool     `json:"clear"`
	// Quote is the user's own words. A confirmation that shows only a number,
	// gives the user no way to tell a correct reading from a misheard one -
	// "$500 last month" becoming a $500 ceiling looks identical to a deliberate
	// instruction unless the phrase is shown alongside it.
	Quote string `json:"quote"`
}

const toolName = "emit_architecture_update"

// proposalToolSchema is the JSON Schema for the forced tool. Constraints here
// are a first filter only; catalog.Validate is the authority.
func proposalToolSchema(c *catalog.Catalog) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reply": map[string]any{
				"type":        "string",
				"description": "The chat reply. Summarise what changed in the architecture and why. Plain language, no code fences. If nothing changed, answer the question directly.",
			},
			"chat_title": map[string]any{
				"type":        "string",
				"description": "Four to six word title for this conversation. Only used for the first turn.",
			},
			"config_updated": map[string]any{
				"type":        "boolean",
				"description": "True only if the architecture itself changed. False for clarifying questions or explanations.",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{string(ScopeSupported), string(ScopeNeedsClarification), string(ScopeOutOfScope)},
				"description": "supported: the catalog covers this. needs_clarification: buildable, but one answer would change the design. out_of_scope: the catalog cannot provision this - still propose the architecture and record the gaps in out_of_catalog.",
			},
			"confidence": map[string]any{
				"type":        "number",
				"minimum":     0,
				"maximum":     1,
				"description": "Your own confidence that this proposal matches what the user actually wants. Be honest; a low number shows the user a caution, it does not discard your work.",
			},
			"change_summary": map[string]any{
				"type":        "string",
				"description": "One line describing this revision, shown in the version history list.",
			},
			"rationale": map[string]any{
				"type":        "string",
				"description": "Why this shape of architecture, including what was ruled out.",
			},
			"decisions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"field":    map[string]any{"type": "string"},
						"change":   map[string]any{"type": "string"},
						"why":      map[string]any{"type": "string"},
						"tradeoff": map[string]any{"type": "string"},
					},
					"required": []string{"field", "change", "why"},
				},
			},
			"budget_request": map[string]any{
				"type": "object",
				"description": "Set ONLY when the user states a spending limit for this project in their message. " +
					"Report what they said; do not invent a figure, and do not treat a mention of past spending " +
					"(\"we spent $500 last month\") as a limit. Leave this out entirely when no limit was stated. " +
					"This is the chat's ceiling, which is a different thing from the cost-budget block in the catalog.",
				"properties": map[string]any{
					"amount_usd": map[string]any{
						"type":        "number",
						"minimum":     0,
						"description": "The monthly ceiling in USD.",
					},
					"clear": map[string]any{
						"type":        "boolean",
						"description": "True when the user asked to remove the limit entirely.",
					},
					"quote": map[string]any{
						"type":        "string",
						"description": "The user's own words that you read as stating a limit.",
					},
				},
			},
			"config": map[string]any{
				"type":        "object",
				"description": "The COMPLETE architecture config after this turn, not a patch. Omit when config_updated is false.",
				"properties": map[string]any{
					"schema_version": map[string]any{"type": "integer", "enum": []any{catalog.CurrentSchemaVersion}},
					"name":           map[string]any{"type": "string"},
					"summary":        map[string]any{"type": "string"},
					"blocks": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":          map[string]any{"type": "string", "description": "Stable identifier for this block within the config. Keep it unchanged across revisions."},
								"template_id": map[string]any{"type": "string", "enum": c.IDs()},
								"purpose":     map[string]any{"type": "string"},
								"parameters":  map[string]any{"type": "object"},
								"depends_on":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							},
							"required": []string{"id", "template_id", "parameters"},
						},
					},
					"out_of_catalog": map[string]any{
						"type":        "array",
						"description": "Requirements that no vetted block covers. Record them here instead of inventing a block.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"need":                  map[string]any{"type": "string"},
								"reason":                map[string]any{"type": "string"},
								"suggested_manual_step": map[string]any{"type": "string"},
							},
							"required": []string{"need", "reason"},
						},
					},
					"open_questions": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"required": []string{"name", "blocks"},
			},
		},
		"required": []string{"reply", "config_updated", "scope", "confidence"},
	}
}
