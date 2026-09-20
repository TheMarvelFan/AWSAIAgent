package reasoning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/catalog"
	"github.com/cloud-ai/ai-aws-architect/internal/configdiff"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

// ChatRepository is the slice of the store this service needs. Declaring it
// here keeps the dependency pointing inward and makes the service testable
// with a fake.
type ChatRepository interface {
	GetChat(ctx context.Context, userID, chatID uuid.UUID) (*domain.Chat, error)
	RecentMessages(ctx context.Context, chatID uuid.UUID, n int) ([]domain.Message, error)
	CurrentConfig(ctx context.Context, chatID uuid.UUID) (*domain.ConfigVersion, error)
	AppendTurn(ctx context.Context, p store.AppendTurnParams) (*store.TurnResult, error)
	ApplyManualEdit(ctx context.Context, p store.ManualEditParams) (*domain.ConfigVersion, *domain.Message, error)
}

// RegionResolver reports which AWS region a user's proposals target. Backed by
// their AWS connection; users who have not connected still get proposals, in
// the configured default region.
type RegionResolver interface {
	RegionForUser(ctx context.Context, userID uuid.UUID) (string, error)
}

type Options struct {
	MaxHistory int
	Timeout    time.Duration
}

type Service struct {
	repo    ChatRepository
	catalog *catalog.Catalog
	llm     LLM
	regions RegionResolver
	opts    Options
}

func NewService(repo ChatRepository, cat *catalog.Catalog, llm LLM, regions RegionResolver, opts Options) *Service {
	if opts.MaxHistory <= 0 {
		opts.MaxHistory = 20
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 90 * time.Second
	}
	return &Service{repo: repo, catalog: cat, llm: llm, regions: regions, opts: opts}
}

// SendResult is what the handler returns to the client: the two new messages,
// the new config version if one was created, and the diff that produced it.
type SendResult struct {
	Chat             domain.Chat           `json:"chat"`
	UserMessage      domain.Message        `json:"user_message"`
	AssistantMessage domain.Message        `json:"assistant_message"`
	ConfigVersion    *domain.ConfigVersion `json:"config_version"`
	Diff             *configdiff.Result    `json:"diff"`
	// BudgetChange is present when the turn mentioned a spending limit. Null
	// otherwise - most turns do not.
	BudgetChange *BudgetChange `json:"budget_change"`
}

