package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/cloud-ai/ai-aws-architect/internal/awsfail"
	"github.com/cloud-ai/ai-aws-architect/internal/domain"
	"github.com/cloud-ai/ai-aws-architect/internal/runner"
	"github.com/cloud-ai/ai-aws-architect/internal/store"
)

type WorkerOptions struct {
	// ID identifies this worker in the jobs table. Any stable-ish string.
	ID string
	// PollInterval is how long to sleep when the queue is empty.
	PollInterval time.Duration
	// HeartbeatInterval must be comfortably shorter than StaleAfter.
	HeartbeatInterval time.Duration
	// StaleAfter is how long a silent running job waits before being declared
	// abandoned. Too short and a slow CloudFront distribution gets reclaimed
	// out from under a healthy worker; fifteen minutes is a safe floor.
	StaleAfter time.Duration
	// WorkspaceTTL is how long an unreferenced workspace survives before the
	// sweep removes it. Each one holds a full copy of the AWS provider -
	// hundreds of megabytes - so leaking them fills a disk quickly.
	WorkspaceTTL time.Duration
	// BaseBackoff is the first retry delay; it doubles per attempt.
	BaseBackoff time.Duration
}

// Worker drains the jobs table.
//
// It is not an agent and makes no decisions. It claims a job, runs one
// Terraform operation, and moves a deployment along a fixed state machine.
type Worker struct {
	svc  *Service
	jobs *store.JobStore
	deps *store.DeploymentStore
	opts WorkerOptions
}

func NewWorker(svc *Service, jobs *store.JobStore, deps *store.DeploymentStore, opts WorkerOptions) *Worker {
	if opts.ID == "" {
		opts.ID = "worker-" + uuid.NewString()[:8]
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 15 * time.Second
	}
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = 15 * time.Minute
	}
	if opts.BaseBackoff <= 0 {
		opts.BaseBackoff = 10 * time.Second
	}
	if opts.WorkspaceTTL <= 0 {
		opts.WorkspaceTTL = 2 * time.Hour
	}
	return &Worker{svc: svc, jobs: jobs, deps: deps, opts: opts}
}

// Run blocks until the context is canceled.
func (w *Worker) Run(ctx context.Context) {
	slog.Info("deployment worker started", "worker_id", w.opts.ID, "runner", w.svc.runner.Name())

	// Anything still marked running at startup belonged to a process that is
	// no longer here. See Reconcile for why those become unknown, not failed.
	w.Reconcile(ctx)
	w.sweepWorkspaces(ctx)

	reconcile := time.NewTicker(w.opts.StaleAfter / 3)
	defer reconcile.Stop()
	poll := time.NewTicker(w.opts.PollInterval)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("deployment worker stopping", "worker_id", w.opts.ID)
			return
		case <-reconcile.C:
			w.Reconcile(ctx)
			w.sweepWorkspaces(ctx)
		case <-poll.C:
			// Drain rather than taking one job per tick, so a backlog clears
			// promptly instead of at one job per poll interval.
			for {
				claimed, err := w.claimAndRun(ctx)
				if err != nil || !claimed {
					break
				}
			}
		}
	}
}

// Reconcile finds jobs whose worker stopped reporting in.
//
// These are deliberately NOT marked failed. A worker that died during an apply
// may have left half a VPC behind, and "failed" reads as "nothing happened".
// The deployment goes to unknown, whose only exit is a fresh plan - which asks
// AWS what actually exists instead of guessing.
func (w *Worker) Reconcile(ctx context.Context) {
	stale, err := w.jobs.ReclaimStale(ctx, w.opts.StaleAfter)
	if err != nil {
		slog.Error("reclaiming stale jobs failed", "err", err)
		return
	}
	for _, sj := range stale {
		slog.Warn("job abandoned by a lost worker; deployment outcome is unknown",
			"job_id", sj.JobID, "deployment_id", sj.DeploymentID, "type", sj.Type)
		if _, err := w.deps.Transition(ctx, sj.DeploymentID, domain.DeployUnknown, store.TransitionParams{
			FailureCode:    "worker_lost",
			FailureMessage: "The process running this operation stopped unexpectedly. We cannot tell what was created, so nothing is being assumed. Run a plan to find out what exists.",
		}); err != nil {
			slog.Error("could not mark deployment unknown", "deployment_id", sj.DeploymentID, "err", err)
		}
	}
}

