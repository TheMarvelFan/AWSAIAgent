package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

// Claims is the access-token payload. Access tokens are short-lived and never
// stored server-side; revocation happens on the refresh token.
type Claims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
	Type  string `json:"typ"`
}

const accessTokenType = "access"

type TokenIssuer struct {
	secret    []byte
	issuer    string
	accessTTL time.Duration
}

func NewTokenIssuer(secret []byte, issuer string, accessTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: secret, issuer: issuer, accessTTL: accessTTL}
}

func (t *TokenIssuer) IssueAccessToken(u *domain.User) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(t.accessTTL)
	claims := Claims{
		Subject:   u.ID.String(),
		Issuer:    t.issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
		ID:        uuid.NewString(),
		Email:     u.Email,
		Type:      accessTokenType,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// ParseAccessToken validates signature, expiry, issuer and algorithm. The
// algorithm check is what stops an "alg: none" forgery.
func (t *TokenIssuer) ParseAccessToken(raw string) (*Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", token.Header["alg"])
		}
		return t.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(t.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, domain.ErrTokenInvalid
	}
	if claims.Type != accessTokenType {
		return nil, domain.ErrTokenInvalid
	}
	return &claims, nil
}

// NewRefreshToken returns the opaque token handed to the client and the SHA-256
// digest that is all the database ever sees.
func NewRefreshToken() (raw string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}

func HashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
