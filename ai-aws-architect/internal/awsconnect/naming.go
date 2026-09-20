// Package awsconnect owns the customer AWS account connection: generating the
// CloudFormation launch link, verifying the role the customer created, checking
// that it still works, and minting temporary credentials from it.
//
// The design rule that governs everything here: we never hold an AWS
// credential. We hold a role ARN (an address) and an external ID (a secret we
// generated). Access is something the customer grants in their own account and
// can revoke without telling us.
package awsconnect

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// RolePrefix is the fixed part of every provisioning role name.
const RolePrefix = "AIArchitectProvisioner"

// RoleNameFor derives a stable, unique-per-user IAM role name.
//
// Deterministic on purpose. If the customer deletes and re-runs the stack, the
// role comes back with the identical name and therefore the identical ARN, so
// the connection repairs itself with no action from us. Letting CloudFormation
// auto-generate the name would append a fresh random suffix each time and
// strand the ARN we stored.
//
// Keyed on user rather than chat: two colleagues can each connect the same
// company AWS account without colliding, while a user still connects only once.
func RoleNameFor(userID uuid.UUID) string {
	sum := sha256.Sum256([]byte("ai-aws-architect/role/" + userID.String()))
	return fmt.Sprintf("%s-%s", RolePrefix, hex.EncodeToString(sum[:6]))
}

// NewExternalID generates the shared secret that pins the trust policy to us.
//
// Without it, anyone who learns a customer's role ARN could ask our service to
// act in that account on their behalf - the confused deputy problem. Generated
// once at first connect and reused permanently: minting a fresh one on
// reconnect would not match the role the customer already created.
func NewExternalID() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// roleARNPattern is a shape check only. Real verification is an STS call.
var roleARNPattern = regexp.MustCompile(`^arn:aws[a-z-]*:iam::(\d{12}):role/(.+)$`)

// ParseRoleARN extracts the account ID and role name, rejecting anything that
// is not an IAM role ARN.
func ParseRoleARN(arn string) (accountID, roleName string, err error) {
	m := roleARNPattern.FindStringSubmatch(strings.TrimSpace(arn))
	if m == nil {
		return "", "", fmt.Errorf("not a valid IAM role ARN")
	}
	return m[1], m[2], nil
}

// LaunchURLParams are the values baked into the CloudFormation quick-create link.
type LaunchURLParams struct {
	// TemplateURL must be publicly readable; the customer's browser fetches it.
	TemplateURL string
	// Region only decides where the CloudFormation *stack* is recorded. IAM is
	// global, so the resulting role works everywhere regardless.
	Region string
	// ServiceAccountID and ServiceRoleName identify us in the trust policy:
	// the single identity on our side permitted to assume customer roles.
	ServiceAccountID string
	ServiceRoleName  string
	ExternalID       string
	RoleName         string
}

// LaunchURL builds a console link that opens the create-stack review screen in
// the customer's own account with every parameter pre-filled. They tick the IAM
// acknowledgement and click Create; they never see or write a policy document.
func LaunchURL(p LaunchURLParams) string {
	q := url.Values{}
	q.Set("templateURL", p.TemplateURL)
	q.Set("stackName", "ai-aws-architect-connect")
	q.Set("param_ServiceAccountId", p.ServiceAccountID)
	q.Set("param_ServiceRoleName", p.ServiceRoleName)
	q.Set("param_ExternalId", p.ExternalID)
	q.Set("param_RoleName", p.RoleName)

	return fmt.Sprintf(
		"https://%s.console.aws.amazon.com/cloudformation/home?region=%s#/stacks/quickcreate?%s",
		p.Region, p.Region, q.Encode(),
	)
}

// StackConsoleURL points at the stack list, for the "delete the stack to fully
// revoke" instruction on disconnect.
func StackConsoleURL(region string) string {
	return fmt.Sprintf(
		"https://%s.console.aws.amazon.com/cloudformation/home?region=%s#/stacks",
		region, region,
	)
}