func (w *Worker) claimAndRun(ctx context.Context) (bool, error) {
	job, err := w.jobs.Claim(ctx, w.opts.ID)
	if errors.Is(err, store.ErrNoJob) {
		return false, nil
	}
	if err != nil {
		slog.Error("claiming a job failed", "err", err)
		return false, err
	}

	slog.Info("job claimed", "job_id", job.ID, "type", job.Type,
		"deployment_id", job.DeploymentID, "attempt", job.Attempts)
	w.execute(ctx, job)
	return true, nil
}

func (w *Worker) execute(ctx context.Context, job *domain.Job) {
	runCtx, cancel := context.WithTimeout(ctx, w.svc.opts.MaxRunTime)
	defer cancel()

	stopBeat := w.beat(runCtx, job.ID)
	defer stopBeat()

	req, err := w.buildRequest(runCtx, job)
	if err != nil {
		// Failing before the runner starts means nothing was attempted, so this
		// is a clean failure rather than an unknown.
		w.finishFailed(ctx, job, "precondition_failed", err.Error(), false)
		return
	}

	switch job.Type {
	case domain.JobPlan:
		w.runPlan(ctx, runCtx, job, req)
	case domain.JobApply:
		w.runApply(ctx, runCtx, job, req)
	case domain.JobDestroy:
		w.runDestroy(ctx, runCtx, job, req)
	default:
		w.finishFailed(ctx, job, "unknown_job_type", string(job.Type), false)
	}

	w.ensureFinished(ctx, job)
}

// ensureFinished is the backstop for a handler that returned without recording
// an outcome.
//
// A job left in 'running' is not merely untidy: it holds the per-chat lock, so
// nothing else can run for that chat, and it sits there until the stale reclaim
// notices - which then blames a lost worker for what was actually a bug. Better
// to say plainly that the outcome went unrecorded.
func (w *Worker) ensureFinished(ctx context.Context, job *domain.Job) {
	cur, err := w.jobs.Get(ctx, job.ID)
	if err != nil {
		slog.Error("could not confirm job finished", "job_id", job.ID, "err", err)
		return
	}
	if cur.Status != domain.JobRunning {
		return
	}
	slog.Error("BUG: handler left a job running; marking it unknown",
		"job_id", job.ID, "type", job.Type, "deployment_id", job.DeploymentID)
	_ = w.jobs.MarkUnknown(ctx, job.ID, "unrecorded_outcome",
		"the operation finished but its outcome was never recorded")
}

func (w *Worker) runPlan(ctx, runCtx context.Context, job *domain.Job, req runner.Request) {
	res, err := w.svc.runner.Plan(runCtx, req)
	if err != nil {
		// A failed plan produced no saved plan file, so nothing in the
		// workspace is worth keeping.
		w.cleanup(ctx, job)
		w.handleRunError(ctx, job, domain.DeployPlanFailed, err)
		return
	}
	summary, _ := json.Marshal(res.Summary)

	if _, err := w.deps.Transition(ctx, job.DeploymentID, domain.DeployAwaitingApproval,
		store.TransitionParams{
			PlanHash:    res.Hash,
			PlanOutput:  res.Output,
			PlanSummary: summary,
		}); err != nil {
		slog.Error("could not record plan result", "job_id", job.ID, "err", err)
		_ = w.jobs.MarkUnknown(ctx, job.ID, "unrecorded_outcome",
			"the plan ran but its result could not be recorded")
		return
	}
	_ = w.jobs.Succeed(ctx, job.ID)
	slog.Info("plan ready for approval", "deployment_id", job.DeploymentID,
		"add", res.Summary.Add, "change", res.Summary.Change,
		"replace", res.Summary.Replace, "destroy", res.Summary.Destroy)
}

