package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

type JobStore struct{ pool *pgxpool.Pool }

func NewJobStore(pool *pgxpool.Pool) *JobStore { return &JobStore{pool: pool} }

// Status VALUES are bound from the domain constants so there is one source of
// truth: renaming a constant without updating a literal would otherwise write a
// value the CHECK constraint rejects, at runtime, in production.
//
// Status predicates in WHERE clauses stay as literals on purpose. The partial
// indexes (jobs_claim_idx, jobs_running_idx, jobs_one_running_per_chat) are
// defined WHERE status = '<literal>', and Postgres can only use a partial index
// when it can prove the query predicate implies the index predicate. It
// cannot do this against a bound parameter in a generic plan.

const jobColumns = `id, type, deployment_id, chat_id, status, attempts, max_attempts,
	run_after, started_at, finished_at, failure_code, last_error, created_at`

func scanJob(row rowScanner) (*domain.Job, error) {
	var j domain.Job
	if err := row.Scan(&j.ID, &j.Type, &j.DeploymentID, &j.ChatID, &j.Status,
		&j.Attempts, &j.MaxAttempts, &j.RunAfter, &j.StartedAt, &j.FinishedAt,
		&j.FailureCode, &j.LastError, &j.CreatedAt); err != nil {
		return nil, err
	}
	return &j, nil
}

func insertJob(ctx context.Context, tx pgx.Tx, t domain.JobType, deploymentID, chatID uuid.UUID, runAfter time.Time) (*domain.Job, error) {
	return scanJob(tx.QueryRow(ctx, `
		INSERT INTO jobs (id, type, deployment_id, chat_id, status, run_after)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+jobColumns,
		uuid.New(), string(t), deploymentID, chatID, string(domain.JobPending), runAfter))
}

// Schedule queues a job to run at or after a given time.
//
// Auto-teardown is exactly this: a destroy job with run_after set N minutes out.
// No EventBridge rule, no separate scheduler - and unlike an EventBridge rule
// in our account, it can reach into the customer's account via assume-role.
func (s *JobStore) Schedule(ctx context.Context, t domain.JobType, deploymentID, chatID uuid.UUID, runAfter time.Time) (*domain.Job, error) {
	var j *domain.Job
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		j, err = insertJob(ctx, tx, t, deploymentID, chatID, runAfter)
		return err
	})
	return j, err
}

// ErrNoJob signals an idle queue. Not an error condition.
var ErrNoJob = errors.New("no job available")

// Claim atomically takes the next runnable job.
//
// Three things are happening in one statement:
//
//   - FOR UPDATE SKIP LOCKED lets several workers poll the same table without
//     ever handing the same row to two of them.
//   - The NOT EXISTS clause enforces one running job per chat, because a chat
//     has one Terraform state and two concurrent runs against it corrupt it.
//     Different chats still run in parallel.
//   - attempts is incremented on claim rather than on failure, so a worker that
//     dies mid-run still burns an attempt and cannot loop forever.
func (s *JobStore) Claim(ctx context.Context, workerID string) (*domain.Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `
		UPDATE jobs SET
			status       = $2,
			attempts     = attempts + 1,
			started_at   = COALESCE(started_at, now()),
			claimed_by   = $1,
			heartbeat_at = now(),
			updated_at   = now()
		WHERE id = (
			SELECT j.id FROM jobs j
			WHERE j.status = 'pending'
			  AND j.run_after <= now()
			  AND NOT EXISTS (
			      SELECT 1 FROM jobs r
			      WHERE r.chat_id = j.chat_id AND r.status = 'running'
			  )
			ORDER BY j.run_after, j.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+jobColumns, workerID, string(domain.JobRunning)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoJob
		}
		// A unique violation here is the jobs_one_running_per_chat index
		// catching a race the NOT EXISTS subquery could not: it tests for
		// absence, so it has no row to lock. Another worker won; try again.
		if pgErrCode(err) == codeUniqueViolation {
			return nil, ErrNoJob
		}
		return nil, err
	}
	return j, nil
}

// Heartbeat marks a running job as still alive. A job whose heartbeat goes
// stale is assumed to have died with its worker.
func (s *JobStore) Heartbeat(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now() WHERE id = $1 AND status = 'running'`, id)
	return err
}

func (s *JobStore) Succeed(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status = $2, finished_at = now(),
		    failure_code = '', last_error = '', updated_at = now()
		WHERE id = $1`, id, string(domain.JobSucceeded))
	return err
}

