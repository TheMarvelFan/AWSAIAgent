package handlers

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

const timeFormat = time.RFC3339

// chatIDParam parses and validates :chatID once, so no handler ever passes a
// raw string into the store.
func chatIDParam(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("chatID"))
	if err != nil {
		return uuid.Nil, domain.NewValidationError("chatID", "must be a UUID")
	}
	return id, nil
}

func versionParam(c *gin.Context, name string) (int, error) {
	v, err := strconv.Atoi(c.Param(name))
	if err != nil || v < 1 {
		return 0, domain.NewValidationError(name, "must be a positive integer")
	}
	return v, nil
}

func intQuery(c *gin.Context, key string, def, min, max int) int {
	raw := c.Query(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func int64Query(c *gin.Context, key string, def int64) int64 {
	raw := c.Query(key)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return v
}

func boolQuery(c *gin.Context, key string) bool {
	v, err := strconv.ParseBool(c.Query(key))
	return err == nil && v
}