func (w *Worker) runApply(ctx, runCtx context.Context, job *domain.Job, req runner.Request) {
	_, err := w.svc.runner.Apply(runCtx, req)

	// Either way the saved plan has been consumed and the workspace has done
	// its job. Destroy rebuilds from scratch - state lives in S3 - so nothing
	// downstream needs this directory.
	defer w.cleanup(ctx, job)

	if err != nil {
		// apply_failed, not unknown, when the error is determinate - but
		// resources may STILL exist, because Terraform writes state for
		// everything it created before stopping. MayHaveLiveResources reports
		// true for apply_failed for exactly this reason.
		w.handleRunError(ctx, job, domain.DeployApplyFailed, err)
		return
	}

	var teardownAt *time.Time
	if w.svc.opts.TeardownAfter > 0 {
		t := time.Now().Add(w.svc.opts.TeardownAfter)
		teardownAt = &t
	}

	dep, err := w.deps.Transition(ctx, job.DeploymentID, domain.DeployApplied,
		store.TransitionParams{MarkApplied: true, TeardownAfter: teardownAt})
	if err != nil {
		// Resources exist but the record does not say so. Unknown is the only
		// honest status, and the job must not be left running.
		slog.Error("apply succeeded but could not be recorded", "job_id", job.ID, "err", err)
		_ = w.jobs.MarkUnknown(ctx, job.ID, "unrecorded_outcome",
			"the apply ran but its result could not be recorded")
		return
	}
	_ = w.jobs.Succeed(ctx, job.ID)

	// Auto-teardown is just a scheduled job. No EventBridge rule - and unlike
	// one in our account, this can reach into the customer's account.
	if teardownAt != nil {
		if _, err := w.svc.jobs.Schedule(ctx, domain.JobDestroy, dep.ID, dep.ChatID, *teardownAt); err != nil {
			// Loud: a missing teardown means resources bill indefinitely.
			slog.Error("APPLIED BUT TEARDOWN NOT SCHEDULED - resources will not be cleaned up automatically",
				"deployment_id", dep.ID, "err", err)
		}
	}
	slog.Info("deployment applied", "deployment_id", dep.ID, "teardown_after", teardownAt)
}

func (w *Worker) runDestroy(ctx, runCtx context.Context, job *domain.Job, req runner.Request) {
	// A scheduled teardown job does not pass through the service, so the
	// deployment is still 'applied' when this runs. applied -> destroyed is not
	// a legal transition, and discovering that AFTER Terraform has already
	// destroyed everything leaves the system unable to record what it just did.
	//
	// Idempotent when the manual destroy endpoint has already moved it.
	if _, err := w.deps.Transition(ctx, job.DeploymentID, domain.DeployDestroying,
		store.TransitionParams{}); err != nil {
		// Illegal from here means there is nothing to tear down - already
		// destroyed, or canceled. Not a failure; the job's work is moot.
		slog.Info("skipping teardown: deployment is not in a destroyable state",
			"job_id", job.ID, "deployment_id", job.DeploymentID, "err", err)
		_ = w.jobs.Succeed(ctx, job.ID)
		w.cleanup(ctx, job)
		return
	}

	_, err := w.svc.runner.Destroy(runCtx, req)
	if err != nil {
		// Left in place on failure: a retry reuses it, and it is evidence when
		// someone has to work out what happened.
		w.handleRunError(ctx, job, domain.DeployDestroyFailed, err)
		return
	}
	w.cleanup(ctx, job)
	if _, err := w.deps.Transition(ctx, job.DeploymentID, domain.DeployDestroyed,
		store.TransitionParams{MarkDestroyed: true}); err != nil {
		// The "destroy" itself worked; only recording it failed. That is
		// genuinely indeterminate from the system's point of view, and it must
		// not be left for the stale reclaim to discover half an hour later.
		slog.Error("destroy succeeded but could not be recorded", "job_id", job.ID, "err", err)
		_ = w.jobs.MarkUnknown(ctx, job.ID, "unrecorded_outcome",
			"the teardown ran but its result could not be recorded")
		return
	}
	_ = w.jobs.Succeed(ctx, job.ID)
	slog.Info("deployment destroyed", "deployment_id", job.DeploymentID)
}

// handleRunError is where the indeterminate flag finally lands.
//
// An error that might mean the work partially succeeded - a timeout, an AWS
// internal error, anything unclassifiable - must never be reported as a clean
// failure, because "failed" reads as "nothing was created". That is how
// resources get orphaned and billed forever.
func (w *Worker) handleRunError(ctx context.Context, job *domain.Job, failedStatus domain.DeploymentStatus, runErr error) {
	f := awsfail.Classify(runErr)
	// Raw error to logs only: AWS error strings can echo request parameters.
	slog.Error("run failed", "job_id", job.ID, "type", job.Type,
		"code", f.Code, "indeterminate", f.Indeterminate, "err", runErr)

	if f.Indeterminate {
		_ = w.jobs.MarkUnknown(ctx, job.ID, string(f.Code), f.Message)
		if _, err := w.deps.Transition(ctx, job.DeploymentID, domain.DeployUnknown,
			store.TransitionParams{
				FailureCode: string(f.Code),
				FailureMessage: f.Message +
					" Because this may have partially taken effect, nothing is being assumed about what exists. Run a plan to find out.",
			}); err != nil {
			slog.Error("could not mark deployment unknown", "job_id", job.ID, "err", err)
		}
		return
	}

	w.finishFailed(ctx, job, string(f.Code), f.Message, f.Retryable)
	if requeued := w.wasRequeued(ctx, job.ID); requeued {
		// Still pending a retry, so the deployment stays in flight.
		return
	}
	if _, err := w.deps.Transition(ctx, job.DeploymentID, failedStatus,
		store.TransitionParams{FailureCode: string(f.Code), FailureMessage: f.Message}); err != nil {
		slog.Error("could not mark deployment failed", "job_id", job.ID, "err", err)
	}
}

