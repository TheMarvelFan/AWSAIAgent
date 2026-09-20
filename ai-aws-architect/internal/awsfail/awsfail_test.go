package awsfail

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/smithy-go"
)

type apiErr struct{ code string }

func (e *apiErr) Error() string                 { return e.code + ": something went wrong" }
func (e *apiErr) ErrorCode() string             { return e.code }
func (e *apiErr) ErrorMessage() string          { return "something went wrong" }
func (e *apiErr) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

func TestClassifyByAPICode(t *testing.T) {
	cases := map[string]Code{
		"AccessDenied":                  CodeAccessDenied,
		"NoSuchEntity":                  CodeRoleMissing,
		"OptInRequired":                 CodeAccountProblem,
		"SubscriptionRequiredException": CodeAccountProblem,
		"AuthFailure":                   CodeAccountProblem,
		"ThrottlingException":           CodeThrottled,
		"ServiceUnavailable":            CodeServiceUnavailable,
		"LimitExceeded":                 CodeQuotaExceeded,
		"ExpiredToken":                  CodeOurCredentials,
		"SomethingNobodyAnticipated":    CodeUnknown,
	}
	for code, want := range cases {
		if got := Classify(&apiErr{code: code}).Code; got != want {
			t.Errorf("%s: got %q, want %q", code, got, want)
		}
	}
}

// The safety property that matters most: anything we could not classify, and
// anything that may have succeeded with the response lost, must be marked
// indeterminate so no caller reports success or failure of the actual work.
func TestUnknownAndTimeoutAreIndeterminate(t *testing.T) {
	for _, err := range []error{
		errors.New("some error nobody has ever seen"),
		context.DeadlineExceeded,
		fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
		&apiErr{code: "ServiceUnavailable"},
	} {
		if f := Classify(err); !f.Indeterminate {
			t.Errorf("%v: expected indeterminate, got %+v", err, f)
		}
	}
}

// A permission or account problem must NOT raise the "AWS may be down" banner,
// or users go and read a status page instead of fixing their own account.
func TestUserFacingProblemsAreNotAWSSide(t *testing.T) {
	for _, code := range []string{"AccessDenied", "NoSuchEntity", "OptInRequired", "LimitExceeded"} {
		f := Classify(&apiErr{code: code})
		if f.AWSSide {
			t.Errorf("%s: should not be flagged as AWS-side", code)
		}
		if f.Retryable {
			t.Errorf("%s: should not be retryable", code)
		}
	}
}

// Properties recomputed from a stored code must match the freshly classified
// failure, or a connection read back from the database reports differently
// from one classified live.
func TestStoredCodePropertiesMatch(t *testing.T) {
	for _, code := range []string{
		"AccessDenied", "NoSuchEntity", "OptInRequired", "ThrottlingException",
		"ServiceUnavailable", "LimitExceeded", "ExpiredToken", "RequestTimeout",
	} {
		f := Classify(&apiErr{code: code})
		if Retryable(f.Code) != f.Retryable {
			t.Errorf("%s: Retryable mismatch", code)
		}
		if AWSSide(f.Code) != f.AWSSide {
			t.Errorf("%s: AWSSide mismatch", code)
		}
		if Indeterminate(f.Code) != f.Indeterminate {
			t.Errorf("%s: Indeterminate mismatch", code)
		}
	}
}

// narrowed simulates a command runner that wraps a large body of output.
type narrowed struct{ diag, output string }

func (e *narrowed) Error() string            { return e.diag + "\n" + e.output }
func (e *narrowed) ClassifiableText() string { return e.diag }

// The bug this guards: Terraform prints resource attributes, and a Lambda with
// `timeout = 15` in its plan output was classified as a network timeout - which
// marked a determinate failure as indeterminate and sent the deployment to
// 'unknown' instead of 'destroy_failed'.
func TestClassifiableIgnoresCommandOutput(t *testing.T) {
	err := &narrowed{
		diag:   "Error: deleting IAM Role: AccessDenied: not authorized to perform iam:DeleteRole",
		output: `- timeout = 15 -> null` + "\n" + `- bucket = "throttle-test" -> null`,
	}
	if got := Classify(err).Code; got != CodeAccessDenied {
		t.Errorf("got %q, want %q - output text is leaking into classification", got, CodeAccessDenied)
	}
}

// An empty narrowing must not swallow the error: fall through to the normal
// message match rather than returning nothing useful.
func TestEmptyClassifiableFallsThrough(t *testing.T) {
	err := &narrowed{diag: "   ", output: "irrelevant"}
	if got := Classify(err).Code; got != CodeUnknown {
		t.Errorf("got %q, want %q", got, CodeUnknown)
	}
}