// SendMessage runs one full turn: load context, call the model, validate what
// comes back against the catalog, diff it against the running config, and
// persist the whole thing atomically.
func (s *Service) SendMessage(ctx context.Context, userID, chatID uuid.UUID, text string) (*SendResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, domain.NewValidationError("content", "must not be empty")
	}

	chat, err := s.repo.GetChat(ctx, userID, chatID)
	if err != nil {
		return nil, err
	}
	history, err := s.repo.RecentMessages(ctx, chat.ID, s.opts.MaxHistory)
	if err != nil {
		return nil, err
	}
	current, err := s.repo.CurrentConfig(ctx, chat.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	system, err := buildSystemPrompt(s.catalog, current, chat.MonthlyBudgetUSD)
	if err != nil {
		return nil, err
	}

	llmCtx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()

	var currentDoc json.RawMessage
	if current != nil {
		currentDoc = current.Document
	}

	resp, err := s.llm.Invoke(llmCtx, Request{
		System:        system,
		Messages:      toTurns(history, text),
		CurrentConfig: currentDoc,
		Tool: ToolSpec{
			Name:        toolName,
			Description: "Return the chat reply and, when the architecture changed, the complete updated configuration.",
			InputSchema: proposalToolSchema(s.catalog),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrUpstream, err)
	}

	proposal, err := parseProposal(resp)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrUpstream, err)
	}

	params := store.AppendTurnParams{
		ChatID:           chat.ID,
		UserID:           userID,
		UserContent:      text,
		AssistantContent: strings.TrimSpace(proposal.Reply),
		Model:            resp.Model,
		InputTokens:      resp.InputTokens,
		OutputTokens:     resp.OutputTokens,
		TitleSuggestion:  firstNonEmpty(proposal.ChatTitle, text),
	}
	if current != nil {
		base := current.Version
		params.ExpectedBaseVersion = &base
	}

	var diff *configdiff.Result

	// Resolved before validation on purpose. "Keep it under $20" has to bind
	// the very proposal it accompanies; validating against the old ceiling
	// would let the turn that sets a limit be the one turn exempt from it.
	budgetChange := resolveBudgetChange(chat.MonthlyBudgetUSD, proposal.BudgetRequest)
	effectiveBudget := chat.MonthlyBudgetUSD
	if budgetChange != nil && budgetChange.Applied {
		effectiveBudget = budgetChange.ToUSD
		params.SetBudget = true
		params.NewBudgetUSD = budgetChange.ToUSD
	}
	if budgetChange != nil {
		params.AssistantContent = appendBudgetNote(params.AssistantContent, budgetChange)
	}

	if proposal.ConfigUpdated && proposal.Config != nil {
		region, err := s.regions.RegionForUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		validation := s.catalog.Validate(proposal.Config, catalog.ValidateOptions{
			Region:            region,
			MonthlyBudgetUSD:  effectiveBudget,
			ScopeOutOfCatalog: proposal.Scope == ScopeOutOfScope,
		})

		switch validation.Verdict {
		case catalog.VerdictRejected:
			// A model fault. The running config is left untouched and the reply
			// says exactly what was wrong - a visible guardrail, not a silent
			// failure.
			slog.WarnContext(ctx, "proposed config rejected",
				"chat_id", chat.ID, "errors", len(validation.Errors))
			params.AssistantContent = appendRejectionNote(params.AssistantContent, validation)

		default:
			// Both OK and NotApplicable are stored. NotApplicable is the
			// spec's confidence/scope guardrail: the architecture appears in
			// the panel, Apply is disabled, and the reason is stated. Without
			// this branch a request the catalog cannot cover would be
			// indistinguishable from a bug.
			doc, err := json.Marshal(proposal.Config)
			if err != nil {
				return nil, err
			}
			var prevDoc json.RawMessage
			if current != nil {
				prevDoc = current.Document
			}
			changes, err := configdiff.Diff(prevDoc, doc)
			if err != nil {
				return nil, err
			}

			// A turn producing an identical document is not a new version.
			if len(changes) > 0 {
				changesJSON, err := json.Marshal(changes)
				if err != nil {
					return nil, err
				}
				validationJSON, err := json.Marshal(validation)
				if err != nil {
					return nil, err
				}
				blockersJSON, err := json.Marshal(validation.Blockers)
				if err != nil {
					return nil, err
				}
				decisionsJSON, _ := json.Marshal(proposal.Decisions)

				if !validation.Applicable() {
					params.AssistantContent = appendBlockedNote(params.AssistantContent, validation)
				}

				params.ExpectedBaseVersion = baseVersion(current)
				params.NewConfig = &store.NewConfigVersion{
					Document:   doc,
					Summary:    firstNonEmpty(proposal.ChangeSummary, "Configuration updated"),
					Rationale:  buildRationale(proposal.Rationale, decisionsJSON),
					Changes:    changesJSON,
					Validation: validationJSON,
					Source:     domain.SourceAssistant,
					Applicable: validation.Applicable(),
					Blockers:   blockersJSON,
					Scope:      string(scopeOrDefault(proposal.Scope)),
					Confidence: clampConfidence(proposal.Confidence),
				}
				stats := configdiff.Summarize(changes)
				unified, uerr := configdiff.Unified(prevDoc, doc, 3)
				if uerr != nil {
					unified = ""
				}
				diff = &configdiff.Result{Changes: changes, Stats: stats, Unified: unified}
			}
		}
	}

	turn, err := s.repo.AppendTurn(ctx, params)
	if err != nil {
		return nil, err
	}

	out := &SendResult{
		BudgetChange:     budgetChange,
		Chat:             turn.Chat,
		UserMessage:      turn.UserMessage,
		AssistantMessage: turn.AssistantMessage,
		ConfigVersion:    turn.ConfigVersion,
		Diff:             diff,
	}
	if diff != nil && turn.ConfigVersion != nil {
		v := turn.ConfigVersion.Version
		diff.To = &v
		if current != nil {
			p := current.Version
			diff.From = &p
		}
	}
	return out, nil
}

