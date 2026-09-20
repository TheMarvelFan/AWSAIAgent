// Package store is the only package that talks to Postgres. Every query is
// scoped by user_id where a user owns the row, so authorization is enforced in
// the WHERE clause rather than in handler code.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cloud-ai/ai-aws-architect/internal/domain"
)

type PoolOptions struct {
	MaxConns int32
	MinConns int32
}

// Connect builds a pool and verifies it before returning, so a bad
// DATABASE_URL fails at boot instead of on the first request.
func Connect(ctx context.Context, url string, opts PoolOptions) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}
	if opts.MinConns > 0 {
		cfg.MinConns = opts.MinConns
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// pgErrCode extracts the SQLSTATE from a pgx error, if there is one.
func pgErrCode(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}

const (
	codeUniqueViolation = "23505"
)

// mapErr converts pgx plumbing errors into domain errors so callers never have
// to import pgx to check "was this a 404".
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if pgErrCode(err) == codeUniqueViolation {
		return domain.ErrConflict
	}
	return err
}
