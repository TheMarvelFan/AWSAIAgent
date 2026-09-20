package middleware

import (
	"net/http"
	"strings"

	"github.com/cloud-ai/ai-aws-architect/internal/auth"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/httperr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	ContextUserID = "user_id"
	ContextEmail  = "user_email"
)

// RequireAuth validates the bearer access token and pins the user id into the
// context. Handlers read it via MustUserID and never trust an id from the path
// or body.
func RequireAuth(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			unauthorized(c, "missing bearer token")
			return
		}

		claims, err := svc.ParseAccessToken(strings.TrimSpace(token))
		if err != nil {
			unauthorized(c, "invalid or expired token")
			return
		}
		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			unauthorized(c, "invalid token subject")
			return
		}

		c.Set(ContextUserID, userID.String())
		c.Set(ContextEmail, claims.Email)
		c.Next()
	}
}

// MustUserID returns the authenticated user. Safe only behind RequireAuth.
func MustUserID(c *gin.Context) uuid.UUID {
	id, err := uuid.Parse(c.GetString(ContextUserID))
	if err != nil {
		return uuid.Nil
	}
	return id
}

func unauthorized(c *gin.Context, msg string) {
	httperr.Write(c, http.StatusUnauthorized, "unauthorized", msg)
}