func parseProposal(resp *Response) (*Proposal, error) {
	if len(resp.ToolInput) == 0 {
		// Model answered in prose instead of calling the tool. Treat it as a
		// reply with no config change rather than failing the turn.
		if strings.TrimSpace(resp.Text) == "" {
			return nil, fmt.Errorf("model returned neither a tool call nor text")
		}
		return &Proposal{Reply: resp.Text, ConfigUpdated: false}, nil
	}
	var p Proposal
	if err := json.Unmarshal(resp.ToolInput, &p); err != nil {
		return nil, fmt.Errorf("decode tool input: %w", err)
	}
	if strings.TrimSpace(p.Reply) == "" {
		p.Reply = firstNonEmpty(resp.Text, "Updated the configuration.")
	}
	return &p, nil
}

func baseVersion(current *domain.ConfigVersion) *int {
	if current == nil {
		return nil
	}
	v := current.Version
	return &v
}

func scopeOrDefault(s Scope) Scope {
	switch s {
	case ScopeSupported, ScopeNeedsClarification, ScopeOutOfScope:
		return s
	default:
		return ScopeSupported
	}
}

// clampConfidence keeps a self-reported number inside 0..1 without inventing
// one. A model that omits it reports 0, which the UI shows as "not stated"
// rather than "no confidence".
func clampConfidence(c float64) float64 {
	switch {
	case c < 0:
		return 0
	case c > 1:
		return 1
	default:
		return c
	}
}

// appendRejectionNote covers a model FAULT: nothing is stored.
func appendRejectionNote(reply string, v catalog.ValidationResult) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(reply))
	sb.WriteString("\n\nI did not apply this to the running configuration. The proposal failed validation against the vetted catalog:\n")
	for _, issue := range v.Errors {
		_, err := fmt.Fprintf(&sb, "\n- %s: %s", issue.Path, issue.Message)
		if err != nil {
			log.Printf("failed to write response in appendRejectionNote: %v", err)
		}
	}
	sb.WriteString("\n\nThe configuration on the right is unchanged. Tell me how you would like to adjust the requirement and I will try again within what the catalog supports.")
	return sb.String()
}

// appendBlockedNote covers a coherent proposal that cannot be provisioned. The
// architecture IS stored and shown; only Apply is withheld. This is the
// difference between "I could not do that" and "here is what I would build,
// and here is why I am not going to build it".
func appendBlockedNote(reply string, v catalog.ValidationResult) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(reply))
	sb.WriteString("\n\nThis proposal is shown on the right, but it cannot be provisioned as it stands:\n")
	for _, issue := range v.Blockers {
		_, err := fmt.Fprintf(&sb, "\n- %s", issue.Message)
		if err != nil {
			log.Printf("failed to write response in appendBlockedNote: %v", err)
		}
	}
	return sb.String()
}

