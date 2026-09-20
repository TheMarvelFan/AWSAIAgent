package terraform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloud-ai/ai-aws-architect/internal/runner"
)

//go:embed modules
var moduleFS embed.FS

const planFile = "tfplan"

type Options struct {
	// Binary is the terraform executable. Looked up on PATH if bare.
	Binary string
	// WorkRoot holds one directory per deployment.
	//
	// NOT a temp dir that disappears: the saved plan file written by Plan must
	// still be there when Apply runs, because applying the SAVED plan is what
	// guarantees the approved plan and the executed plan are the same artifact.
	// This does mean plan and apply must run on the same machine and volume -
	// an honest single-worker limitation, and the thing to revisit first if
	// this ever runs on more than one instance.
	WorkRoot string
	// StateBucket / StateRegion hold Terraform state, one key per chat.
	StateBucket string
	StateRegion string
	// MaxOutputBytes caps captured stdout+stderr. Terraform is chatty and an
	// unbounded capture is a memory footgun on a large plan.
	MaxOutputBytes int
}

type Runner struct {
	opts    Options
	modules map[string]bool
}

func New(opts Options) (*Runner, error) {
	if opts.Binary == "" {
		opts.Binary = "terraform"
	}
	if opts.WorkRoot == "" {
		opts.WorkRoot = filepath.Join(os.TempDir(), "ai-aws-architect")
	}
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = 1 << 20
	}
	if opts.StateBucket == "" {
		return nil, fmt.Errorf("TF_STATE_BUCKET is required for the terraform runner")
	}
	if _, err := exec.LookPath(opts.Binary); err != nil {
		return nil, fmt.Errorf("terraform binary %q not found on PATH: %w", opts.Binary, err)
	}
	if err := os.MkdirAll(opts.WorkRoot, 0o755); err != nil {
		return nil, err
	}

	entries, err := fs.ReadDir(moduleFS, "modules")
	if err != nil {
		return nil, err
	}
	mods := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			mods[e.Name()] = true
		}
	}
	if len(mods) == 0 {
		return nil, fmt.Errorf("no terraform modules are bundled")
	}
	return &Runner{opts: opts, modules: mods}, nil
}

func (r *Runner) Name() string { return "terraform" }

// Provisionable reports whether a module is bundled for this template. The
// generator errors at plan time for anything else, so this is the same fact the
// startup warning is built from.
func (r *Runner) Provisionable(templateID string) bool { return r.modules[templateID] }

// Modules reports which catalog templates have a bundled module, so startup can
// warn about templates that would fail at plan time rather than surprising
// someone mid-demo.
func (r *Runner) Modules() []string {
	out := make([]string, 0, len(r.modules))
	for m := range r.modules {
		out = append(out, m)
	}
	return out
}

func (r *Runner) Plan(ctx context.Context, req runner.Request) (*runner.PlanResult, error) {
	dir, err := r.prepare(ctx, req)
	if err != nil {
		return nil, err
	}

	if _, err := r.run(ctx, dir, "plan",
		"-input=false", "-no-color", "-lock-timeout=60s", "-out="+planFile); err != nil {
		return nil, err
	}

	raw, err := r.run(ctx, dir, "show", "-json", planFile)
	if err != nil {
		return nil, err
	}
	summary, err := parsePlanJSON(raw)
	if err != nil {
		return nil, err
	}

	human, err := r.run(ctx, dir, "show", "-no-color", planFile)
	if err != nil {
		return nil, err
	}

	// Hash the saved plan file itself, not its rendering. Apply runs this exact
	// file and Terraform independently refuses it if state has moved since -
	// so the binding between what a human approved and what executes is
	// enforced twice, once by us and once by Terraform.
	planBytes, err := os.ReadFile(filepath.Join(dir, planFile))
	if err != nil {
		return nil, fmt.Errorf("read saved plan: %w", err)
	}
	sum := sha256.Sum256(planBytes)

	return &runner.PlanResult{
		Hash:    hex.EncodeToString(sum[:]),
		Output:  string(human),
		Summary: summary,
	}, nil
}

