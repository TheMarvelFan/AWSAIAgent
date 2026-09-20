// Package httperr owns the API error envelope and the single place where Go
// errors become HTTP status codes.
//
// It lives in its own package rather than in httpapi because httpapi builds the
// handlers, so handlers cannot import it back. Keeping this a leaf package that
// both depend on breaks that cycle - and middleware can use it too, so every
// error response in the API has one shape.
package httperr

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

// ErrorBody is the single error envelope for the whole API, so the frontend has
// exactly one shape to handle.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type errorResponse struct {
	Error ErrorBody `json:"error"`
}

// WriteError is the only place status codes are chosen. Handlers return domain
// errors and let this map them.
func WriteError(c *gin.Context, err error) {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, errorResponse{ErrorBody{
			Code: "validation_failed", Message: "request failed validation", Fields: ve.Fields,
		}})
	case errors.Is(err, domain.ErrNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, errorResponse{ErrorBody{
			Code: "not_found", Message: "resource not found",
		}})
	case errors.Is(err, domain.ErrEmailTaken):
		c.AbortWithStatusJSON(http.StatusConflict, errorResponse{ErrorBody{
			Code: "email_taken", Message: "that email is already registered",
		}})
	case errors.Is(err, domain.ErrInvalidCredentials):
		c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{ErrorBody{
			Code: "invalid_credentials", Message: "invalid email or password",
		}})
	case errors.Is(err, domain.ErrTokenInvalid):
		c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse{ErrorBody{
			Code: "invalid_token", Message: "token is invalid or expired",
		}})
	case errors.Is(err, domain.ErrForbidden):
		c.AbortWithStatusJSON(http.StatusForbidden, errorResponse{ErrorBody{
			Code: "forbidden", Message: "not allowed",
		}})
	case errors.Is(err, domain.ErrStaleConfig):
		c.AbortWithStatusJSON(http.StatusConflict, errorResponse{ErrorBody{
			Code:    "stale_config",
			Message: "this no longer matches what is on the server - reload and try again",
		}})
	case errors.Is(err, domain.ErrNotConnected):
		c.AbortWithStatusJSON(http.StatusPreconditionRequired, errorResponse{ErrorBody{
			Code: "aws_not_connected", Message: "connect an AWS account first",
		}})
	case errors.Is(err, domain.ErrConflict):
		c.AbortWithStatusJSON(http.StatusConflict, errorResponse{ErrorBody{
			Code: "conflict", Message: "request conflicts with current state",
		}})
	case errors.Is(err, domain.ErrAWSUpstream):
		c.AbortWithStatusJSON(http.StatusBadGateway, errorResponse{ErrorBody{
			Code: "aws_call_failed", Message: "the call to AWS did not succeed",
		}})
	case errors.Is(err, domain.ErrUpstream):
		// The model call failed. Surfaced distinctly so the UI can offer a
		// retry rather than showing a generic failure.
		slog.Error("upstream failure", "err", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, errorResponse{ErrorBody{
			Code: "upstream_failed", Message: "the reasoning engine could not be reached, try again",
		}})
	default:
		slog.Error("unhandled error", "err", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, errorResponse{ErrorBody{
			Code: "internal_error", Message: "something went wrong",
		}})
	}
}

// Write emits the standard envelope directly, for the few places that pick
// their own status code (auth middleware, unknown routes) rather than mapping a
// domain error.
func Write(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, errorResponse{ErrorBody{Code: code, Message: message}})
}

// WriteValidation is shorthand for rejecting a bound request body.
func WriteValidation(c *gin.Context, field, msg string) {
	WriteError(c, domain.NewValidationError(field, msg))
}