func buildRationale(rationale string, decisionsJSON []byte) string {
	rationale = strings.TrimSpace(rationale)
	if len(decisionsJSON) > 0 && string(decisionsJSON) != "null" && string(decisionsJSON) != "[]" {
		var decisions []Decision
		if err := json.Unmarshal(decisionsJSON, &decisions); err == nil {
			var sb strings.Builder
			sb.WriteString(rationale)
			for _, d := range decisions {
				_, err := fmt.Fprintf(&sb, "\n- %s: %s (%s)", d.Field, d.Change, d.Why)
				if err != nil {
					log.Printf("failed to write response in buildRationale before tradeoff check: %v", err)
				}
				if d.Tradeoff != "" {
					_, errTradeOff := fmt.Fprintf(&sb, " Trade-off: %s", d.Tradeoff)
					if errTradeOff != nil {
						log.Printf("failed to write response in buildRationale while writing Tradeoff: %v", errTradeOff)
					}
				}
			}
			return strings.TrimSpace(sb.String())
		}
	}
	return rationale
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Engine reports which reasoning implementation is active and which model it
// is using, so a client can say "simulated" rather than inferring it from a
// build-time flag that can go stale in the dangerous direction.
func (s *Service) Engine() (name, model string) {
	return s.llm.Name(), s.llm.ModelID()
}

// ManualEditResult is what a hand-edited config returns to the client.
type ManualEditResult struct {
	ConfigVersion *domain.ConfigVersion `json:"config_version"`
	Message       *domain.Message       `json:"message"`
	Diff          *configdiff.Result    `json:"diff"`
}

// ApplyManualEdit writes a user-authored config version.
//
// It lives here, in the package named for the reasoning engine, because this is
// where the validation pipeline lives - catalog, region resolution and the
// chat's budget ceiling. A manual edit has to pass through exactly the same
// gates as a model proposal, and duplicating them in the handler is how the two
// paths drift until one of them stops enforcing the budget.
//
// The document is treated as untrusted input in precisely the way model output
// is. A hand-edited config is not more trustworthy than a generated one; it is
// differently sourced.
func (s *Service) ApplyManualEdit(
	ctx context.Context,
	userID, chatID uuid.UUID,
	document json.RawMessage,
	basedOnVersion int,
	note string,
) (*ManualEditResult, error) {
	chat, err := s.repo.GetChat(ctx, userID, chatID)
	if err != nil {
		return nil, err
	}
	if chat.ArchivedAt != nil {
		return nil, fmt.Errorf("%w: this chat is archived; restore it before making changes",
			domain.ErrConflict)
	}

	var cfg catalog.ArchitectureConfig
	dec := json.NewDecoder(bytes.NewReader(document))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, domain.NewValidationError("document",
			"is not a valid architecture configuration: "+err.Error())
	}

	region, err := s.regions.RegionForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Validate mutates cfg: it stamps region, fills parameter defaults and
	// recomputes estimated_cost. What gets stored is the normalized document,
	// never the raw submission - otherwise a client could omit a default and
	// change behavior, or state a cost figure of its own.
	validation := s.catalog.Validate(&cfg, catalog.ValidateOptions{
		Region:           region,
		MonthlyBudgetUSD: chat.MonthlyBudgetUSD,
	})

	// A model fault is discarded; a hand-written fault is reported back so the
	// editor can show it. The user is present and can fix it, which is the one
	// respect in which this path differs from a turn.
	if validation.Verdict == catalog.VerdictRejected {
		fields := make(map[string]string, len(validation.Errors))
		for _, issue := range validation.Errors {
			fields[issue.Path] = issue.Message
		}
		return nil, &domain.ValidationError{Fields: fields}
	}

	normalised, err := json.Marshal(&cfg)
	if err != nil {
		return nil, err
	}
	validationJSON, err := json.Marshal(validation)
	if err != nil {
		return nil, err
	}
	blockersJSON, err := json.Marshal(validation.Blockers)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(note) == "" {
		note = "Configuration edited directly."
	}

	// Captured before the write: afterwards CurrentConfig returns the version
	// this edit just created, and diffing a document against itself is empty.
	var previous json.RawMessage
	if cur, cErr := s.repo.CurrentConfig(ctx, chatID); cErr == nil && cur != nil {
		previous = cur.Document
	}

	cv, msg, err := s.repo.ApplyManualEdit(ctx, store.ManualEditParams{
		ChatID:         chatID,
		UserID:         userID,
		Document:       normalised,
		Applicable:     validation.Applicable(),
		Blockers:       blockersJSON,
		Validation:     validationJSON,
		Summary:        "Edited directly",
		BasedOnVersion: basedOnVersion,
		Note:           note,
	})
	if err != nil {
		return nil, err
	}

	out := &ManualEditResult{ConfigVersion: cv, Message: msg}

	// Best effort: the edit is already written, so a diff that fails to render
	// must not fail the request.
	if changes, dErr := configdiff.Diff(previous, cv.Document); dErr == nil {
		stats := configdiff.Summarize(changes)
		unified, uErr := configdiff.Unified(previous, cv.Document, 3)
		if uErr != nil {
			unified = ""
		}
		out.Diff = &configdiff.Result{
			Changes: changes, Stats: stats, Unified: unified,
			From: cv.ParentVersion, To: &cv.Version,
		}
	}
	return out, nil
}