func (r *Runner) Apply(ctx context.Context, req runner.Request) (*runner.ApplyResult, error) {
	dir := r.workspace(req.DeploymentID)

	// The saved plan must still exist. If it does not, the workspace was lost
	// between approval and apply, and re-planning here would apply something
	// nobody approved. Refusing is the only safe answer.
	if _, err := os.Stat(filepath.Join(dir, planFile)); err != nil {
		return nil, fmt.Errorf("the approved plan is no longer available in this workspace; re-plan before applying: %w", err)
	}

	if _, err := r.run(ctx, dir, "apply",
		"-input=false", "-no-color", "-lock-timeout=120s", planFile); err != nil {
		return nil, err
	}

	outputs, err := r.outputs(ctx, dir)
	if err != nil {
		// The "apply" succeeded; failing to read outputs is cosmetic and must not
		// be reported as a failed apply.
		outputs = map[string]string{}
	}
	return &runner.ApplyResult{Outputs: outputs, ResourceCount: len(outputs)}, nil
}

func (r *Runner) Destroy(ctx context.Context, req runner.Request) (*runner.DestroyResult, error) {
	// Rebuilt rather than reused: teardown often runs long after the plan, and
	// the workspace may be gone. State lives in S3, so a fresh workspace still
	// knows exactly what exists.
	dir, err := r.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	if _, err := r.run(ctx, dir, "destroy",
		"-input=false", "-no-color", "-auto-approve", "-lock-timeout=120s"); err != nil {
		return nil, err
	}
	return &runner.DestroyResult{}, nil
}

// prepare materializes the workspace and runs init.
func (r *Runner) prepare(ctx context.Context, req runner.Request) (string, error) {
	var arch architecture
	if err := json.Unmarshal(req.Config, &arch); err != nil {
		return "", fmt.Errorf("parse config document: %w", err)
	}
	if arch.Region == "" {
		arch.Region = req.Target.Region
	}

	dir := r.workspace(req.DeploymentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := writeModules(dir); err != nil {
		return "", err
	}

	root, err := generateRoot(req, arch, r.modules)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(root), 0o644); err != nil {
		return "", err
	}

	// One state key per chat: that is what makes applying version 5 over
	// version 4 a modification of the existing infrastructure rather than a
	// second copy of it.
	if _, err := r.run(ctx, dir, "init",
		"-input=false", "-no-color", "-reconfigure",
		"-backend-config=bucket="+r.opts.StateBucket,
		"-backend-config=key="+req.StateKey,
		"-backend-config=region="+r.opts.StateRegion,
		// Terraform 1.10+ locks via a file in the state bucket, so no DynamoDB
		// table is needed. On older Terraform this flag is rejected and a
		// dynamodb_table must be supplied instead.
		"-backend-config=use_lockfile=true",
		"-backend-config=encrypt=true",
	); err != nil {
		return "", err
	}
	return dir, nil
}

// Cleanup removes a deployment's working directory.
//
// Safe to call after apply and after destroy: Destroy rebuilds the workspace
// from scratch, because state lives in S3 and teardown often runs long after
// the plan. The one moment it must not run is while a deployment is awaiting
// approval, which the worker enforces by only calling it on terminal outcomes.
func (r *Runner) Cleanup(_ context.Context, deploymentID string) error {
	dir := r.workspace(deploymentID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove workspace %s: %w", dir, err)
	}
	return nil
}

