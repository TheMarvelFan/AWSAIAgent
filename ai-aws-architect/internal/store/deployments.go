package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

type DeploymentStore struct{ pool *pgxpool.Pool }

func NewDeploymentStore(pool *pgxpool.Pool) *DeploymentStore {
	return &DeploymentStore{pool: pool}
}

const deploymentColumns = `id, chat_id, user_id, config_version, status, plan_hash, plan_output,
	plan_summary, approved_at, aws_account_id, region, state_key, failure_code,
	failure_message, teardown_after, applied_at, destroyed_at, created_at, updated_at`

func scanDeployment(row rowScanner) (*domain.Deployment, error) {
	var d domain.Deployment
	var summary []byte
	if err := row.Scan(&d.ID, &d.ChatID, &d.UserID, &d.ConfigVersion, &d.Status, &d.PlanHash,
		&d.PlanOutput, &summary, &d.ApprovedAt, &d.AWSAccountID, &d.Region,
		&d.StateKey, &d.FailureCode, &d.FailureMessage, &d.TeardownAfter,
		&d.AppliedAt, &d.DestroyedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.PlanSummary = summary
	d.LiveResources = d.Status.MayHaveLiveResources()
	return &d, nil
}

type CreateDeploymentParams struct {
	ChatID        uuid.UUID
	UserID        uuid.UUID
	ConfigVersion int
	AWSAccountID  string
	AWSRoleARN    string
	Region        string
	StateKey      string
}

// Create opens a deployment in the planning state and queues its plan job in
// the same transaction, so a deployment can never exist with no work scheduled.
//
// Any earlier deployment for this chat that was still awaiting approval is
// canceled: two pending approvals for one chat is a confusing thing to show a
// user, and only one can ever be applied anyway.
func (s *DeploymentStore) Create(ctx context.Context, p CreateDeploymentParams) (*domain.Deployment, *domain.Job, error) {
	var dep *domain.Deployment
	var job *domain.Job

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Lock the chat so two concurrent plan requests cannot both pass the
		// in-flight check below.
		var scratch int64
		if err := tx.QueryRow(ctx,
			`SELECT message_seq FROM chats WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			p.ChatID, p.UserID).Scan(&scratch); err != nil {
			return mapErr(err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE deployments SET status = 'cancelled', updated_at = now()
			WHERE chat_id = $1 AND status = 'awaiting_approval'`, p.ChatID); err != nil {
			return err
		}

		// The partial unique index is the real guard; this check exists to turn
		// a constraint violation into a readable error.
		var inflight int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM deployments
			WHERE chat_id = $1
			  AND status IN ('planning', 'awaiting_approval', 'applying', 'destroying')`,
			p.ChatID).Scan(&inflight); err != nil {
			return err
		}
		if inflight > 0 {
			return domain.ErrConflict
		}

		id := uuid.New()
		row := tx.QueryRow(ctx, `
			INSERT INTO deployments
				(id, chat_id, user_id, config_version, status, aws_account_id,
				 aws_role_arn, region, state_key)
			VALUES ($1, $2, $3, $4, 'planning', $5, $6, $7, $8)
			RETURNING `+deploymentColumns,
			id, p.ChatID, p.UserID, p.ConfigVersion, p.AWSAccountID,
			p.AWSRoleARN, p.Region, p.StateKey)
		var err error
		if dep, err = scanDeployment(row); err != nil {
			return err
		}

		job, err = insertJob(ctx, tx, domain.JobPlan, dep.ID, p.ChatID, time.Now())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return dep, job, nil
}

func (s *DeploymentStore) Get(ctx context.Context, userID, id uuid.UUID) (*domain.Deployment, error) {
	d, err := scanDeployment(s.pool.QueryRow(ctx, `
		SELECT `+deploymentColumns+`
		FROM deployments WHERE id = $1 AND user_id = $2`, id, userID))
	if err != nil {
		return nil, mapErr(err)
	}
	return d, nil
}

// GetUnscoped reads a deployment without an ownership predicate.
//
// For the worker only, which processes jobs with no request and therefore no
// authenticated user. Every HTTP path must use Get, which scopes by user_id.
func (s *DeploymentStore) GetUnscoped(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	d, err := scanDeployment(s.pool.QueryRow(ctx, `
		SELECT `+deploymentColumns+`
		FROM deployments WHERE id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return d, nil
}

// ForUpdate reads a deployment inside a transaction with its row locked.
func deploymentForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Deployment, error) {
	d, err := scanDeployment(tx.QueryRow(ctx, `
		SELECT `+deploymentColumns+`
		FROM deployments WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return d, nil
}

// ListForChat returns deployment history, newest first.
func (s *DeploymentStore) ListForChat(ctx context.Context, userID, chatID uuid.UUID, limit int) ([]domain.Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+deploymentColumns+`
		FROM deployments WHERE chat_id = $1 AND user_id = $2
		ORDER BY created_at DESC LIMIT $3`, chatID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Deployment
	out = []domain.Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// Live returns deployments that might still be billing. Used by the disconnect
// warning and by teardown reporting.
func (s *DeploymentStore) Live(ctx context.Context, userID uuid.UUID) ([]domain.Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+deploymentColumns+`
		FROM deployments
		WHERE user_id = $1
		  AND status IN ('applied', 'apply_failed', 'destroy_failed', 'unknown', 'destroying')
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Deployment
	out = []domain.Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeployedVersion answers "what config version is actually live in this chat",
// which is a different question from chats.current_config_version.
func (s *DeploymentStore) DeployedVersion(ctx context.Context, chatID uuid.UUID) (*int, error) {
	var v int
	err := s.pool.QueryRow(ctx, `
		SELECT config_version FROM deployments
		WHERE chat_id = $1 AND status = 'applied'
		ORDER BY applied_at DESC LIMIT 1`, chatID).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

// WorkspaceRetainIDs lists deployments whose working directory must not be
// deleted: anything awaiting approval holds a saved plan that apply will run,
// and anything in flight may have a job using the directory right now.
func (s *DeploymentStore) WorkspaceRetainIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM deployments
		WHERE status IN ('planning', 'awaiting_approval', 'applying', 'destroying')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type TransitionParams struct {
	FailureCode    string
	FailureMessage string
	PlanHash       string
	PlanOutput     string
	PlanSummary    json.RawMessage
	Approve        bool
	ApprovedBy     *uuid.UUID
	TeardownAfter  *time.Time
	MarkApplied    bool
	MarkDestroyed  bool
}

// Transition moves a deployment to a new status, refusing illegal moves.
//
// Every status write goes through here. The alternative - each call site
// issuing its own UPDATE - is how a deployment ends up marked destroyed by a
// code path that never ran a "destroy".
func (s *DeploymentStore) Transition(ctx context.Context, id uuid.UUID, to domain.DeploymentStatus, p TransitionParams) (*domain.Deployment, error) {
	var out *domain.Deployment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := deploymentForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status == to {
			out = cur
			return nil // idempotent
		}
		if !domain.CanTransition(cur.Status, to) {
			return domain.ErrIllegalTransition(cur.Status, to)
		}

		// A newly applied deployment supersedes whatever was live before. Same
		// infrastructure, new config version - the old row records history.
		if to == domain.DeployApplied {
			if _, err := tx.Exec(ctx, `
				UPDATE deployments SET status = 'superseded', updated_at = now()
				WHERE chat_id = $1 AND id <> $2 AND status = 'applied'`,
				cur.ChatID, id); err != nil {
				return err
			}
		}

		row := tx.QueryRow(ctx, `
			UPDATE deployments SET
				status          = $2,
				failure_code    = COALESCE(NULLIF($3, ''), failure_code),
				failure_message = COALESCE(NULLIF($4, ''), failure_message),
				plan_hash       = COALESCE(NULLIF($5, ''), plan_hash),
				plan_output     = COALESCE(NULLIF($6, ''), plan_output),
				plan_summary    = COALESCE($7::jsonb, plan_summary),
				approved_at     = CASE WHEN $8::bool THEN now() ELSE approved_at END,
				approved_by     = COALESCE($9, approved_by),
				teardown_after  = COALESCE($10, teardown_after),
				applied_at      = CASE WHEN $11::bool THEN now() ELSE applied_at END,
				destroyed_at    = CASE WHEN $12::bool THEN now() ELSE destroyed_at END,
				updated_at      = now()
			WHERE id = $1
			RETURNING `+deploymentColumns,
			id, string(to), p.FailureCode, p.FailureMessage, p.PlanHash, p.PlanOutput,
			nullableJSON(p.PlanSummary), p.Approve, p.ApprovedBy, p.TeardownAfter,
			p.MarkApplied, p.MarkDestroyed)
		out, err = scanDeployment(row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ApproveAndQueueApply is the approval gate, enforced as one atomic step.
//
// planHash must match what the caller was shown. Without that check there is a
// window between a human reading a plan and the apply firing in which the plan
// could change - the classic time-of-check to time-of-use problem, and a
// serious one when the outcome is real infrastructure.
func (s *DeploymentStore) ApproveAndQueueApply(ctx context.Context, userID, id uuid.UUID, planHash string) (*domain.Deployment, *domain.Job, error) {
	var dep *domain.Deployment
	var job *domain.Job

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := deploymentForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status != domain.DeployAwaitingApproval {
			// Also the double-click guard: the second request finds a status
			// that is no longer awaiting approval and is refused.
			return domain.ErrIllegalTransition(cur.Status, domain.DeployApplying)
		}
		if cur.PlanHash == "" || cur.PlanHash != planHash {
			return domain.ErrStaleConfig
		}

		row := tx.QueryRow(ctx, `
			UPDATE deployments
			SET status = 'applying', approved_at = now(), approved_by = $2, updated_at = now()
			WHERE id = $1
			RETURNING `+deploymentColumns, id, userID)
		if dep, err = scanDeployment(row); err != nil {
			return err
		}

		job, err = insertJob(ctx, tx, domain.JobApply, dep.ID, dep.ChatID, time.Now())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return dep, job, nil
}

func nullableJSON(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	return []byte(v)
}
