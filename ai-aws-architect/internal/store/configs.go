package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cloud-ai/ai-aws-architect/internal/configdiff"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

// NewConfigVersion describes a config version that is about to be appended.
// Nil on a turn where the assistant answered a question without changing
// anything, which is the common case for follow-up questions.
type NewConfigVersion struct {
	Document   json.RawMessage
	Summary    string
	Rationale  string
	Changes    json.RawMessage
	Validation json.RawMessage
	Source     domain.ConfigSource
	// Applicable false means: store and display this, but do not allow "apply".
	Applicable bool
	Blockers   json.RawMessage
	Scope      string
	Confidence float64
}

type AppendTurnParams struct {
	ChatID           uuid.UUID
	UserID           uuid.UUID
	UserContent      string
	AssistantContent string
	Model            string
	InputTokens      int
	OutputTokens     int
	// TitleSuggestion is applied only while the chat still has its default
	// title, so a user rename is never clobbered.
	TitleSuggestion string
	// SetBudget and NewBudgetUSD change the chat's spending ceiling as part of
	// this turn. Written in the same transaction as the messages, because a
	// ceiling that moved without a message explaining it is the panel changing
	// for no visible reason - the same invariant the config version has.
	//
	// Only tightening reaches here; loosening is refused upstream and offered
	// to the user as a confirmation instead.
	SetBudget    bool
	NewBudgetUSD *float64
	NewConfig    *NewConfigVersion
	// ExpectedBaseVersion is the config version the assistant actually
	// reasoned against, captured before the model call. Nil when the chat had
	// no config yet.
	//
	// The model call happens outside this transaction (it takes seconds, and
	// holding a row lock across it would serialize every user). That means the
	// running config can move underneath a turn - two browser tabs on the same
	// chat is enough. Without this check the new version would get the right
	// version NUMBER but a diff computed against a base that no longer exists,
	// which corrupts the history silently. Checked only when a config change
	// is being written; a plain question does not care.
	ExpectedBaseVersion *int
}

type TurnResult struct {
	Chat             domain.Chat
	UserMessage      domain.Message
	AssistantMessage domain.Message
	ConfigVersion    *domain.ConfigVersion
}

