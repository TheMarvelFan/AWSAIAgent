package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

type UserStore struct{ pool *pgxpool.Pool }

func NewUserStore(pool *pgxpool.Pool) *UserStore { return &UserStore{pool: pool} }

// NormalizeEmail is used on both write and read paths so lookups always match.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *UserStore) Create(ctx context.Context, email, passwordHash, displayName string) (*domain.User, error) {
	u := domain.User{
		ID:          uuid.New(),
		Email:       NormalizeEmail(email),
		DisplayName: strings.TrimSpace(displayName),
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, password_hash, display_name)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at`,
		u.ID, u.Email, passwordHash, u.DisplayName,
	).Scan(&u.CreatedAt)
	if err != nil {
		if pgErrCode(err) == codeUniqueViolation {
			return nil, domain.ErrEmailTaken
		}
		return nil, err
	}
	return &u, nil
}

// GetCredentialsByEmail returns the user plus the stored password hash.
// Kept separate from GetByID so the hash never leaks into a response DTO by
// accident.
func (s *UserStore) GetCredentialsByEmail(ctx context.Context, email string) (*domain.User, string, error) {
	var u domain.User
	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, created_at, password_hash
		FROM users WHERE lower(email) = $1`,
		NormalizeEmail(email),
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt, &hash)
	if err != nil {
		return nil, "", mapErr(err)
	}
	return &u, hash, nil
}

func (s *UserStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, created_at FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}
