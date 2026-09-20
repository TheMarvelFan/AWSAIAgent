// Package deployment owns the lifecycle between an approved configuration and
// real infrastructure: plan, human approval, apply, teardown.
//
// No model is involved anywhere in this package. The reasoning engine's job
// ended when the configuration was validated against the vetted catalog; from
// here it is deterministic code, a pre-written module, and a human decision.
// Putting an agent in this path would be the exact blast-radius problem the
// project exists to answer.
package deployment

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/awsconnect"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/runner"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

type Options struct {
	// TeardownAfter is how long applied resources live before an automatic
	// destroy job fires. Zero disables it.
	TeardownAfter time.Duration
	// MaxRunTime bounds a single Terraform run. On expiry the outcome is
	// unknown, not failed - the run may have partially taken effect.
	MaxRunTime time.Duration
}

type Service struct {
	deployments *store.DeploymentStore
	jobs        *store.JobStore
	chats       *store.ChatStore
	aws         *awsconnect.Service
	runner      runner.Runner
	opts        Options
}

func NewService(
	deployments *store.DeploymentStore,
	jobs *store.JobStore,
	chats *store.ChatStore,
	aws *awsconnect.Service,
	r runner.Runner,
	opts Options,
) *Service {
	if opts.MaxRunTime <= 0 {
		opts.MaxRunTime = 20 * time.Minute
	}
	return &Service{deployments: deployments, jobs: jobs, chats: chats,
		aws: aws, runner: r, opts: opts}
}

// Plan starts a deployment for a specific config version.
//
// Four things are checked before any work is queued, in order of how badly they
// would fail later: the version exists, it is applicable, the user has a
// verified AWS connection, and no other run is in flight for this chat.
func (s *Service) Plan(ctx context.Context, userID, chatID uuid.UUID, version int) (*domain.Deployment, *domain.Job, error) {
	chat, err := s.chats.GetChat(ctx, userID, chatID)
	if err != nil {
		return nil, nil, err
	}

	// An archived chat is frozen. Planning into one would provision
	// infrastructure for a conversation the user believes is put away, and the
	// scheduled teardown would then fire against a chat they never look at.
	if chat.ArchivedAt != nil {
		return nil, nil, fmt.Errorf("%w: this chat is archived; restore it before planning",
			domain.ErrConflict)
	}

	cv, err := s.chats.GetConfigVersion(ctx, chat.ID, version)
	if err != nil {
		return nil, nil, err
	}
	if !cv.Applicable {
		// The not-applicable state exists precisely so this is refused rather
		// than provisioned. A proposal can be shown without being buildable.
		return nil, nil, fmt.Errorf("%w: version %d is marked not applicable and cannot be provisioned",
			domain.ErrConflict, version)
	}

	// Re-verified here rather than trusting the cached connection status:
	// applying into an account we can no longer reach fails halfway through.
	target, err := s.aws.TargetFor(ctx, userID)
	if err != nil {
		return nil, nil, err
	}

	return s.deployments.Create(ctx, store.CreateDeploymentParams{
		ChatID:        chat.ID,
		UserID:        userID,
		ConfigVersion: version,
		AWSAccountID:  target.AccountID,
		AWSRoleARN:    target.RoleARN,
		Region:        target.Region,
		// One state per chat. This is what makes a later version a
		// modification of the existing infrastructure rather than a new build.
		StateKey: fmt.Sprintf("chats/%s/terraform.tfstate", chat.ID),
	})
}

// Approve is the gate. The caller must present the plan hash it was shown, so
// the thing approved and the thing applied are provably identical.
func (s *Service) Approve(ctx context.Context, userID, deploymentID uuid.UUID, planHash string) (*domain.Deployment, *domain.Job, error) {
	if _, err := s.deployments.Get(ctx, userID, deploymentID); err != nil {
		return nil, nil, err
	}
	return s.deployments.ApproveAndQueueApply(ctx, userID, deploymentID, planHash)
}

// Destroy queues a teardown. Allowed from applied, and also from apply_failed
// and unknown, where resources may exist even though the run did not finish.
func (s *Service) Destroy(ctx context.Context, userID, deploymentID uuid.UUID) (*domain.Deployment, *domain.Job, error) {
	dep, err := s.deployments.Get(ctx, userID, deploymentID)
	if err != nil {
		return nil, nil, err
	}
	if !domain.CanTransition(dep.Status, domain.DeployDestroying) {
		return nil, nil, domain.ErrIllegalTransition(dep.Status, domain.DeployDestroying)
	}

	updated, err := s.deployments.Transition(ctx, dep.ID, domain.DeployDestroying, store.TransitionParams{})
	if err != nil {
		return nil, nil, err
	}
	job, err := s.jobs.Schedule(ctx, domain.JobDestroy, dep.ID, dep.ChatID, time.Now())
	if err != nil {
		return nil, nil, err
	}
	return updated, job, nil
}