// AppendTurn writes the user message, the assistant reply and (optionally) the
// resulting config version in ONE transaction. Either the whole turn lands or
// none of it does — no half-written turns where the panel shows a config that
// no message explains.
func (s *ChatStore) AppendTurn(ctx context.Context, p AppendTurnParams) (*TurnResult, error) {
	var res TurnResult

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Row lock serializes concurrent sends in the same chat, which is what
		// makes seq and version allocation safe without a sequence per chat.
		var seq int64
		var title string
		var currentVersion *int
		err := tx.QueryRow(ctx, `
			SELECT message_seq, title, current_config_version
			FROM chats WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			p.ChatID, p.UserID).Scan(&seq, &title, &currentVersion)
		if err != nil {
			return mapErr(err)
		}

		if p.NewConfig != nil && !sameVersion(p.ExpectedBaseVersion, currentVersion) {
			return domain.ErrStaleConfig
		}

		userSeq := seq + 1
		assistantSeq := seq + 2

		res.UserMessage = domain.Message{
			ID: uuid.New(), ChatID: p.ChatID, Seq: userSeq,
			Role: domain.RoleUser, Content: p.UserContent,
		}
		if err := insertMessage(ctx, tx, &res.UserMessage, 0, 0, ""); err != nil {
			return err
		}

		res.AssistantMessage = domain.Message{
			ID: uuid.New(), ChatID: p.ChatID, Seq: assistantSeq,
			Role: domain.RoleAssistant, Content: p.AssistantContent, Model: p.Model,
			InputTokens: p.InputTokens, OutputTokens: p.OutputTokens,
		}
		if err := insertMessage(ctx, tx, &res.AssistantMessage, p.InputTokens, p.OutputTokens, p.Model); err != nil {
			return err
		}

		newVersion := currentVersion
		if p.NewConfig != nil {
			next := 1
			if currentVersion != nil {
				next = *currentVersion + 1
			}
			cv, err := insertConfigVersion(ctx, tx, configInsert{
				ChatID:             p.ChatID,
				Version:            next,
				ParentVersion:      currentVersion,
				Source:             p.NewConfig.Source,
				Document:           p.NewConfig.Document,
				Summary:            p.NewConfig.Summary,
				Rationale:          p.NewConfig.Rationale,
				Changes:            p.NewConfig.Changes,
				Validation:         p.NewConfig.Validation,
				CreatedByMessageID: &res.AssistantMessage.ID,
				Applicable:         p.NewConfig.Applicable,
				Blockers:           p.NewConfig.Blockers,
				Scope:              p.NewConfig.Scope,
				Confidence:         &p.NewConfig.Confidence,
			})
			if err != nil {
				return err
			}
			res.ConfigVersion = cv
			newVersion = &cv.Version
			res.AssistantMessage.ConfigVersion = &cv.Version

			if _, err := tx.Exec(ctx,
				`UPDATE messages SET config_version = $2 WHERE id = $1`,
				res.AssistantMessage.ID, cv.Version); err != nil {
				return err
			}
		}

		newTitle := title
		if isDefaultTitle(title) && strings.TrimSpace(p.TitleSuggestion) != "" {
			newTitle = truncate(strings.TrimSpace(p.TitleSuggestion), 80)
		}

		err = tx.QueryRow(ctx, `
			UPDATE chats
			SET message_seq = $2,
			    title = $3,
			    current_config_version = $4,
			    monthly_budget_usd = CASE
			        WHEN $5::bool THEN $6
			        ELSE monthly_budget_usd
			    END,
			    last_message_at = now(),
			    updated_at = now()
			WHERE id = $1
			RETURNING id, title, current_config_version, message_seq,
			          last_message_at, archived_at, created_at, updated_at,
			          monthly_budget_usd`,
			p.ChatID, assistantSeq, newTitle, newVersion, p.SetBudget, p.NewBudgetUSD,
		).Scan(&res.Chat.ID, &res.Chat.Title, &res.Chat.CurrentConfigVersion,
			&res.Chat.MessageCount, &res.Chat.LastMessageAt, &res.Chat.ArchivedAt,
			&res.Chat.CreatedAt, &res.Chat.UpdatedAt, &res.Chat.MonthlyBudgetUSD)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// Revert appends a NEW version carrying a copy of targetVersion's document, and
// narrates it into the transcript as a system message. Nothing is deleted, so
// you can revert a revert.
func (s *ChatStore) Revert(ctx context.Context, userID, chatID uuid.UUID, targetVersion int, note string) (*domain.ConfigVersion, *domain.Message, error) {
	var cv *domain.ConfigVersion
	var msg domain.Message

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var seq int64
		var currentVersion *int
		err := tx.QueryRow(ctx, `
			SELECT message_seq, current_config_version
			FROM chats WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			chatID, userID).Scan(&seq, &currentVersion)
		if err != nil {
			return mapErr(err)
		}
		if currentVersion == nil {
			return domain.ErrNotFound
		}

		var doc, validation, blockers []byte
		var summary, scope string
		var applicable bool
		var confidence *float64
		err = tx.QueryRow(ctx, `
			SELECT document, validation, summary, applicable, blockers, scope, confidence
			FROM chat_config_versions WHERE chat_id = $1 AND version = $2`,
			chatID, targetVersion).Scan(&doc, &validation, &summary,
			&applicable, &blockers, &scope, &confidence)
		if err != nil {
			return mapErr(err)
		}
		if targetVersion == *currentVersion {
			return domain.ErrConflict // already the running config
		}

		// The change list is the diff from the version being replaced to the
		// one being restored, so a reverted version reads the same way as any
		// other in the history rather than showing an empty change set.
		var liveDoc []byte
		if err := tx.QueryRow(ctx, `
			SELECT document FROM chat_config_versions WHERE chat_id = $1 AND version = $2`,
			chatID, *currentVersion).Scan(&liveDoc); err != nil {
			return mapErr(err)
		}
		changeList, err := configdiff.Diff(liveDoc, doc)
		if err != nil {
			return err
		}
		changesJSON, err := json.Marshal(changeList)
		if err != nil {
			return err
		}

		next := *currentVersion + 1
		msg = domain.Message{
			ID: uuid.New(), ChatID: chatID, Seq: seq + 1, Role: domain.RoleSystem,
			Content: note, ConfigVersion: &next,
		}
		if err := insertMessage(ctx, tx, &msg, 0, 0, ""); err != nil {
			return err
		}

		cv, err = insertConfigVersion(ctx, tx, configInsert{
			ChatID:              chatID,
			Version:             next,
			ParentVersion:       currentVersion,
			RevertedFromVersion: &targetVersion,
			Source:              domain.SourceRevert,
			Document:            doc,
			Summary:             summary,
			Rationale:           note,
			Changes:             changesJSON,
			// Carried forward, not blanked: the document is a byte-for-byte
			// copy of a version that already passed validation. Writing {} here
			// made every reverted version look unvalidated to the UI.
			Validation:         validation,
			CreatedByMessageID: &msg.ID,
			// A revert restores a document verbatim, so its applicability
			// travels with it. Reverting to a blocked proposal gives you a
			// blocked version, which is correct.
			Applicable: applicable,
			Blockers:   blockers,
			Scope:      scope,
			// Confidence belongs to the document, so a revert inherits the
			// target's - including its absence.
			Confidence: confidence,
		})
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE chats
			SET message_seq = $2, current_config_version = $3,
			    last_message_at = now(), updated_at = now()
			WHERE id = $1`, chatID, msg.Seq, next)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return cv, &msg, nil
}

func (s *ChatStore) CurrentConfig(ctx context.Context, chatID uuid.UUID) (*domain.ConfigVersion, error) {
	cv, err := scanConfigVersion(s.pool.QueryRow(ctx, `
		SELECT `+configColumns+`
		FROM chat_config_versions
		WHERE chat_id = $1
		ORDER BY version DESC
		LIMIT 1`, chatID))
	if err != nil {
		return nil, mapErr(err)
	}
	return cv, nil
}

func (s *ChatStore) GetConfigVersion(ctx context.Context, chatID uuid.UUID, version int) (*domain.ConfigVersion, error) {
	cv, err := scanConfigVersion(s.pool.QueryRow(ctx, `
		SELECT `+configColumns+`
		FROM chat_config_versions
		WHERE chat_id = $1 AND version = $2`, chatID, version))
	if err != nil {
		return nil, mapErr(err)
	}
	return cv, nil
}

// ListConfigVersions returns metadata only (no documents) for the history list.
func (s *ChatStore) ListConfigVersions(ctx context.Context, chatID uuid.UUID, current *int) ([]domain.ConfigVersionMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, version, parent_version, reverted_from_version, source, summary,
		       created_at, applicable, scope, confidence
		FROM chat_config_versions
		WHERE chat_id = $1
		ORDER BY version DESC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.ConfigVersionMeta
	out = []domain.ConfigVersionMeta{}
	for rows.Next() {
		var m domain.ConfigVersionMeta
		if err := rows.Scan(&m.ID, &m.Version, &m.ParentVersion, &m.RevertedFromVersion,
			&m.Source, &m.Summary, &m.CreatedAt, &m.Applicable, &m.Scope,
			&m.Confidence); err != nil {
			return nil, err
		}
		m.IsCurrent = current != nil && *current == m.Version
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- internals ----------------------------------------------------------

const configColumns = `id, chat_id, version, parent_version, reverted_from_version,
	source, document, summary, rationale, changes, validation,
	created_by_message_id, created_at, applicable, blockers, scope, confidence`

type rowScanner interface{ Scan(dest ...any) error }

func scanConfigVersion(row rowScanner) (*domain.ConfigVersion, error) {
	var cv domain.ConfigVersion
	var doc, changes, validation, blockers []byte
	if err := row.Scan(&cv.ID, &cv.ChatID, &cv.Version, &cv.ParentVersion,
		&cv.RevertedFromVersion, &cv.Source, &doc, &cv.Summary, &cv.Rationale,
		&changes, &validation, &cv.CreatedByMessageID, &cv.CreatedAt,
		&cv.Applicable, &blockers, &cv.Scope, &cv.Confidence); err != nil {
		return nil, err
	}
	cv.Document = doc
	cv.Changes = changes
	cv.Validation = validation
	cv.Blockers = blockers
	return &cv, nil
}

func insertMessage(ctx context.Context, tx pgx.Tx, m *domain.Message, in, out int, model string) error {
	return tx.QueryRow(ctx, `
		INSERT INTO messages (id, chat_id, seq, role, content, model, input_tokens, output_tokens)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`,
		m.ID, m.ChatID, m.Seq, string(m.Role), m.Content, model, in, out,
	).Scan(&m.CreatedAt)
}

type configInsert struct {
	ChatID              uuid.UUID
	Version             int
	ParentVersion       *int
	RevertedFromVersion *int
	Source              domain.ConfigSource
	Document            json.RawMessage
	Summary             string
	Rationale           string
	Changes             json.RawMessage
	Validation          json.RawMessage
	CreatedByMessageID  *uuid.UUID
	Applicable          bool
	Blockers            json.RawMessage
	Scope               string
	// Confidence is nil when no model produced this document - a manual edit,
	// or a revert to one. The column is nullable (migration 0006) so the
	// absence is stored as NULL rather than flattened to a zero that reads as
	// a score.
	Confidence *float64
}

func insertConfigVersion(ctx context.Context, tx pgx.Tx, in configInsert) (*domain.ConfigVersion, error) {
	if len(in.Changes) == 0 {
		in.Changes = json.RawMessage("[]")
	}
	if len(in.Validation) == 0 {
		in.Validation = json.RawMessage("{}")
	}
	if len(in.Blockers) == 0 {
		in.Blockers = json.RawMessage("[]")
	}
	if in.Scope == "" {
		in.Scope = "supported"
	}
	cv := domain.ConfigVersion{
		ID: uuid.New(), ChatID: in.ChatID, Version: in.Version,
		ParentVersion: in.ParentVersion, RevertedFromVersion: in.RevertedFromVersion,
		Source: in.Source, Document: in.Document, Summary: in.Summary,
		Rationale: in.Rationale, Changes: in.Changes, Validation: in.Validation,
		CreatedByMessageID: in.CreatedByMessageID, Applicable: in.Applicable,
		Blockers: in.Blockers, Scope: in.Scope,
	}
	cv.Confidence = in.Confidence
	err := tx.QueryRow(ctx, `
		INSERT INTO chat_config_versions
			(id, chat_id, version, parent_version, reverted_from_version, source,
			 document, summary, rationale, changes, validation, created_by_message_id,
			 applicable, blockers, scope, confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10::jsonb, $11::jsonb, $12,
		        $13, $14::jsonb, $15, $16)
		RETURNING created_at`,
		cv.ID, cv.ChatID, cv.Version, cv.ParentVersion, cv.RevertedFromVersion,
		string(cv.Source), []byte(cv.Document), cv.Summary, cv.Rationale,
		[]byte(cv.Changes), []byte(cv.Validation), cv.CreatedByMessageID,
		cv.Applicable, []byte(cv.Blockers), cv.Scope, in.Confidence,
	).Scan(&cv.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &cv, nil
}

// sameVersion compares two optional version pointers by value.
func sameVersion(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func isDefaultTitle(t string) bool {
	t = strings.TrimSpace(t)
	return t == "" || t == defaultChatTitle
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "..."
}

// ManualEditParams describes a user-authored replacement for the running
// config.
type ManualEditParams struct {
	ChatID uuid.UUID
	UserID uuid.UUID
	// Document has already been through catalog validation by the caller, so
	// region is stamped, defaults are filled and estimated_cost is recomputed.
	// The store never trusts a document it was handed raw.
	Document   json.RawMessage
	Applicable bool
	Blockers   json.RawMessage
	Validation json.RawMessage
	Summary    string
	// BasedOnVersion is the version the editor was looking at. Rejected if the
	// running config has moved since - the same protection AppendTurn gets from
	// ExpectedBaseVersion, for the same reason: an edit computed against a
	// stale base produces a diff that describes a change nobody made.
	BasedOnVersion int
	Note           string
}

// ApplyManualEdit appends a user-authored config version.
//
// Runs the same append-only path as an assistant turn: a new version, a diff
// against the one it replaces, and a system message narrating it into the
// transcript so the panel never changes without the conversation explaining
// why. The only difference is source = manual and the absence of a model.
func (s *ChatStore) ApplyManualEdit(ctx context.Context, p ManualEditParams) (*domain.ConfigVersion, *domain.Message, error) {
	var cv *domain.ConfigVersion
	var msg domain.Message

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var seq int64
		var currentVersion *int
		err := tx.QueryRow(ctx, `
			SELECT message_seq, current_config_version
			FROM chats WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			p.ChatID, p.UserID).Scan(&seq, &currentVersion)
		if err != nil {
			return mapErr(err)
		}
		if currentVersion == nil {
			// Nothing to edit yet. Sending a message is how a config starts.
			return domain.ErrNotFound
		}
		if *currentVersion != p.BasedOnVersion {
			return domain.ErrStaleConfig
		}

		var liveDoc []byte
		if err := tx.QueryRow(ctx, `
			SELECT document FROM chat_config_versions WHERE chat_id = $1 AND version = $2`,
			p.ChatID, *currentVersion).Scan(&liveDoc); err != nil {
			return mapErr(err)
		}

		changeList, err := configdiff.Diff(liveDoc, p.Document)
		if err != nil {
			return err
		}
		// An edit that changes nothing is not a version. Same rule as a
		// conversational turn, so the history does not fill with no-ops from
		// someone opening the editor and pressing save.
		if len(changeList) == 0 {
			return domain.ErrConflict
		}
		changesJSON, err := json.Marshal(changeList)
		if err != nil {
			return err
		}

		next := *currentVersion + 1
		msg = domain.Message{
			ID: uuid.New(), ChatID: p.ChatID, Seq: seq + 1, Role: domain.RoleSystem,
			Content: p.Note, ConfigVersion: &next,
		}
		if err := insertMessage(ctx, tx, &msg, 0, 0, ""); err != nil {
			return err
		}

		cv, err = insertConfigVersion(ctx, tx, configInsert{
			ChatID:             p.ChatID,
			Version:            next,
			ParentVersion:      currentVersion,
			Source:             domain.SourceManual,
			Document:           p.Document,
			Summary:            p.Summary,
			Rationale:          p.Note,
			Changes:            changesJSON,
			Validation:         p.Validation,
			CreatedByMessageID: &msg.ID,
			Applicable:         p.Applicable,
			Blockers:           p.Blockers,
			// A handwritten config has no model behind it, so neither field
			// means anything. Recording a confidence would invent signal.
			Scope: ScopeSupportedManual,
			// No model, so no confidence. Not zero - absent.
			Confidence: nil,
		})
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE chats
			SET message_seq = $2, current_config_version = $3,
			    last_message_at = now(), updated_at = now()
			WHERE id = $1`, p.ChatID, msg.Seq, next)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return cv, &msg, nil
}

// ScopeSupportedManual is the scope written for a hand-edited version. The
// reasoning package owns the Scope vocabulary, but the store cannot import it
// without a cycle, so the one value it needs is named here.
const ScopeSupportedManual = "supported"
