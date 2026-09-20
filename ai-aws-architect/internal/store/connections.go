package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

type ConnectionStore struct{ pool *pgxpool.Pool }

func NewConnectionStore(pool *pgxpool.Pool) *ConnectionStore {
	return &ConnectionStore{pool: pool}
}

const connectionColumns = `id, user_id, external_id, role_name, role_arn, account_id,
	region, status, last_error, last_error_code, verified_at, last_checked_at,
	created_at, updated_at`

// EnsurePending creates the connection row on first connect, or returns the
// existing one untouched.
//
// The upsert is deliberately a no-op on conflict: the external ID and role name
// must survive a reconnect, or the role the user already created in their
// account stops matching what we expect.
func (s *ConnectionStore) EnsurePending(ctx context.Context, userID uuid.UUID, externalID, roleName, region string) (*domain.AWSConnection, error) {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO aws_connections (id, user_id, external_id, role_name, region, status)
		VALUES ($1, $2, $3, $4, $5, 'pending')
		ON CONFLICT (user_id) DO NOTHING`,
		uuid.New(), userID, externalID, roleName, region)
	if err != nil {
		return nil, err
	}
	return s.GetByUser(ctx, userID)
}

func (s *ConnectionStore) GetByUser(ctx context.Context, userID uuid.UUID) (*domain.AWSConnection, error) {
	var c domain.AWSConnection
	err := s.pool.QueryRow(ctx, `
		SELECT `+connectionColumns+`
		FROM aws_connections WHERE user_id = $1`, userID,
	).Scan(&c.ID, &c.UserID, &c.ExternalID, &c.RoleName, &c.RoleARN, &c.AccountID,
		&c.Region, &c.Status, &c.LastError, &c.LastErrorCode, &c.VerifiedAt,
		&c.LastCheckedAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &c, nil
}

// MarkActive records a successful verification.
func (s *ConnectionStore) MarkActive(ctx context.Context, userID uuid.UUID, roleARN, accountID string) (*domain.AWSConnection, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE aws_connections
		SET role_arn = $2, account_id = $3, status = 'active',
		    last_error = '', last_error_code = '',
		    verified_at = now(), last_checked_at = now(), updated_at = now()
		WHERE user_id = $1`, userID, roleARN, accountID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.ErrNotFound
	}
	return s.GetByUser(ctx, userID)
}

// MarkBroken records a failed health check. The role ARN and account are kept
// so the UI can still say which account is affected.
func (s *ConnectionStore) MarkBroken(ctx context.Context, userID uuid.UUID, code, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE aws_connections
		SET status = 'broken', last_error = $2, last_error_code = $3,
		    last_checked_at = now(), updated_at = now()
		WHERE user_id = $1`, userID, reason, code)
	return err
}

// TouchChecked records a successful health check without changing anything else.
func (s *ConnectionStore) TouchChecked(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE aws_connections
		SET status = 'active', last_error = '', last_error_code = '',
		    last_checked_at = now(), updated_at = now()
		WHERE user_id = $1`, userID)
	return err
}

func (s *ConnectionStore) Delete(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM aws_connections WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Fresh reports whether the last health check is recent enough to trust,
// so a page load does not always cost an STS round trip.
func Fresh(c *domain.AWSConnection, window time.Duration) bool {
	return c.LastCheckedAt != nil && time.Since(*c.LastCheckedAt) < window
}
