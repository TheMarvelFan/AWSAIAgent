// Package auth handles email/password signup and login with rotating refresh
// tokens. No OAuth: adding a provider later means adding an identity table and
// a new handler, not reworking this.
package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

type Service struct {
	users        *store.UserStore
	tokens       *store.TokenStore
	issuer       *TokenIssuer
	refreshTTL   time.Duration
	refreshGrace time.Duration
}

// DefaultRefreshGrace tolerates concurrent refreshes from multiple tabs without
// tripping theft detection.
const DefaultRefreshGrace = 30 * time.Second

func NewService(users *store.UserStore, tokens *store.TokenStore, issuer *TokenIssuer, refreshTTL time.Duration) *Service {
	return &Service{
		users: users, tokens: tokens, issuer: issuer,
		refreshTTL: refreshTTL, refreshGrace: DefaultRefreshGrace,
	}
}

type Session struct {
	User         *domain.User
	AccessToken  string
	ExpiresAt    time.Time
	RefreshToken string
}

// ClientInfo is recorded against a refresh token for auditability.
type ClientInfo struct {
	UserAgent string
	IP        string
}

const (
	minPasswordLen = 10
	maxPasswordLen = 128
)

func (s *Service) Signup(ctx context.Context, email, password, displayName string, client ClientInfo) (*Session, error) {
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user, err := s.users.Create(ctx, email, hash, displayName)
	if err != nil {
		return nil, err
	}
	return s.newSession(ctx, user, client)
}

func (s *Service) Login(ctx context.Context, email, password string, client ClientInfo) (*Session, error) {
	user, hash, err := s.users.GetCredentialsByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Same error and roughly the same work either way, so the response
			// does not reveal whether the address is registered.
			_, _ = HashPassword(password)
			return nil, domain.ErrInvalidCredentials
		}
		return nil, err
	}
	if err := VerifyPassword(password, hash); err != nil {
		return nil, domain.ErrInvalidCredentials
	}
	return s.newSession(ctx, user, client)
}

// Refresh rotates the token. Validation, replacement and theft detection all
// happen inside one database transaction (see store.ConsumeForRotation), so two
// simultaneous refreshes cannot both mint a session.
func (s *Service) Refresh(ctx context.Context, rawToken string, client ClientInfo) (*Session, error) {
	raw, newHash, err := NewRefreshToken()
	if err != nil {
		return nil, err
	}

	outcome, userID, err := s.tokens.ConsumeForRotation(
		ctx, HashRefreshToken(rawToken), newHash,
		time.Now().Add(s.refreshTTL), s.refreshGrace,
		client.UserAgent, client.IP,
	)
	if err != nil {
		return nil, err
	}
	switch outcome {
	case store.RotateOK, store.RotateGrace:
	default:
		return nil, domain.ErrTokenInvalid
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	access, expiresAt, err := s.issuer.IssueAccessToken(user)
	if err != nil {
		return nil, err
	}
	return &Session{User: user, AccessToken: access, ExpiresAt: expiresAt, RefreshToken: raw}, nil
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	rec, err := s.tokens.GetByHash(ctx, HashRefreshToken(rawToken))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil // idempotent
		}
		return err
	}
	return s.tokens.Revoke(ctx, rec.ID)
}

func (s *Service) UserByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	return s.users.GetByID(ctx, id)
}

func (s *Service) ParseAccessToken(raw string) (*Claims, error) {
	return s.issuer.ParseAccessToken(raw)
}

func (s *Service) newSession(ctx context.Context, user *domain.User, client ClientInfo) (*Session, error) {
	session, _, err := s.issueSession(ctx, user, client)
	return session, err
}

func (s *Service) issueSession(ctx context.Context, user *domain.User, client ClientInfo) (*Session, uuid.UUID, error) {
	access, expiresAt, err := s.issuer.IssueAccessToken(user)
	if err != nil {
		return nil, uuid.Nil, err
	}
	raw, hash, err := NewRefreshToken()
	if err != nil {
		return nil, uuid.Nil, err
	}
	id, err := s.tokens.Create(ctx, user.ID, hash, time.Now().Add(s.refreshTTL), client.UserAgent, client.IP)
	if err != nil {
		return nil, uuid.Nil, err
	}
	return &Session{User: user, AccessToken: access, ExpiresAt: expiresAt, RefreshToken: raw}, id, nil
}

func validatePassword(p string) error {
	switch {
	case len([]rune(p)) < minPasswordLen:
		return domain.NewValidationError("password", "must be at least 10 characters")
	case len(p) > maxPasswordLen:
		return domain.NewValidationError("password", "must be at most 128 characters")
	case strings.TrimSpace(p) == "":
		return domain.NewValidationError("password", "must not be blank")
	}
	return nil
}
