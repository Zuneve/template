package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Zuneve/template/internal/postgres"
)

type TripStatusHistoryRepository struct {
	pool *pgxpool.Pool
}

func NewTripStatusHistoryRepository(pool *pgxpool.Pool) *TripStatusHistoryRepository {
	return &TripStatusHistoryRepository{pool: pool}
}

func (r *TripStatusHistoryRepository) Insert(ctx context.Context, e StatusHistoryEntry) error {
	query, args, err := psql.Insert("trip_status_history").
		Columns("trip_id", "from_status", "to_status", "reason").
		Values(e.TripID, e.FromStatus, e.ToStatus, e.Reason).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert trip status history query: %w", err)
	}

	if _, err := postgres.ExecutorFromContext(ctx, r.pool).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("insert trip status history: %w", err)
	}
	return nil
}