// Fail records a failed attempt. When retries remain and the failure is
// retryable it goes back to pending with a backoff; otherwise it is final.
//
// Returns whether the job was requeued, so the caller knows not to mark the
// deployment failed yet.
func (s *JobStore) Fail(ctx context.Context, id uuid.UUID, code, msg string, retryable bool, backoff time.Duration) (requeued bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var attempts, maxAttempts int
		if err := tx.QueryRow(ctx,
			`SELECT attempts, max_attempts FROM jobs WHERE id = $1 FOR UPDATE`, id,
		).Scan(&attempts, &maxAttempts); err != nil {
			return mapErr(err)
		}

		if retryable && attempts < maxAttempts {
			requeued = true
			_, err := tx.Exec(ctx, `
				UPDATE jobs SET status = $5, run_after = now() + $2::interval,
				    claimed_by = '', heartbeat_at = NULL,
				    failure_code = $3, last_error = $4, updated_at = now()
				WHERE id = $1`, id, backoff.String(), code, msg, string(domain.JobPending))
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE jobs SET status = $4, finished_at = now(),
			    failure_code = $2, last_error = $3, updated_at = now()
			WHERE id = $1`, id, code, msg, string(domain.JobFailed))
		return err
	})
	return requeued, err
}

// CancelPending clears queued work for a deployment that will never need it.
//
// Used when a deployment is resolved or superseded: leaving a scheduled destroy
// behind would fire against a chat's shared Terraform state and could tear down
// whatever is live there now.
func (s *JobStore) CancelPending(ctx context.Context, deploymentID uuid.UUID, reason string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status = $3, finished_at = now(),
		    failure_code = 'cancelled', last_error = $2, updated_at = now()
		WHERE deployment_id = $1 AND status = 'pending'`,
		deploymentID, reason, string(domain.JobFailed))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MarkUnknown is used when the outcome genuinely cannot be determined - a
// timeout, an indeterminate AWS error, or a worker that vanished. Never
// retried automatically: re-running an apply whose effect is unknown risks
// creating a second copy of whatever succeeded the first time.
func (s *JobStore) MarkUnknown(ctx context.Context, id uuid.UUID, code, msg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status = $4, finished_at = now(),
		    failure_code = $2, last_error = $3, updated_at = now()
		WHERE id = $1`, id, code, msg, string(domain.JobUnknown))
	return err
}

// StaleJob pairs an abandoned job with the deployment it was working on.
type StaleJob struct {
	JobID        uuid.UUID
	DeploymentID uuid.UUID
	Type         domain.JobType
}

// ReclaimStale finds jobs whose worker stopped reporting in.
//
// Called at startup and periodically. These are NOT marked failed: a worker
// that died during an apply may have left half a VPC behind, and "failed"
// implies nothing was created. They go to unknown, and the deployment with
// them.
func (s *JobStore) ReclaimStale(ctx context.Context, staleAfter time.Duration) ([]StaleJob, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE jobs SET status = $2, finished_at = now(),
		    failure_code = 'worker_lost',
		    last_error = 'the worker running this job stopped reporting; the outcome is unknown',
		    updated_at = now()
		WHERE status = 'running'
		  AND (heartbeat_at IS NULL OR heartbeat_at < now() - $1::interval)
		RETURNING id, deployment_id, type`,
		staleAfter.String(), string(domain.JobUnknown))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StaleJob
	for rows.Next() {
		var sj StaleJob
		if err := rows.Scan(&sj.JobID, &sj.DeploymentID, &sj.Type); err != nil {
			return nil, err
		}
		out = append(out, sj)
	}
	return out, rows.Err()
}

func (s *JobStore) Get(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return j, nil
}

// ListForDeployment returns the job history for a deployment, newest first.
func (s *JobStore) ListForDeployment(ctx context.Context, deploymentID uuid.UUID) ([]domain.Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE deployment_id = $1 ORDER BY created_at DESC`,
		deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Job
	out = []domain.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
