package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors. The HTTP layer maps these to status codes in exactly one
// place (httpapi.WriteError), so handlers never hand-pick status codes.
var (
	ErrNotFound           = errors.New("not found")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrTokenInvalid       = errors.New("token is invalid or expired")
	ErrForbidden          = errors.New("forbidden")
	ErrConflict           = errors.New("conflict")
	// ErrNotConnected the user has no verified AWS account connection.
	ErrNotConnected = errors.New("no verified AWS account connection")
	// ErrStaleConfig the assistant reasoned against a config version that is
	// no longer current, because another turn landed in between. The caller
	// should resend rather than have a diff computed against the wrong base.
	ErrStaleConfig = errors.New("configuration changed since this turn started")
	ErrValidation  = errors.New("validation failed")
	ErrAWSUpstream = errors.New("aws call failed")
	ErrUpstream    = errors.New("upstream service failed")
)

// ValidationError carries per-field detail for 422 responses.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed on %d field(s)", len(e.Fields))
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

func NewValidationError(field, msg string) *ValidationError {
	return &ValidationError{Fields: map[string]string{field: msg}}
}
