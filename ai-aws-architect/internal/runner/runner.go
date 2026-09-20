// Package runner is the seam between the deployment state machine and whatever
// actually talks to AWS.
//
// Terraform lives behind this interface for the same reason Bedrock lives
// behind agent.LLM: the state machine, the job queue, the approval binding and
// the failure handling can all be built and tested without Terraform installed,
// without AWS credentials, and without spending credits.
//
// Note what is NOT in this interface: nothing takes a model, a prompt, or free
// text. By the time work reaches here the configuration is already validated
// against the vetted catalog and approved by a human. The runner selects
// pre-written modules and passes validated parameters; it never authors
// infrastructure code.
package runner

import (
	"context"
	"encoding/json"
	"time"
)

// Target is where the work happens. It carries a role to assume rather than
// credentials: Terraform's AWS provider assumes the role itself, because a
// subprocess receives its environment once at launch and injected credentials
// would expire mid-run on anything longer than the session.
type Target struct {
	RoleARN    string
	ExternalID string
	AccountID  string
	Region     string
}

// Request is one whole Terraform run.
type Request struct {
	DeploymentID string
	ChatID       string
	// Config is the validated architecture document. The runner maps
	// template_id to a pre-written module and the parameters to variables.
	Config json.RawMessage
	Target Target
	// StateKey identifies the Terraform state. One per chat, which is what
	// makes applying version 5 over version 4 a modification rather than a
	// fresh build.
	StateKey string
	// Tags go on every created resource: user, chat, config version, and a
	// ManagedBy marker. They are what make an orphaned resource findable if
	// state and reality ever diverge.
	Tags map[string]string
}

// PlanResult is what the human approves.
type PlanResult struct {
	// Hash identifies this exact plan. The approval is bound to it, so nothing
	// can change between a human reading a plan and that plan being applied.
	Hash string
	// Output is the human-readable plan text shown in the approval gate.
	Output string
	// Summary is the structured counts the UI renders.
	Summary PlanSummary
}

// PlanSummary counts what a plan would do.
//
// Replace is separated from Add and Change deliberately. Terraform's -/+ means
// destroy-then-create: an S3 bucket rename is a new bucket and the old contents
// are gone. "This will delete your data" deserves louder treatment in the
// approval gate than "this will change a CPU setting".
type PlanSummary struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Replace int `json:"replace"`
	Destroy int `json:"destroy"`
	// DestructiveResources names what will be destroyed or replaced, so the
	// approval gate can list them rather than showing a bare count.
	DestructiveResources []string `json:"destructive_resources"`
}

// IsDestructive reports whether this plan removes or recreates anything, which
// the approval gate should surface far more loudly than an ordinary change.
func (s PlanSummary) IsDestructive() bool { return s.Replace > 0 || s.Destroy > 0 }

// ApplyResult reports what was created.
type ApplyResult struct {
	// Outputs are Terraform outputs, e.g. a CloudFront domain name.
	Outputs map[string]string
	// ResourceCount is how many resources the state now tracks.
	ResourceCount int
}

type DestroyResult struct {
	Destroyed int
}

// Runner executes infrastructure work. Implementations must be safe to cancel
// via context, and must not assume they are the only process on the machine.
type Runner interface {
	Plan(ctx context.Context, req Request) (*PlanResult, error)
	Apply(ctx context.Context, req Request) (*ApplyResult, error)
	Destroy(ctx context.Context, req Request) (*DestroyResult, error)
	// Name identifies the implementation in logs, so a demo can never silently
	// be running against the stub.
	Name() string

	// Provisionable reports whether this runner can actually build a catalog
	// template.
	//
	// It is a property of the RUNNER, not of the catalog. The stub simulates
	// anything, so everything is provisionable under it; the Terraform runner
	// is limited to the modules bundled with the binary. A template that is
	// vetted but has no module can be proposed, validated and approved, and
	// then fails at plan time - after the user has committed. Exposing this
	// lets a client warn before that point instead of after.
	Provisionable(templateID string) bool

	// Cleanup releases any working directory held for a deployment.
	//
	// Called once the workspace is provably no longer needed. It must NOT be
	// called while a deployment is awaiting approval: that workspace holds the
	// saved plan file, and apply runs that exact file.
	Cleanup(ctx context.Context, deploymentID string) error

	// Sweep removes workspaces whose deployment is not in keep and which have
	// not been touched for olderThan. Returns how many it removed.
	//
	// A backstop for the cases Cleanup cannot cover: a process killed
	// mid-deployment, or a status transition that failed to write. Without it
	// a workspace leaks permanently, and each one holds a full copy of the
	// provider - hundreds of megabytes apiece.
	Sweep(ctx context.Context, keep []string, olderThan time.Duration) (int, error)
}
