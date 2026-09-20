package awsconnect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/awsfail"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

type Options struct {
	// ServiceAccountID and ServiceRoleName are the single identity on our side
	// that assumes customer roles. Everything in Section 3 of the design rests
	// on this principal, so it is configured explicitly rather than inferred.
	ServiceAccountID string
	ServiceRoleName  string
	TemplateURL      string
	// DefaultRegion is used for proposals made before a user has connected,
	// and as the connection's region once they do. This is a real choice: it
	// decides where the customer's resources live.
	DefaultRegion string
	// StackRegion is only where the CloudFormation stack RECORD lives. The
	// stack creates one IAM role and IAM is global, so the role works
	// everywhere regardless - but the customer must have this region enabled,
	// and newer AWS accounts are pinned to a single home region they chose at
	// signup. us-east-1 is the safe default because it can never be disabled.
	//
	// Deliberately separate from DefaultRegion: sharing one value meant a
	// customer in a region we had picked for provisioning could not open the
	// connect link at all.
	StackRegion string
	// AllowedRegions bounds where we will provision. Applied to the connection,
	// not to model output.
	AllowedRegions []string
	// CheckFreshness avoids an STS round trip on every page load.
	CheckFreshness time.Duration
}

type Service struct {
	store *store.ConnectionStore
	sts   *STS
	opts  Options
}

func NewService(cs *store.ConnectionStore, s *STS, opts Options) *Service {
	if opts.CheckFreshness <= 0 {
		opts.CheckFreshness = 60 * time.Second
	}
	if opts.DefaultRegion == "" {
		opts.DefaultRegion = "ap-south-1"
	}
	if opts.StackRegion == "" {
		opts.StackRegion = "us-east-1"
	}
	return &Service{store: cs, sts: s, opts: opts}
}

// StartResult is what the UI needs to walk the user through connecting.
type StartResult struct {
	Connection *domain.AWSConnection `json:"connection"`
	// LaunchURL opens the pre-filled CloudFormation review screen in the
	// customer's own console. It embeds the external ID, so it is a secret.
	LaunchURL string `json:"launch_url"`
	// ExpectedRoleARN lets the UI pre-validate the paste-back and warn on a
	// mismatch before an STS call.
	ExpectedRoleARN string `json:"expected_role_arn_suffix"`
}

// Start creates (or returns) the pending connection and builds the launch link.
// Safe to call repeatedly: the external ID and role name are generated once and
// reused, because the role the customer already created still carries them.
func (s *Service) Start(ctx context.Context, userID uuid.UUID) (*StartResult, error) {
	if s.opts.ServiceAccountID == "" {
		return nil, fmt.Errorf("AWS_SERVICE_ACCOUNT_ID is not configured")
	}

	externalID, err := NewExternalID()
	if err != nil {
		return nil, err
	}
	roleName := RoleNameFor(userID)

	conn, err := s.store.EnsurePending(ctx, userID, externalID, roleName, s.opts.DefaultRegion)
	if err != nil {
		return nil, err
	}

	return &StartResult{
		Connection: conn,
		LaunchURL: LaunchURL(LaunchURLParams{
			TemplateURL:      s.opts.TemplateURL,
			Region:           s.opts.StackRegion,
			ServiceAccountID: s.opts.ServiceAccountID,
			ServiceRoleName:  s.opts.ServiceRoleName,
			ExternalID:       conn.ExternalID,
			RoleName:         conn.RoleName,
		}),
		ExpectedRoleARN: ":role/" + conn.RoleName,
	}, nil
}

// Verify completes the connect flow. The role ARN comes from the user (pasted
// from the stack outputs), so it is treated as a claim to be checked, not a
// fact: only a successful assume-plus-identity round trip marks the connection
// active.
func (s *Service) Verify(ctx context.Context, userID uuid.UUID, roleARN string) (*domain.AWSConnection, error) {
	conn, err := s.store.GetByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	claimedAccount, roleName, err := ParseRoleARN(roleARN)
	if err != nil {
		return nil, domain.NewValidationError("role_arn", err.Error())
	}
	if roleName != conn.RoleName {
		return nil, domain.NewValidationError("role_arn", fmt.Sprintf(
			"expected the role created by the stack (%s), got %s", conn.RoleName, roleName))
	}

	identity, err := s.sts.VerifyRole(ctx, roleARN, conn.ExternalID)
	if err != nil {
		f := awsfail.Classify(err)
		_ = s.store.MarkBroken(ctx, userID, string(f.Code), f.Message)
		return nil, fmt.Errorf("%w: %v", domain.ErrUpstream, err)
	}
	// Guards against a role ARN that assumes successfully but belongs to a
	// different account than the ARN claims.
	if identity.AccountID != claimedAccount {
		return nil, domain.NewValidationError("role_arn",
			"the assumed role belongs to a different account than the ARN states")
	}

	return s.store.MarkActive(ctx, userID, strings.TrimSpace(roleARN), identity.AccountID)
}

// Status returns the connection for the padlock indicator, re-checking against
// AWS when the last check is stale or when the caller forces it.
//
// Returns domain.ErrNotFound when the user has never connected; the handler
// turns that into the "not connected" state rather than an error.
func (s *Service) Status(ctx context.Context, userID uuid.UUID, force bool) (*domain.AWSConnection, error) {
	conn, err := s.store.GetByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if conn.Status == domain.AWSPending || conn.RoleARN == nil {
		return conn, nil
	}
	if !force && store.Fresh(conn, s.opts.CheckFreshness) {
		return conn, nil
	}
	return s.Check(ctx, conn)
}

