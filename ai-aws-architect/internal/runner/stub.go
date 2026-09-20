package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StubRunner simulates Terraform deterministically. No binary, no AWS, no cost.
//
// It exists so the deployment lifecycle - plan, approve, apply, teardown,
// failure classification, retry, unknown - can be exercised end to end before
// the real runner exists, and so tests do not need a cloud account.
//
// Delay is non-zero on purpose: a plan that returns instantly hides every
// concurrency bug the job queue is there to prevent.
type StubRunner struct {
	Delay time.Duration
	// FailPlan / FailApply / FailDestroy inject failures for testing the
	// failure paths, which are otherwise very hard to reach on demand.
	FailPlan    error
	FailApply   error
	FailDestroy error
}

func NewStubRunner() *StubRunner { return &StubRunner{Delay: 2 * time.Second} }

func (r *StubRunner) Name() string { return "stub" }

func (r *StubRunner) Plan(ctx context.Context, req Request) (*PlanResult, error) {
	if err := r.wait(ctx); err != nil {
		return nil, err
	}
	if r.FailPlan != nil {
		return nil, r.FailPlan
	}

	var doc struct {
		Blocks []struct {
			ID         string `json:"id"`
			TemplateID string `json:"template_id"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(req.Config, &doc); err != nil {
		return nil, fmt.Errorf("stub plan: %w", err)
	}

	var out strings.Builder
	out.WriteString("Terraform will perform the following actions (STUB - nothing real):\n\n")
	for _, b := range doc.Blocks {
		out.WriteString(fmt.Sprintf("  + module.%s (%s)\n", b.ID, b.TemplateID))
	}

	// Hash covers the config AND the target account, so an approval cannot be
	// replayed against a different account.
	sum := sha256.Sum256(append(req.Config, []byte(req.Target.AccountID+req.StateKey)...))

	return &PlanResult{
		Hash:    hex.EncodeToString(sum[:]),
		Output:  out.String(),
		Summary: PlanSummary{Add: len(doc.Blocks), DestructiveResources: []string{}},
	}, nil
}

func (r *StubRunner) Apply(ctx context.Context, _ Request) (*ApplyResult, error) {
	if err := r.wait(ctx); err != nil {
		return nil, err
	}
	if r.FailApply != nil {
		return nil, r.FailApply
	}
	return &ApplyResult{
		Outputs:       map[string]string{"stub": "no real resources were created"},
		ResourceCount: 0,
	}, nil
}

func (r *StubRunner) Destroy(ctx context.Context, _ Request) (*DestroyResult, error) {
	if err := r.wait(ctx); err != nil {
		return nil, err
	}
	if r.FailDestroy != nil {
		return nil, r.FailDestroy
	}
	return &DestroyResult{Destroyed: 0}, nil
}

// Provisionable is always true: the stub fabricates a plan from the config
// document alone and never looks for a module, so nothing in the catalog can
// fail under it. This is exactly why a stub demo cannot prove a template works.
func (r *StubRunner) Provisionable(string) bool { return true }

// Cleanup and Sweep are no-ops: the stub never writes anything to disk.
func (r *StubRunner) Cleanup(context.Context, string) error { return nil }

func (r *StubRunner) Sweep(context.Context, []string, time.Duration) (int, error) { return 0, nil }

func (r *StubRunner) wait(ctx context.Context) error {
	if r.Delay <= 0 {
		return nil
	}
	select {
	case <-time.After(r.Delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
