// Package awsfail turns an AWS SDK error into a classification the rest of the
// system can act on and a user can read.
//
// The point is to never collapse everything into one generic failure. Retrying
// a quota error is pointless, retrying a service outage is correct, and an
// access denial means the customer changed something on their side. Same
// mechanics, entirely different message and entirely different next step.
//
// It also exists so the honest-reporting rule has somewhere to live: when we
// cannot tell what happened, we say so rather than guessing. CodeUnknown is a
// real answer, not a fallback to hide behind.
package awsfail

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/smithy-go"
)

type Code string

const (
	// CodeAccessDenied the assume-role or an API call was refused. Usually the
	// customer edited the trust policy, narrowed the permission policy, or an
	// organization-level policy is blocking it.
	CodeAccessDenied Code = "access_denied"
	// CodeRoleMissing the IAM role is gone - the CloudFormation stack was
	// almost certainly deleted, which is the supported way to revoke access.
	CodeRoleMissing Code = "role_missing"
	// CodeQuotaExceeded an account limit was hit. Retrying will not help.
	CodeQuotaExceeded Code = "quota_exceeded"
	// CodeThrottled rate limited. Retrying after a backoff will help.
	CodeThrottled Code = "throttled"
	// CodeServiceUnavailable AWS itself is failing. Retrying later will help.
	CodeServiceUnavailable Code = "service_unavailable"
	// CodeTimeout no answer in time. Dangerous, because the call may have
	// SUCCEEDED at AWS with the response lost in transit.
	CodeTimeout Code = "timeout"
	// CodeOurCredentials our own service credentials were rejected. Nothing to
	// do with the customer and the message should say so.
	CodeOurCredentials Code = "our_credentials"
	// CodeInvalidRequest AWS rejected the request as malformed or invalid.
	CodeInvalidRequest Code = "invalid_request"
	// CodeAccountProblem the account itself is the problem rather than
	// permissions - suspended for non-payment, closed, or a service not
	// enabled. Distinguished from CodeAccessDenied because the remedy is
	// completely different: nobody should be inspecting a trust policy when
	// the real issue is an unpaid bill.
	CodeAccountProblem Code = "account_problem"
	// CodeRegionUnavailable the region is disabled for this account, or the
	// service does not exist there.
	CodeRegionUnavailable Code = "region_unavailable"
	// CodeUnknown genuinely unclassified. Never presented as success.
	CodeUnknown Code = "unknown"
)

// Failure is the classified result. It is safe to return to a client: the raw
// AWS error is deliberately not included, because AWS error strings can echo
// request parameters back.
type Failure struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	// Retryable reports whether waiting and trying again could plausibly work.
	Retryable bool `json:"retryable"`
	// AWSSide distinguishes "AWS is having a problem" from "your configuration
	// is wrong". Drives whether the UI shows the service-status warning.
	AWSSide bool `json:"aws_side"`
	// Indeterminate marks failures where the operation may have partially or
	// wholly succeeded despite the error. Callers must NOT report success or
	// failure of the underlying work - only that the outcome is unknown.
	Indeterminate bool `json:"indeterminate"`
}

// Classifiable lets an error expose only the text worth matching against.
//
// Substring classification is fine for an SDK error, whose message is only the
// error. It is actively wrong for anything that wraps a large body of output:
// Terraform prints resource attributes, and a Lambda with `timeout = 15` or a
// bucket named "throttle-test" reads exactly like a real timeout or throttle.
// An error carrying command output should implement this and return just its
// diagnostic lines.
type Classifiable interface {
	error
	ClassifiableText() string
}

// Classify inspects an AWS SDK error.
//
// It prefers the structured smithy API error code over string matching;
// substring checks are a fallback for transport-level errors that never reach
// an API response.
func Classify(err error) Failure {
	if err == nil {
		return Failure{Code: CodeUnknown, Message: "no error"}
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return timeoutFailure()
	}

	// Checked before the message fallback so a wrapper can keep its own output
	// out of the match.
	if narrowed, ok := errors.AsType[Classifiable](err); ok {
		if text := strings.TrimSpace(narrowed.ClassifiableText()); text != "" {
			return byMessage(text)
		}
	}

	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		if f, ok := byAPICode(apiErr.ErrorCode()); ok {
			return f
		}
	}
	return byMessage(err.Error())
}

