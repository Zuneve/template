package idempotency

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/infra/postgres/transaction"
	"github.com/Zuneve/template/internal/model"
)

const uniqueViolation = "23505"

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

type Repository struct {
	db executor
}

func New(db executor) *Repository {
	return &Repository{db: db}
}

// conn returns the transaction from ctx if there is one, otherwise the pool.
func (r *Repository) conn(ctx context.Context) executor {
	if tx, ok := transaction.TxFromContext(ctx); ok {
		return tx
	}
	return r.db
}

// DeleteExpired removes idempotency records older than the retention period.
// Their trips and status history remain intact.
func (r *Repository) DeleteExpired(ctx context.Context) error {
	query, args, err := psql.Delete("idempotency_keys").
		Where("created_at < now() - interval '24 hours'").
		ToSql()
	if err != nil {
		return fmt.Errorf("build delete expired idempotency keys query: %w", err)
	}
	if _, err := r.conn(ctx).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("delete expired idempotency keys: %w", err)
	}
	return nil
}

// Get returns the record for key or domain.ErrIdempotencyKeyNotFound.
func (r *Repository) Get(ctx context.Context, key uuid.UUID) (model.IdempotencyRecord, error) {
	query, args, err := psql.Select("key", "request_hash", "trip_id").
		From("idempotency_keys").
		Where(sq.Eq{"key": key}).
		ToSql()
	if err != nil {
		return model.IdempotencyRecord{}, fmt.Errorf("build select idempotency key query: %w", err)
	}

	var rec model.IdempotencyRecord
	err = r.conn(ctx).QueryRow(ctx, query, args...).Scan(&rec.Key, &rec.RequestHash, &rec.TripID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.IdempotencyRecord{}, domain.ErrIdempotencyKeyNotFound
		}
		return model.IdempotencyRecord{}, fmt.Errorf("select idempotency key: %w", err)
	}
	return rec, nil
}

// Insert claims the key for the given trip. A concurrent request racing on
// the same brand-new key loses this insert with domain.ErrIdempotencyKeyConflict.
func (r *Repository) Insert(ctx context.Context, rec model.IdempotencyRecord) error {
	query, args, err := psql.Insert("idempotency_keys").
		Columns("key", "request_hash", "trip_id").
		Values(rec.Key, rec.RequestHash, rec.TripID).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert idempotency key query: %w", err)
	}

	if _, err := r.conn(ctx).Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return domain.ErrIdempotencyKeyConflict
		}
		return fmt.Errorf("insert idempotency key: %w", err)
	}
	return nil
}
