package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cloud-ai/ai-aws-architect/internal/awsconnect"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/httperr"
	"github.com/cloud-ai/ai-aws-architect/internal/httpapi/middleware"
)

type ConnectionHandler struct{ svc *awsconnect.Service }

func NewConnectionHandler(svc *awsconnect.Service) *ConnectionHandler {
	return &ConnectionHandler{svc: svc}
}

type verifyConnectionRequest struct {
	RoleARN string `json:"role_arn" binding:"required,max=2048"`
}

// notConnected is a state, not an error, so the UI renders its empty state
// without special-casing a 404.
var notConnected = awsconnect.Health{Status: "not_connected"}

// Status is called on page load. It re-checks against AWS when the cached
// result is stale, because a stored "verified" flag proves nothing: the
// customer can delete the role at any time without telling us. Finding that out
// on page load is fine; finding out when Apply is pressed is not.
func (h *ConnectionHandler) Status(c *gin.Context) {
	conn, err := h.svc.Status(c.Request.Context(), middleware.MustUserID(c), boolQuery(c, "refresh"))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusOK, notConnected)
			return
		}
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, awsconnect.HealthOf(conn))
}

// Start returns the pre-filled CloudFormation launch link.
//
// Idempotent: calling it again reuses the same external ID and role name, so a
// user who abandons the flow and retries does not invalidate a role they may
// already have created.
func (h *ConnectionHandler) Start(c *gin.Context) {
	result, err := h.svc.Start(c.Request.Context(), middleware.MustUserID(c))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// Verify completes the connection using the role ARN from the stack outputs.
// The ARN is a claim from the user, so it is proven by an assume-role round
// trip rather than believed.
func (h *ConnectionHandler) Verify(c *gin.Context) {
	var req verifyConnectionRequest
	if !bindJSON(c, &req) {
		return
	}
	conn, err := h.svc.Verify(c.Request.Context(), middleware.MustUserID(c), req.RoleARN)
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, awsconnect.HealthOf(conn))
}

// Disconnect removes our stored pointer. It deliberately returns warnings
// rather than reporting a clean success: the IAM role survives in the customer's
// account until they delete the stack, and any live resources keep billing.
func (h *ConnectionHandler) Disconnect(c *gin.Context) {
	result, err := h.svc.Disconnect(c.Request.Context(), middleware.MustUserID(c))
	if err != nil {
		httperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