func byAPICode(code string) (Failure, bool) {
	switch code {
	case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation",
		"NotAuthorized":
		// Deliberately does not assert a single cause. The old wording named
		// the trust policy, which sent people to inspect a perfectly healthy
		// policy when the real problem was elsewhere.
		return Failure{
			Code:      CodeAccessDenied,
			Message:   "AWS refused the request. This is usually a change to the role's permissions, but an organisation policy or a problem with the account itself can produce the same refusal.",
			Retryable: false,
		}, true

	case "OptInRequired", "SubscriptionRequiredException", "AccountProblem",
		"AuthFailure", "InvalidAccessKeyId", "AccountSuspended",
		"SuspendedAccountException", "AccessDeniedForAccount":
		// Account suspension has no single error code across AWS services, so
		// this list is a best-effort rather than a guarantee. Anything missed
		// lands in CodeUnknown, which is indeterminate and never reports
		// success.
		return Failure{
			Code:      CodeAccountProblem,
			Message:   "AWS reported a problem with the account rather than with permissions. This usually means billing, a suspended account, or a service that has not been enabled. Check the billing and account pages in the AWS console.",
			Retryable: false,
		}, true

	case "UnrecognizedClientException", "InvalidClientTokenId.Region",
		"EndpointConnectionError", "UnsupportedOperation", "InvalidRegion":
		return Failure{
			Code:      CodeRegionUnavailable,
			Message:   "The region appears to be disabled for this account, or the service is not available there.",
			Retryable: false,
		}, true

	case "NoSuchEntity", "NoSuchEntityException", "RoleNotFound", "ValidationError":
		// ValidationError from STS is usually "role does not exist".
		return Failure{
			Code:      CodeRoleMissing,
			Message:   "The IAM role could not be found. The CloudFormation stack was most likely deleted, which removes access.",
			Retryable: false,
		}, true

	case "ExpiredToken", "ExpiredTokenException", "InvalidClientTokenId",
		"SignatureDoesNotMatch", "IncompleteSignature":
		return Failure{
			Code:      CodeOurCredentials,
			Message:   "Our own AWS credentials were rejected. This is a problem on our side, not with your account.",
			Retryable: false,
		}, true

	case "LimitExceeded", "LimitExceededException", "QuotaExceeded",
		"ServiceQuotaExceededException", "ResourceLimitExceeded":
		return Failure{
			Code:      CodeQuotaExceeded,
			Message:   "An AWS account limit was reached. Retrying will not help until the limit is raised or existing resources are removed.",
			Retryable: false,
		}, true

	case "Throttling", "ThrottlingException", "TooManyRequestsException",
		"RequestLimitExceeded", "SlowDown":
		return Failure{
			Code:      CodeThrottled,
			Message:   "AWS is rate limiting these requests. This usually clears on its own shortly.",
			Retryable: true,
			AWSSide:   true,
		}, true

	case "ServiceUnavailable", "ServiceUnavailableException", "InternalError",
		"InternalFailure", "InternalServerError", "ServiceFailure":
		return Failure{
			Code:      CodeServiceUnavailable,
			Message:   "AWS reported a service-side error. This is an AWS problem rather than a configuration problem - it is worth checking the AWS health dashboard.",
			Retryable: true,
			AWSSide:   true,
			// An internal error can arrive after the work was already done.
			Indeterminate: true,
		}, true

	case "RequestTimeout", "RequestTimeoutException", "RequestExpired":
		return timeoutFailure(), true

	case "InvalidParameterValue", "ValidationException", "InvalidRequest",
		"MalformedPolicyDocument":
		return Failure{
			Code:      CodeInvalidRequest,
			Message:   "AWS rejected the request as invalid.",
			Retryable: false,
		}, true
	}
	return Failure{}, false
}

func byMessage(msg string) Failure {
	lower := strings.ToLower(msg)
	switch {
	case contains(lower, "suspended", "account is not active", "optinrequired",
		"subscriptionrequired", "account problem", "past due", "delinquent"):
		f, _ := byAPICode("OptInRequired")
		return f
	case contains(lower, "accessdenied", "not authorized", "explicit deny"):
		f, _ := byAPICode("AccessDenied")
		return f
	case contains(lower, "region is disabled", "not available in", "unsupportedoperation"):
		f, _ := byAPICode("UnsupportedOperation")
		return f
	case contains(lower, "nosuchentity", "cannot be found", "does not exist"):
		f, _ := byAPICode("NoSuchEntity")
		return f
	case contains(lower, "expiredtoken", "invalidclienttokenid"):
		f, _ := byAPICode("ExpiredToken")
		return f
	case contains(lower, "throttl", "rate exceeded", "too many requests"):
		f, _ := byAPICode("Throttling")
		return f
	case contains(lower, "deadline exceeded", "timeout", "timed out",
		"connection reset", "no such host", "eof"):
		return timeoutFailure()
	case contains(lower, "serviceunavailable", "internal error", "503", "500"):
		f, _ := byAPICode("ServiceUnavailable")
		return f
	default:
		return Failure{
			Code:          CodeUnknown,
			Message:       "The request to AWS failed for a reason we could not identify.",
			Retryable:     true,
			Indeterminate: true,
		}
	}
}

// timeoutFailure is always indeterminate: a request that timed out may have
// succeeded at AWS with the response lost on the way back. That is the case
// that creates untracked resources, so it must never be reported as a clean
// failure.
func timeoutFailure() Failure {
	return Failure{
		Code:          CodeTimeout,
		Message:       "The request to AWS timed out. It may or may not have taken effect, so the result is unknown.",
		Retryable:     true,
		AWSSide:       true,
		Indeterminate: true,
	}
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Retryable , AWSSide and Indeterminate answer the same questions as the fields
// on Failure, for a code that has been round-tripped through storage.
func Retryable(c Code) bool {
	switch c {
	case CodeThrottled, CodeServiceUnavailable, CodeTimeout, CodeUnknown:
		return true
	}
	return false
}

// AWSSide drives the "something may be wrong on AWS's side" banner. It is
// deliberately narrow: showing it for a permission error the user caused would
// send them looking at a status page instead of at their own config.
func AWSSide(c Code) bool {
	switch c {
	case CodeThrottled, CodeServiceUnavailable, CodeTimeout:
		return true
	}
	return false
}

// Indeterminate means the outcome of the underlying work is genuinely unknown
// and must not be reported either way.
func Indeterminate(c Code) bool {
	switch c {
	case CodeTimeout, CodeServiceUnavailable, CodeUnknown:
		return true
	}
	return false
}
