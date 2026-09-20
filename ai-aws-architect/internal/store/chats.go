package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

type ChatStore struct{ pool *pgxpool.Pool }

func NewChatStore(pool *pgxpool.Pool) *ChatStore { return &ChatStore{pool: pool} }

const defaultChatTitle = "New chat"

func (s *ChatStore) CreateChat(ctx context.Context, userID uuid.UUID, title string, budget *float64) (*domain.Chat, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = defaultChatTitle
	}
	c := domain.Chat{ID: uuid.New(), Title: title, MonthlyBudgetUSD: budget}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chats (id, user_id, title, monthly_budget_usd)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at`,
		c.ID, userID, c.Title, budget,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListChats powers the left-hand history panel.
func (s *ChatStore) ListChats(ctx context.Context, userID uuid.UUID, limit, offset int, includeArchived bool) ([]domain.Chat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, current_config_version, message_seq,
		       last_message_at, archived_at, created_at, updated_at, monthly_budget_usd
		FROM chats
		WHERE user_id = $1
		  AND ($2::bool OR archived_at IS NULL)
		ORDER BY COALESCE(last_message_at, created_at) DESC
		LIMIT $3 OFFSET $4`,
		userID, includeArchived, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.Chat, 0, limit)
	for rows.Next() {
		var c domain.Chat
		if err := rows.Scan(&c.ID, &c.Title, &c.CurrentConfigVersion, &c.MessageCount,
			&c.LastMessageAt, &c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt,
			&c.MonthlyBudgetUSD); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetChat doubles as the authorization check: a chat belonging to someone else
// is indistinguishable from one that does not exist.
func (s *ChatStore) GetChat(ctx context.Context, userID, chatID uuid.UUID) (*domain.Chat, error) {
	var c domain.Chat
	err := s.pool.QueryRow(ctx, `
		SELECT id, title, current_config_version, message_seq,
		       last_message_at, archived_at, created_at, updated_at, monthly_budget_usd
		FROM chats WHERE id = $1 AND user_id = $2`, chatID, userID,
	).Scan(&c.ID, &c.Title, &c.CurrentConfigVersion, &c.MessageCount,
		&c.LastMessageAt, &c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt,
		&c.MonthlyBudgetUSD)
	if err != nil {
		return nil, mapErr(err)
	}
	return &c, nil
}

// UpdateChat applies the mutable chat fields. A nil pointer means "leave
// alone"; clearing the budget is a separate explicit operation so that an
// omitted field can never silently remove a cost ceiling.
// archived is nil to leave the archive state alone, true to archive, false to
// restore. Restoring clears archived_at outright rather than tracking history:
// a chat is either in the sidebar or it is not.
func (s *ChatStore) UpdateChat(ctx context.Context, userID, chatID uuid.UUID, title *string, budget *float64, clearBudget bool, archived *bool) (*domain.Chat, error) {
	tag, err := s.pool.Exec(ctx, `
			UPDATE chats
			SET title = COALESCE($3, title),
				monthly_budget_usd = CASE
					WHEN $5::bool THEN NULL
					ELSE COALESCE($4, monthly_budget_usd)
				END,
				archived_at = CASE
					WHEN $6::bool IS NULL THEN archived_at
					WHEN $6::bool THEN COALESCE(archived_at, now())
					END,
				updated_at = now()
			WHERE id = $1 AND user_id = $2`, chatID, userID, title, budget, clearBudget, archived)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.ErrNotFound
	}
	return s.GetChat(ctx, userID, chatID)
}

// ArchiveChat is a soft delete: history and every config version survive, the
// chat just leaves the sidebar.
func (s *ChatStore) ArchiveChat(ctx context.Context, userID, chatID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE chats SET archived_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND archived_at IS NULL`, chatID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListMessages returns messages oldest-first. beforeSeq (0 = newest page) walks
// backwards through history for infinite scroll.
func (s *ChatStore) ListMessages(ctx context.Context, chatID uuid.UUID, limit int, beforeSeq int64) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, chat_id, seq, role, content, config_version, model,
		       input_tokens, output_tokens, created_at
		FROM messages
		WHERE chat_id = $1 AND ($2 = 0 OR seq < $2)
		ORDER BY seq DESC
		LIMIT $3`, chatID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.Message, 0, limit)
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Seq, &m.Role, &m.Content, &m.ConfigVersion,
			&m.Model, &m.InputTokens, &m.OutputTokens, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Query is DESC for the LIMIT to mean "most recent N"; the client wants
	// ascending order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// RecentMessages is the LLM context window: the last n turns, oldest-first.
func (s *ChatStore) RecentMessages(ctx context.Context, chatID uuid.UUID, n int) ([]domain.Message, error) {
	return s.ListMessages(ctx, chatID, n, 0)
}