// cleanup releases a deployment's workspace, logging rather than failing: a
// leaked directory is a disk problem, not a correctness problem, and must never
// turn a successful apply into a reported failure.
func (w *Worker) cleanup(ctx context.Context, job *domain.Job) {
	if err := w.svc.runner.Cleanup(ctx, job.DeploymentID.String()); err != nil {
		slog.Warn("could not remove deployment workspace",
			"deployment_id", job.DeploymentID, "err", err)
	}
}

// sweepWorkspaces is the backstop for workspaces Cleanup never reached - a
// process killed mid-run, or a status write that failed. Anything awaiting
// approval or in flight is protected; the rest go once they are old enough
// that no job could still be starting up inside them.
func (w *Worker) sweepWorkspaces(ctx context.Context) {
	ids, err := w.deps.WorkspaceRetainIDs(ctx)
	if err != nil {
		slog.Error("could not list workspaces to retain", "err", err)
		return
	}
	keep := make([]string, 0, len(ids))
	for _, id := range ids {
		keep = append(keep, id.String())
	}

	removed, err := w.svc.runner.Sweep(ctx, keep, w.opts.WorkspaceTTL)
	if err != nil {
		slog.Warn("workspace sweep failed", "err", err)
		return
	}
	if removed > 0 {
		slog.Info("removed stale deployment workspaces", "count", removed, "retained", len(keep))
	}
}

func (w *Worker) finishFailed(ctx context.Context, job *domain.Job, code, msg string, retryable bool) {
	// Exponential: attempts is already incremented at claim time.
	backoff := w.opts.BaseBackoff * time.Duration(1<<uint(max(0, job.Attempts-1)))
	if _, err := w.jobs.Fail(ctx, job.ID, code, msg, retryable, backoff); err != nil {
		slog.Error("could not record job failure", "job_id", job.ID, "err", err)
	}
}

func (w *Worker) wasRequeued(ctx context.Context, jobID uuid.UUID) bool {
	j, err := w.jobs.Get(ctx, jobID)
	if err != nil {
		return false
	}
	return j.Status == domain.JobPending
}

// beat keeps the job's heartbeat fresh so Reconcile does not mistake a long
// CloudFront distribution for a dead worker.
func (w *Worker) beat(ctx context.Context, jobID uuid.UUID) func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(w.opts.HeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				// Detached context: the run context may already be cancelling,
				// and a heartbeat write must still land.
				bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				if err := w.jobs.Heartbeat(bctx, jobID); err != nil {
					slog.Warn("heartbeat failed", "job_id", jobID, "err", err)
				}
				cancel()
			}
		}
	}()
	return func() { close(done) }
}

func (w *Worker) buildRequest(ctx context.Context, job *domain.Job) (runner.Request, error) {
	// Unscoped: the worker has no request and therefore no authenticated user.
	// The deployment row carries its owner, which is what resolves the AWS
	// connection below.
	dep, err := w.deps.GetUnscoped(ctx, job.DeploymentID)
	if err != nil {
		return runner.Request{}, err
	}

	cv, err := w.svc.chats.GetConfigVersion(ctx, dep.ChatID, dep.ConfigVersion)
	if err != nil {
		return runner.Request{}, fmt.Errorf("load config version: %w", err)
	}

	// Re-resolved at execution time, not read from the deployment row. A
	// connection revoked since planning must fail here rather than have us
	// attempt a run we cannot authenticate.
	target, err := w.svc.aws.TargetFor(ctx, dep.UserID)
	if err != nil {
		return runner.Request{}, fmt.Errorf("resolve aws target: %w", err)
	}

	return runner.Request{
		DeploymentID: dep.ID.String(),
		ChatID:       dep.ChatID.String(),
		Config:       cv.Document,
		StateKey:     dep.StateKey,
		Target: runner.Target{
			RoleARN:    target.RoleARN,
			ExternalID: target.ExternalID,
			AccountID:  target.AccountID,
			Region:     target.Region,
		},
		Tags: map[string]string{
			"ManagedBy":     "ai-aws-architect",
			"ChatId":        dep.ChatID.String(),
			"DeploymentId":  dep.ID.String(),
			"ConfigVersion": fmt.Sprintf("%d", dep.ConfigVersion),
		},
	}, nil
}
