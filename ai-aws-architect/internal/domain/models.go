// Package domain holds the core types shared across the transport, service and
// storage layers. It deliberately imports nothing from those layers.
package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Role is the author of a chat message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleSystem is used for events the backend itself narrates into the
	// transcript, e.g. "reverted to v3".
	RoleSystem Role = "system"
)

// ConfigSource records why a configuration version exists.
type ConfigSource string

const (
	// SourceAssistant produced by the reasoning engine from a user turn.
	SourceAssistant ConfigSource = "assistant"
	// SourceRevert a copy of an earlier version, created by an explicit revert.
	SourceRevert ConfigSource = "revert"
	// SourceManual reserved for a future "edit the config directly" affordance.
	SourceManual ConfigSource = "manual"
)

type User struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

type Chat struct {
	ID    uuid.UUID `json:"id"`
	Title string    `json:"title"`
	// CurrentConfigVersion is nil until the first proposal lands, which is why
	// the right-hand panel stays empty for a brand-new chat.
	CurrentConfigVersion *int `json:"current_config_version"`
	// MonthlyBudgetUSD is a number the user stated, not a tier we inferred.
	// nil means they have not said.
	MonthlyBudgetUSD *float64   `json:"monthly_budget_usd"`
	MessageCount     int64      `json:"message_count"`
	LastMessageAt    *time.Time `json:"last_message_at"`
	ArchivedAt       *time.Time `json:"archived_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Message struct {
	ID      uuid.UUID `json:"id"`
	ChatID  uuid.UUID `json:"chat_id"`
	Seq     int64     `json:"seq"`
	Role    Role      `json:"role"`
	Content string    `json:"content"`
	// ConfigVersion links an assistant turn to the config version it produced,
	// so the UI can jump from a message to the exact diff it caused.
	ConfigVersion *int      `json:"config_version"`
	Model         string    `json:"model"`
	InputTokens   int       `json:"input_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	CreatedAt     time.Time `json:"created_at"`
}

// ConfigVersion is one immutable snapshot of a chat's architecture config.
// Versions are never mutated or deleted; a revert appends a new version whose
// document is a copy of an older one.
type ConfigVersion struct {
	ID                  uuid.UUID       `json:"id"`
	ChatID              uuid.UUID       `json:"chat_id"`
	Version             int             `json:"version"`
	ParentVersion       *int            `json:"parent_version"`
	RevertedFromVersion *int            `json:"reverted_from_version"`
	Source              ConfigSource    `json:"source"`
	Document            json.RawMessage `json:"document"`
	Summary             string          `json:"summary"`
	Rationale           string          `json:"rationale"`
	Changes             json.RawMessage `json:"changes"`
	Validation          json.RawMessage `json:"validation"`
	CreatedByMessageID  *uuid.UUID      `json:"created_by_message_id"`
	CreatedAt           time.Time       `json:"created_at"`
	// Applicable false means the UI shows this proposal but disables Apply.
	Applicable bool            `json:"applicable"`
	Blockers   json.RawMessage `json:"blockers"`
	Scope      string          `json:"scope"`
	// Confidence is the model's self-report, uncalibrated. Shown as a hint.
	//
	// A pointer because a manually edited version has no model behind it, and
	// therefore no confidence at all. Serializing 0 there would read as the
	// system rating a user's own edit at zero; null says "not stated", which is
	// the truth.
	Confidence *float64 `json:"confidence"`
}

// ConfigVersionMeta is the list-view projection: everything except the document
// itself, so the history sidebar stays cheap to load.
type ConfigVersionMeta struct {
	ID                  uuid.UUID    `json:"id"`
	Version             int          `json:"version"`
	ParentVersion       *int         `json:"parent_version"`
	RevertedFromVersion *int         `json:"reverted_from_version"`
	Source              ConfigSource `json:"source"`
	Summary             string       `json:"summary"`
	IsCurrent           bool         `json:"is_current"`
	Applicable          bool         `json:"applicable"`
	Scope               string       `json:"scope"`
	Confidence          *float64     `json:"confidence"`
	CreatedAt           time.Time    `json:"created_at"`
}

// AWSConnectionStatus drives the padlock indicator in the UI.
type AWSConnectionStatus string

const (
	// AWSPending the user started the connect flow but has not come back with
	// a verified role yet.
	AWSPending AWSConnectionStatus = "pending"
	AWSActive  AWSConnectionStatus = "active"
	// AWSBroken the role was deleted, its trust policy was edited, or an
	// organization policy is blocking the assumption. Distinct from "never
	// connected" because there may be live resources we can no longer reach.
	AWSBroken AWSConnectionStatus = "broken"
)

// AWSConnection is a user's single connected AWS account.
//
// ExternalID is a secret we generate and is deliberately NOT serialized: it is
// handed to the user exactly once, inside the CloudFormation launch URL.
type AWSConnection struct {
	ID            uuid.UUID           `json:"id"`
	UserID        uuid.UUID           `json:"-"`
	ExternalID    string              `json:"-"`
	RoleName      string              `json:"role_name"`
	RoleARN       *string             `json:"role_arn"`
	AccountID     *string             `json:"account_id"`
	Region        string              `json:"region"`
	Status        AWSConnectionStatus `json:"status"`
	LastError     string              `json:"last_error"`
	LastErrorCode string              `json:"last_error_code"`
	VerifiedAt    *time.Time          `json:"verified_at"`
	LastCheckedAt *time.Time          `json:"last_checked_at"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}
