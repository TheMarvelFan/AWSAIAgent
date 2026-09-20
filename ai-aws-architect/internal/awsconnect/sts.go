package awsconnect

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// STS wraps the two calls this package needs: assuming a customer role and
// asking AWS who we currently are.
type STS struct {
	cfg aws.Config
	api *sts.Client
}

func NewSTS(ctx context.Context, region, profile string) (*STS, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &STS{cfg: cfg, api: sts.NewFromConfig(cfg)}, nil
}

// Identity is the answer to GetCallerIdentity.
type Identity struct {
	AccountID string
	ARN       string
	UserID    string
}

// ServiceIdentity reports who WE are. Used at startup to confirm the configured
// service account ID matches the credentials actually in use, so the trust
// policy we hand customers names the right principal.
func (s *STS) ServiceIdentity(ctx context.Context) (*Identity, error) {
	out, err := s.api.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, err
	}
	return &Identity{
		AccountID: aws.ToString(out.Account),
		ARN:       aws.ToString(out.Arn),
		UserID:    aws.ToString(out.UserId),
	}, nil
}

// VerifyRole assumes the customer's role and asks AWS to confirm the identity.
//
// The supplied ARN is never trusted on its own: a user could paste anything.
// Only a successful assume plus a GetCallerIdentity that reports the expected
// account proves the role exists, that its trust policy names us, and that the
// external ID matches.
func (s *STS) VerifyRole(ctx context.Context, roleARN, externalID string) (*Identity, error) {
	creds := s.assumeProvider(roleARN, externalID)

	value, err := creds.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("assume role: %w", err)
	}

	scoped := s.cfg.Copy()
	scoped.Credentials = aws.NewCredentialsCache(&staticProvider{value: value})

	out, err := sts.NewFromConfig(scoped).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("verify identity: %w", err)
	}
	return &Identity{
		AccountID: aws.ToString(out.Account),
		ARN:       aws.ToString(out.Arn),
		UserID:    aws.ToString(out.UserId),
	}, nil
}

// CredentialsFor returns a self-refreshing provider for a customer's account.
//
// Note this is for OUR SDK calls. Terraform is handed the role ARN and external
// ID and assumes the role itself, because a subprocess receives its environment
// once at launch: injected credentials would expire mid-apply on any run longer
// than the session.
func (s *STS) CredentialsFor(roleARN, externalID string) aws.CredentialsProvider {
	return aws.NewCredentialsCache(s.assumeProvider(roleARN, externalID))
}

func (s *STS) assumeProvider(roleARN, externalID string) aws.CredentialsProvider {
	return stscreds.NewAssumeRoleProvider(s.api, roleARN, func(o *stscreds.AssumeRoleOptions) {
		o.ExternalID = aws.String(externalID)
		o.RoleSessionName = "ai-aws-architect"
		o.Duration = time.Hour
	})
}

// staticProvider hands back one already-retrieved credential set.
type staticProvider struct{ value aws.Credentials }

func (p *staticProvider) Retrieve(context.Context) (aws.Credentials, error) {
	return p.value, nil
}