// Sweep removes workspaces that no live deployment needs.
//
// keep holds deployment IDs whose directories must survive - anything awaiting
// approval or actively running. olderThan guards against deleting a directory
// a job has just created but not yet registered.
func (r *Runner) Sweep(_ context.Context, keep []string, olderThan time.Duration) (int, error) {
	protected := make(map[string]bool, len(keep))
	for _, id := range keep {
		protected[filepath.Base(r.workspace(id))] = true
	}

	entries, err := os.ReadDir(r.opts.WorkRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || protected[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.opts.WorkRoot, e.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func (r *Runner) workspace(deploymentID string) string {
	return filepath.Join(r.opts.WorkRoot, hclIdent(strings.ReplaceAll(deploymentID, "-", "")))
}

func writeModules(dir string) error {
	return fs.WalkDir(moduleFS, "modules", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := moduleFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
}

func (r *Runner) outputs(ctx context.Context, dir string) (map[string]string, error) {
	raw, err := r.run(ctx, dir, "output", "-json", "-no-color")
	if err != nil {
		return nil, err
	}
	var parsed map[string]struct {
		Value     any  `json:"value"`
		Sensitive bool `json:"sensitive"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range parsed {
		// Terraform state can contain real secrets. Anything marked sensitive
		// is recorded as present, never as a value.
		if v.Sensitive {
			out[k] = "(sensitive, not shown)"
			continue
		}
		b, _ := json.Marshal(v.Value)
		out[k] = string(b)
	}
	return out, nil
}

// run executes terraform and returns stdout. On failure the error carries a
// trimmed tail of the combined output, which is what surfaces to the user.
func (r *Runner) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.opts.Binary, args...)
	cmd.Dir = dir

	// Inherit the environment so the AWS default credential chain works: the
	// provider needs OUR base identity in order to assume the customer role.
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"CHECKPOINT_DISABLE=1",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	if err != nil {
		return nil, &RunError{
			Command:  args[0],
			Duration: time.Since(start).Round(time.Second),
			ExitErr:  err,
			// Terraform writes diagnostics to stderr and progress to stdout.
			// Keeping them apart matters twice over: stdout can run to
			// megabytes and would bury stderr entirely, and its resource
			// attributes poison any substring classification.
			Diagnostics: head(strings.TrimSpace(stderr.String()), 4000),
			Output:      tail(strings.TrimSpace(stdout.String()), 4000),
		}
	}
	if stdout.Len() > r.opts.MaxOutputBytes {
		return stdout.Bytes()[:r.opts.MaxOutputBytes], nil
	}
	return stdout.Bytes(), nil
}

// RunError is a failed terraform invocation.
//
// It separates the text a human should read from the text a classifier should
// match. Collapsing the two is how a Lambda's `timeout = 15` attribute became
// a reported network timeout.
type RunError struct {
	Command     string
	Duration    time.Duration
	ExitErr     error
	Diagnostics string
	Output      string
}

func (e *RunError) Error() string {
	msg := fmt.Sprintf("terraform %s failed after %s: %v", e.Command, e.Duration, e.ExitErr)
	if e.Diagnostics != "" {
		return msg + ": " + e.Diagnostics
	}
	// No diagnostics at all is unusual; fall back to stdout so the failure is
	// not completely opaque.
	if e.Output != "" {
		return msg + ": " + tail(e.Output, 1000)
	}
	return msg
}

func (e *RunError) Unwrap() error { return e.ExitErr }

// ClassifiableText satisfies awsfail.Classifiable: only the diagnostics, never
// the command output.
func (e *RunError) ClassifiableText() string { return e.Diagnostics }

// head keeps the START of a string. Terraform reports its first error first,
// and that is almost always the cause; later ones are consequences.
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// planJSON is the slice of `terraform show -json` this needs.
type planJSON struct {
	ResourceChanges []struct {
		Address string `json:"address"`
		Change  struct {
			Actions []string `json:"actions"`
		} `json:"change"`
	} `json:"resource_changes"`
}

// parsePlanJSON turns Terraform's machine-readable plan into counts.
//
// The action combinations are Terraform's own encoding: a "replace" is
// ["delete","create"] or ["create","delete"] depending on lifecycle, and both
// mean the resource is destroyed and rebuilt. That distinction is the whole
// reason Replace is counted separately from Change - "this will delete your
// bucket contents" must not render the same as "this will resize a setting".
func parsePlanJSON(raw []byte) (runner.PlanSummary, error) {
	var p planJSON
	if err := json.Unmarshal(raw, &p); err != nil {
		return runner.PlanSummary{}, fmt.Errorf("parse plan json: %w", err)
	}

	// Non-nil so an empty plan serializes as [] rather than null, matching the
	// promise that DestructiveResources is always present.
	s := runner.PlanSummary{DestructiveResources: []string{}}
	for _, rc := range p.ResourceChanges {
		switch actionKind(rc.Change.Actions) {
		case "create":
			s.Add++
		case "update":
			s.Change++
		case "replace":
			s.Replace++
			s.DestructiveResources = append(s.DestructiveResources, rc.Address)
		case "delete":
			s.Destroy++
			s.DestructiveResources = append(s.DestructiveResources, rc.Address)
		}
	}
	return s, nil
}

func actionKind(actions []string) string {
	switch len(actions) {
	case 1:
		switch actions[0] {
		case "create":
			return "create"
		case "update":
			return "update"
		case "delete":
			return "delete"
		default: // "no-op", "read"
			return ""
		}
	case 2:
		if (actions[0] == "delete" && actions[1] == "create") ||
			(actions[0] == "create" && actions[1] == "delete") {
			return "replace"
		}
	}
	return ""
}
