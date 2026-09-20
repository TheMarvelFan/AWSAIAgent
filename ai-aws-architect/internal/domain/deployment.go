package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// DeploymentStatus is where a deployment sits in its lifecycle.
type DeploymentStatus string

const (
	DeployPlanning         DeploymentStatus = "planning"
	DeployAwaitingApproval DeploymentStatus = "awaiting_approval"
	DeployApplying         DeploymentStatus = "applying"
	DeployApplied          DeploymentStatus = "applied"
	DeploySuperseded       DeploymentStatus = "superseded"
	DeployPlanFailed       DeploymentStatus = "plan_failed"
	DeployApplyFailed      DeploymentStatus = "apply_failed"
	DeployDestroying       DeploymentStatus = "destroying"
	DeployDestroyed        DeploymentStatus = "destroyed"
	DeployDestroyFailed    DeploymentStatus = "destroy_failed"
	DeployCancelled        DeploymentStatus = "cancelled"

	// DeployUnknown we do not know what exists in AWS. Reached from a timeout,
	// an indeterminate AWS error, or a worker that died mid-run.
	//
	// Deliberately not resolvable by guessing. The only exit is a fresh plan,
	// which asks AWS what is actually there. Reporting "failed" here would be a
	// lie in the dangerous direction: it implies nothing was created.
	DeployUnknown DeploymentStatus = "unknown"
)

// allowedTransitions is the state machine, enforced in code rather than left to
// each call site to remember.
var allowedTransitions = map[DeploymentStatus][]DeploymentStatus{
	DeployPlanning:         {DeployAwaitingApproval, DeployPlanFailed, DeployCancelled, DeployUnknown},
	DeployAwaitingApproval: {DeployApplying, DeployCancelled},
	DeployApplying:         {DeployApplied, DeployApplyFailed, DeployUnknown},
	DeployApplied:          {DeployDestroying, DeploySuperseded, DeployUnknown},
	// A failed apply may have created resources, so destroying is a legal and
	// often necessary next step.
	DeployApplyFailed: {DeployDestroying, DeployUnknown, DeployCancelled},
	DeployDestroying:  {DeployDestroyed, DeployDestroyFailed, DeployUnknown},
	// Canceled is reachable here and from unknown only via an explicit human
	// assertion that nothing remains - see deployment.Service.Resolve. The
	// transition is legal in the machine; the service is what demands evidence.
	DeployDestroyFailed: {DeployDestroying, DeployUnknown, DeployCancelled},
	// Nothing is assumed about an unknown deployment. Destroy is allowed
	// (best-effort cleanup) and so is giving up on it explicitly.
	DeployUnknown:    {DeployDestroying, DeployCancelled},
	DeployPlanFailed: {DeployCancelled},
	// destroyed, canceled, superseded have no entry: nothing follows them.
}

// CanTransition reports whether a status change is legal.
func CanTransition(from, to DeploymentStatus) bool {
	return slices.Contains(allowedTransitions[from], to)
}

// ErrIllegalTransition is returned rather than silently writing a bad state.
func ErrIllegalTransition(from, to DeploymentStatus) error {
	return fmt.Errorf("%w: cannot move a deployment from %s to %s", ErrConflict, from, to)
}

// InFlight statuses hold the per-chat lock: one at a time, because they all
// imply a Terraform run against the chat's single state file.
func (s DeploymentStatus) InFlight() bool {
	switch s {
	case DeployPlanning, DeployAwaitingApproval, DeployApplying, DeployDestroying:
		return true
	}
	return false
}

// MayHaveLiveResources reports whether AWS might currently be billing for this
// deployment. Drives the disconnect warning and the teardown scheduler.
//
// Errs towards yes: apply_failed and unknown both mean "possibly", and treating
// a possible as a no is how resources get orphaned.
func (s DeploymentStatus) MayHaveLiveResources() bool {
	switch s {
	case DeployApplied, DeployApplyFailed, DeployDestroyFailed, DeployUnknown, DeployDestroying:
		return true
	}
	return false
}

// Terminal reports whether nothing further will happen without a user action.
func (s DeploymentStatus) Terminal() bool {
	switch s {
	case DeployDestroyed, DeployCancelled, DeployPlanFailed, DeploySuperseded:
		return true
	}
	return false
}

type Deployment struct {
	ID     uuid.UUID `json:"id"`
	ChatID uuid.UUID `json:"chat_id"`
	// UserID is how the worker resolves the AWS connection: it runs with no
	// request context, so the owner has to travel on the row.
	UserID        uuid.UUID        `json:"-"`
	ConfigVersion int              `json:"config_version"`
	Status        DeploymentStatus `json:"status"`

	// PlanHash binds an approval to one specific plan. Apply refuses unless the
	// caller presents the hash it was shown, so nothing can change between the
	// human reading a plan and that plan being executed.
	PlanHash    string          `json:"plan_hash"`
	PlanOutput  string          `json:"plan_output"`
	PlanSummary json.RawMessage `json:"plan_summary"`
	ApprovedAt  *time.Time      `json:"approved_at"`

	AWSAccountID string `json:"aws_account_id"`
	Region       string `json:"region"`
	StateKey     string `json:"-"`

	FailureCode    string `json:"failure_code"`
	FailureMessage string `json:"failure_message"`

	TeardownAfter *time.Time `json:"teardown_after"`
	AppliedAt     *time.Time `json:"applied_at"`
	DestroyedAt   *time.Time `json:"destroyed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	// MayHaveLiveResources is computed, not stored, so a client never has to
	// know the status enum to answer "is this costing money".
	LiveResources bool `json:"may_have_live_resources"`
}

// JobType is the unit of work. One job is one whole terraform run.
type JobType string

const (
	JobPlan    JobType = "plan"
	JobApply   JobType = "apply"
	JobDestroy JobType = "destroy"
)

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	// JobUnknown mirrors DeployUnknown: the run may have partially taken
	// effect, so neither success nor failure can be claimed.
	JobUnknown JobStatus = "unknown"
)

type Job struct {
	ID           uuid.UUID `json:"id"`
	Type         JobType   `json:"type"`
	DeploymentID uuid.UUID `json:"deployment_id"`
	ChatID       uuid.UUID `json:"chat_id"`
	Status       JobStatus `json:"status"`

	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	RunAfter    time.Time  `json:"run_after"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`

	FailureCode string    `json:"failure_code"`
	LastError   string    `json:"last_error"`
	CreatedAt   time.Time `json:"created_at"`
}
