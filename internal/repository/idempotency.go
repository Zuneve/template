package repository

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Zuneve/template/internal/postgres"
)

type IdempotencyRecord struct {
	Key         uuid.UUID
	RequestHash string
	TripID      uuid.UUID
}

type IdempotencyRepository struct {
	pool *pgxpool.Pool
}

func NewIdempotencyRepository(pool *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool}
}

func (r *IdempotencyRepository) executor(ctx context.Context) postgres.Executor {
	return postgres.ExecutorFromContext(ctx, r.pool)
}

// DeleteExpired removes idempotency records older than the retention period.
// Their trips and status history remain intact.
func (r *IdempotencyRepository) DeleteExpired(ctx context.Context) error {
	query, args, err := psql.Delete("idempotency_keys").
		Where("created_at < now() - interval '24 hours'").
		ToSql()
	if err != nil {
		return fmt.Errorf("build delete expired idempotency keys query: %w", err)
	}
	if _, err := r.executor(ctx).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("delete expired idempotency keys: %w", err)
	}
	return nil
}

func (r *IdempotencyRepository) Get(ctx context.Context, key uuid.UUID) (IdempotencyRecord, error) {
	query, args, err := psql.Select("key", "request_hash", "trip_id").
		From("idempotency_keys").
		Where(sq.Eq{"key": key}).
		ToSql()
	if err != nil {
		return IdempotencyRecord{}, fmt.Errorf("build select idempotency key query: %w", err)
	}

	var rec IdempotencyRecord
	err = r.executor(ctx).QueryRow(ctx, query, args...).Scan(&rec.Key, &rec.RequestHash, &rec.TripID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IdempotencyRecord{}, ErrIdempotencyKeyNotFound
		}
		return IdempotencyRecord{}, fmt.Errorf("select idempotency key: %w", err)
	}
	return rec, nil
}

// Insert claims the key for the given trip. A concurrent request racing on
// the same brand-new key loses this insert with ErrIdempotencyKeyConflict;
// the caller resolves it the same way as a replay of an already-known key.
func (r *IdempotencyRepository) Insert(ctx context.Context, rec IdempotencyRecord) error {
	query, args, err := psql.Insert("idempotency_keys").
		Columns("key", "request_hash", "trip_id").
		Values(rec.Key, rec.RequestHash, rec.TripID).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert idempotency key query: %w", err)
	}

	if _, err := r.executor(ctx).Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrIdempotencyKeyConflict
		}
		return fmt.Errorf("insert idempotency key: %w", err)
	}
	return nil
}