// Check performs the live health check. A stored "verified" flag from three
// weeks ago proves nothing: the customer can delete the role at any time
// without telling us. Failure is a state, not an error - the padlock turns red
// and the user is offered the launch link again.
func (s *Service) Check(ctx context.Context, conn *domain.AWSConnection) (*domain.AWSConnection, error) {
	if conn.RoleARN == nil {
		return conn, nil
	}
	if _, err := s.sts.VerifyRole(ctx, *conn.RoleARN, conn.ExternalID); err != nil {
		f := awsfail.Classify(err)
		// The raw error goes to logs only: AWS error strings can echo request
		// parameters back.
		slog.WarnContext(ctx, "aws connection health check failed",
			"user_id", conn.UserID, "code", f.Code, "aws_side", f.AWSSide, "err", err)
		if mErr := s.store.MarkBroken(ctx, conn.UserID, string(f.Code), f.Message); mErr != nil {
			return nil, mErr
		}
		return s.store.GetByUser(ctx, conn.UserID)
	}
	if err := s.store.TouchChecked(ctx, conn.UserID); err != nil {
		return nil, err
	}
	return s.store.GetByUser(ctx, conn.UserID)
}

// DisconnectResult carries the honesty payload described in the design: our
// row is gone, but the role is still sitting in their account until the stack
// is deleted. Reporting "disconnected" without saying so would be misleading in
// the one direction we cannot afford.
type DisconnectResult struct {
	StackConsoleURL string   `json:"stack_console_url"`
	Warnings        []string `json:"warnings"`
}

func (s *Service) Disconnect(ctx context.Context, userID uuid.UUID) (*DisconnectResult, error) {
	if _, err := s.store.GetByUser(ctx, userID); err != nil {
		return nil, err
	}
	if err := s.store.Delete(ctx, userID); err != nil {
		return nil, err
	}
	return &DisconnectResult{
		// The stack region, not the provisioning region: this link points at
		// where the stack record lives.
		StackConsoleURL: StackConsoleURL(s.opts.StackRegion),
		Warnings: []string{
			"This removed our stored pointer to your account. The IAM role still exists in your AWS account and will keep granting access until you delete the CloudFormation stack.",
			"Any resources already provisioned are still running and still billing. Auto-teardown can no longer reach them.",
		},
	}, nil
}

// RegionForUser resolves the provisioning region.
//
// This is why region lives on the connection: the target account is resolved
// from the authenticated session and never from anything the model produced,
// and region must follow the same rule or the principle is only half applied.
// Users who have not connected yet still get proposals, in the default region.
func (s *Service) RegionForUser(ctx context.Context, userID uuid.UUID) (string, error) {
	conn, err := s.store.GetByUser(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return s.opts.DefaultRegion, nil
		}
		return "", err
	}
	return conn.Region, nil
}

// ProvisioningTarget is everything the (future) Terraform runner needs. It
// deliberately contains no credentials: the runner passes these to Terraform's
// AWS provider, which assumes the role itself.
type ProvisioningTarget struct {
	RoleARN    string
	ExternalID string
	AccountID  string
	Region     string
}

// TargetFor returns the assume-role details for an apply, refusing unless the
// connection is verified and healthy.
func (s *Service) TargetFor(ctx context.Context, userID uuid.UUID) (*ProvisioningTarget, error) {
	conn, err := s.store.GetByUser(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrNotConnected
		}
		return nil, err
	}
	if conn.Status != domain.AWSActive || conn.RoleARN == nil || conn.AccountID == nil {
		return nil, domain.ErrNotConnected
	}
	// Re-verify immediately before provisioning rather than trusting the cached
	// status: applying into an account we can no longer reach fails halfway.
	if _, err := s.sts.VerifyRole(ctx, *conn.RoleARN, conn.ExternalID); err != nil {
		f := awsfail.Classify(err)
		_ = s.store.MarkBroken(ctx, userID, string(f.Code), f.Message)
		return nil, domain.ErrNotConnected
	}
	return &ProvisioningTarget{
		RoleARN:    *conn.RoleARN,
		ExternalID: conn.ExternalID,
		AccountID:  *conn.AccountID,
		Region:     conn.Region,
	}, nil
}

// Health is the classified connection state handed to the UI.
type Health struct {
	Connection *domain.AWSConnection `json:"connection,omitempty"`
	Status     string                `json:"status"`
	// Failure is present when the last check failed. It tells the UI whether to
	// show "check your AWS account" or "AWS itself looks unhealthy" - two
	// different messages with two different next steps.
	Failure *awsfail.Failure `json:"failure,omitempty"`
}

// HealthOf projects a stored connection into the shape the UI renders.
func HealthOf(conn *domain.AWSConnection) Health {
	h := Health{Connection: conn, Status: string(conn.Status)}
	if conn.Status == domain.AWSBroken && conn.LastErrorCode != "" {
		code := awsfail.Code(conn.LastErrorCode)
		h.Failure = &awsfail.Failure{
			Code:    code,
			Message: conn.LastError,
			// Recomputed from the code rather than stored, so changing the
			// classification rules does not require a data migration.
			Retryable:     awsfail.Retryable(code),
			AWSSide:       awsfail.AWSSide(code),
			Indeterminate: awsfail.Indeterminate(code),
		}
	}
	return h
}