// BudgetChange reports what a turn did, or declined to do, about the chat's
// spending ceiling.
type BudgetChange struct {
	// Applied is false when the request would loosen the ceiling. The client
	// should offer a confirmation that calls PATCH /chats/:id.
	Applied bool     `json:"applied"`
	FromUSD *float64 `json:"from_usd"`
	ToUSD   *float64 `json:"to_usd"`
	// Clear is set when the request was to remove the ceiling entirely.
	Clear bool `json:"clear"`
	// Quote is what the user said, so a confirmation can show the reading
	// rather than only the number.
	Quote  string `json:"quote"`
	Reason string `json:"reason"`
}

// resolveBudgetChange decides whether a stated ceiling takes effect now.
//
// The rule: TIGHTENING APPLIES IMMEDIATELY, LOOSENING NEEDS A HUMAN.
//
// Tightening is safe in the direction that matters. If the model misreads
// "we spent $500 last month" as a $500 limit and the real ceiling was $20, the
// limit stays $20 and nothing was provisioned that should not have been. The
// worst case is a blocker the user corrects in settings.
//
// Loosening is the act that widens what can be provisioned, so it goes through
// the same shape as every other consequential action here: the model proposes,
// a human approves. Setting a first ceiling counts as tightening - there was no
// limit before, so any number is narrower than none.
//
// Returns nil when the turn said nothing about a budget, which is most turns.
func resolveBudgetChange(current *float64, req *BudgetRequest) *BudgetChange {
	if req == nil {
		return nil
	}

	change := &BudgetChange{FromUSD: current, Quote: strings.TrimSpace(req.Quote)}

	if req.Clear || req.AmountUSD == nil {
		change.Clear = true
		change.Applied = false
		change.Reason = "Removing the spending limit widens what can be provisioned, so it needs your confirmation."
		return change
	}

	asked := *req.AmountUSD
	if asked <= 0 || asked > 1000000 {
		change.Applied = false
		change.Reason = "That figure is outside the range a monthly ceiling can take."
		return change
	}
	change.ToUSD = &asked

	switch {
	case current == nil:
		// No ceiling at all is looser than any ceiling.
		change.Applied = true
		change.Reason = "Set, because there was no limit before."
	case asked < *current:
		change.Applied = true
		change.Reason = "Applied, because it is lower than the current limit."
	case asked == *current:
		change.Applied = false
		change.Reason = "That is already the limit."
	default:
		change.Applied = false
		change.Reason = "Raising the limit widens what can be provisioned, so it needs your confirmation."
	}
	return change
}

// appendBudgetNote states plainly what happened to the ceiling.
//
// Added by the server rather than trusted to the model: the model does not know
// whether its request was accepted, and a reply claiming a limit was raised when
// it was not would be the most misleading thing the system could say.
func appendBudgetNote(reply string, c *BudgetChange) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(reply))
	sb.WriteString("\n\n")

	switch {
	case c.Applied && c.ToUSD != nil:
		_, _ = fmt.Fprintf(&sb, "Spending limit for this chat set to $%.2f/month.", *c.ToUSD)
	case c.Clear:
		sb.WriteString("You asked to remove the spending limit. I have not changed it - " +
			"removing a limit widens what can be provisioned, so it needs your confirmation in chat settings.")
	case c.ToUSD != nil && c.FromUSD != nil && *c.ToUSD > *c.FromUSD:
		_, _ = fmt.Fprintf(&sb, "You asked to raise the spending limit to $%.2f/month. I have left it at $%.2f - "+
			"raising it widens what can be provisioned, so it needs your confirmation in chat settings.",
			*c.ToUSD, *c.FromUSD)
	default:
		sb.WriteString(c.Reason)
	}

	if c.Quote != "" {
		_, _ = fmt.Fprintf(&sb, " (Read from: %q)", c.Quote)
	}
	return sb.String()
}
