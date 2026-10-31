package trip

import (
	"context"
	"fmt"

	"github.com/Zuneve/template/internal/model"
)

// AddStatusHistory records one status transition of a trip.
func (r *Repository) AddStatusHistory(ctx context.Context, e model.StatusHistoryEntry) error {
	query, args, err := psql.Insert("trip_status_history").
		Columns("trip_id", "from_status", "to_status").
		Values(e.TripID, e.FromStatus, e.ToStatus).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert trip status history query: %w", err)
	}

	if _, err := r.conn(ctx).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("insert trip status history: %w", err)
	}
	return nil
}
