package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TokenStore struct{ pool *pgxpool.Pool }

func NewTokenStore(pool *pgxpool.Pool) *TokenStore { return &TokenStore{pool: pool} }

type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
	RevokedAt *time.Time
}

func (s *TokenStore) Create(ctx context.Context, userID uuid.UUID, hash []byte, expiresAt time.Time, userAgent, ip string) (uuid.UUID, error) {
	id := uuid.New()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, user_agent, ip)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id, userID, hash, expiresAt, userAgent, ip)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (s *TokenStore) GetByHash(ctx context.Context, hash []byte) (*RefreshToken, error) {
	var t RefreshToken
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`, hash,
	).Scan(&t.ID, &t.UserID, &t.ExpiresAt, &t.RevokedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

// RotateOutcome is the verdict from ConsumeForRotation.
type RotateOutcome int

const (
	// RotateInvalid unknown or expired token.
	RotateInvalid RotateOutcome = iota
	// RotateOK normal rotation.
	RotateOK
	// RotateGrace this token was already rotated, but so recently that it is
	// almost certainly a second browser tab or a retried request rather than
	// theft. A fresh session is issued and no sessions are revoked.
	RotateGrace
	// RotateReuse a long-revoked token was presented. Treated as theft; every
	// live session for the user has been revoked.
	RotateReuse
)

// ConsumeForRotation atomically validates the presented token, issues the
// replacement row, and marks the old one rotated - all under a row lock, so two
// simultaneous refreshes cannot both succeed.
//
// The grace window matters in practice: without it, two browser tabs refreshing
// at the same moment means the second presents an already-revoked token, which
// trips theft detection and logs the user out of everything. That is a bad
// thing to discover during a live demo. Inside the window the second request is
// treated as benign.
func (s *TokenStore) ConsumeForRotation(
	ctx context.Context,
	oldHash, newHash []byte,
	newExpiry time.Time,
	grace time.Duration,
	userAgent, ip string,
) (RotateOutcome, uuid.UUID, error) {
	var outcome RotateOutcome
	var userID uuid.UUID

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		var expiresAt time.Time
		var revokedAt *time.Time
		var replacedBy *uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT id, user_id, expires_at, revoked_at, replaced_by
			FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, oldHash,
		).Scan(&id, &userID, &expiresAt, &revokedAt, &replacedBy)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				outcome = RotateInvalid
				return nil
			}
			return err
		}

		now := time.Now()
		if now.After(expiresAt) {
			outcome = RotateInvalid
			return nil
		}

		if revokedAt != nil {
			graceOK := replacedBy != nil && now.Sub(*revokedAt) <= grace
			if graceOK {
				// The replacement must still be live. If a cascade killed it,
				// honoring grace on its predecessor would let a token survive
				// the very revocation it was caught by.
				var replacementLive bool
				if err := tx.QueryRow(ctx, `
					SELECT revoked_at IS NULL FROM refresh_tokens WHERE id = $1`,
					*replacedBy).Scan(&replacementLive); err != nil {
					return err
				}
				graceOK = replacementLive
			}
			if !graceOK {
				outcome = RotateReuse
				_, err := tx.Exec(ctx, `
					UPDATE refresh_tokens SET revoked_at = now()
					WHERE user_id = $1 AND revoked_at IS NULL`, userID)
				return err
			}
			outcome = RotateGrace
		} else {
			outcome = RotateOK
		}

		newID := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, user_agent, ip)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			newID, userID, newHash, newExpiry, userAgent, ip); err != nil {
			return err
		}

		// Only the first rotation records the replacement pointer; a grace-window
		// request leaves the original chain intact.
		if outcome == RotateOK {
			_, err = tx.Exec(ctx, `
				UPDATE refresh_tokens
				SET revoked_at = now(), replaced_by = $2
				WHERE id = $1 AND revoked_at IS NULL`, id, newID)
			return err
		}
		return nil
	})
	if err != nil {
		return RotateInvalid, uuid.Nil, err
	}
	return outcome, userID, nil
}

func (s *TokenStore) Revoke(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}
