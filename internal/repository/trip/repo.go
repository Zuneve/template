package trip

import (
	"context"

	sq "github.com/Masterminds/squirrel"

	"github.com/Zuneve/template/internal/infra/postgres/transaction"
)

const (
	uniqueViolation = "23505"

	// oneActiveTripPerDriverConstraint is the partial unique index (driver_id
	// WHERE status = 'active') that enforces the one-active-trip-per-driver
	// rule; see migrations/20260927181004_create_trips.sql.
	oneActiveTripPerDriverConstraint = "trips_one_active_per_driver_idx"
)

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