// Cancel abandons a deployment that has not created anything.
func (s *Service) Cancel(ctx context.Context, userID, deploymentID uuid.UUID) (*domain.Deployment, error) {
	dep, err := s.deployments.Get(ctx, userID, deploymentID)
	if err != nil {
		return nil, err
	}
	if dep.Status.MayHaveLiveResources() {
		return nil, fmt.Errorf("%w: this deployment may have live resources, destroy it instead of cancelling",
			domain.ErrConflict)
	}
	return s.deployments.Transition(ctx, dep.ID, domain.DeployCancelled, store.TransitionParams{})
}

// ResolveParams carries the human assertion that closes out a deployment whose
// contents the system cannot determine.
type ResolveParams struct {
	// Acknowledged must be true. The caller is asserting that they have looked
	// in AWS and nothing from this deployment remains. It is deliberately not
	// a default: the whole point of the unknown state is that the system will
	// not guess, so a human has to take responsibility explicitly.
	Acknowledged bool
	// Note records what they checked. Optional but strongly encouraged - it is
	// the only evidence anyone will have later.
	Note string
}

// Resolve closes a deployment whose outcome could not be determined.
//
// Exists because 'unknown' otherwise has no exit. Cancel refuses it (resources
// may exist), destroy may fail against infrastructure that is already gone, and
// a fresh plan produces a NEW deployment rather than settling the old one. The
// result was that unknown deployments accumulated forever in the live list and
// in the disconnect warning, describing infrastructure that no longer existed.
//
// This is not the same as "destroyed": nothing was torn down here, a person
// simply confirmed there was nothing to tear down. The distinction is recorded
// rather than smoothed over, because a wrong assertion leaves real resources
// billing and the record should say who made it.
func (s *Service) Resolve(ctx context.Context, userID, deploymentID uuid.UUID, p ResolveParams) (*domain.Deployment, error) {
	dep, err := s.deployments.Get(ctx, userID, deploymentID)
	if err != nil {
		return nil, err
	}

	if !p.Acknowledged {
		return nil, domain.NewValidationError("acknowledged",
			"confirm in AWS that nothing from this deployment remains, then send acknowledged=true")
	}

	// Deliberately not allowed from 'applied'. There the system knows resources
	// exist, and letting someone assert otherwise would turn a reliable record
	// into an unreliable one. Destroy it instead.
	switch dep.Status {
	case domain.DeployUnknown, domain.DeployApplyFailed, domain.DeployDestroyFailed:
	default:
		return nil, fmt.Errorf("%w: only a deployment whose contents are uncertain can be resolved, and this one is %s",
			domain.ErrConflict, dep.Status)
	}

	// A queued teardown for a deployment declared empty would run against the
	// chat's shared state and could destroy whatever is live there now.
	if n, err := s.jobs.CancelPending(ctx, dep.ID,
		"cancelled: the deployment was manually resolved as empty"); err != nil {
		return nil, err
	} else if n > 0 {
		slog.InfoContext(ctx, "cancelled queued jobs for a resolved deployment",
			"deployment_id", dep.ID, "jobs", n)
	}

	note := strings.TrimSpace(p.Note)
	msg := "Marked resolved by the account owner, who confirmed in AWS that nothing from this deployment remains. Nothing was torn down by this system."
	if note != "" {
		msg += " Note: " + note
	}

	slog.WarnContext(ctx, "deployment manually resolved without teardown",
		"deployment_id", dep.ID, "user_id", userID, "from", dep.Status, "note", note)

	return s.deployments.Transition(ctx, dep.ID, domain.DeployCancelled, store.TransitionParams{
		FailureCode:    "manually_resolved",
		FailureMessage: msg,
	})
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Deployment, error) {
	return s.deployments.Get(ctx, userID, id)
}

func (s *Service) Jobs(ctx context.Context, userID, deploymentID uuid.UUID) ([]domain.Job, error) {
	if _, err := s.deployments.Get(ctx, userID, deploymentID); err != nil {
		return nil, err
	}
	return s.jobs.ListForDeployment(ctx, deploymentID)
}

func (s *Service) ListForChat(ctx context.Context, userID, chatID uuid.UUID, limit int) ([]domain.Deployment, error) {
	if _, err := s.chats.GetChat(ctx, userID, chatID); err != nil {
		return nil, err
	}
	return s.deployments.ListForChat(ctx, userID, chatID, limit)
}

// Live lists deployments that may still be billing. The disconnect flow uses
// this to warn before removing the connection that teardown depends on.
func (s *Service) Live(ctx context.Context, userID uuid.UUID) ([]domain.Deployment, error) {
	return s.deployments.Live(ctx, userID)
}

// DeployedVersion answers "what is actually live", as distinct from
// chats.current_config_version, which is only what the panel proposes.
func (s *Service) DeployedVersion(ctx context.Context, chatID uuid.UUID) (*int, error) {
	return s.deployments.DeployedVersion(ctx, chatID)
}
