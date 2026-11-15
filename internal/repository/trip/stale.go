package trip

import (
	"context"
	"fmt"
	"time"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

// staleSince is how long ago a trip last gave a sign of life: its newest point,
// or its start if it has none. The expression must stay identical to the one in
// migrations/20261115200100_add_trips_stale_idx.sql, or the planner cannot use
// that index.
const staleSince = "COALESCE(last_position_at, started_at)"

// ListStaleTrips returns the active trips that have been silent since before
// staleBefore, the oldest silence first. A trip that never sent a point counts
// from started_at, so a trip nobody tracks is not lost. One query, no per-trip
// lookups: the scanner calls it every tick.
func (r *Repository) ListStaleTrips(ctx context.Context, staleBefore time.Time) ([]domain.StaleTrip, error) {
	query, args, err := psql.Select("id", "driver_id").
		From("trips").
		Where("status = 'active'").
		Where(staleSince+" < ?", staleBefore).
		OrderBy(staleSince+" ASC", "id ASC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build select stale trips query: %w", err)
	}

	rows, err := r.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select stale trips: %w", err)
	}
	defer rows.Close()

	stale := []domain.StaleTrip{}
	for rows.Next() {
		var st domain.StaleTrip
		if err := rows.Scan(&st.TripID, &st.DriverID); err != nil {
			return nil, fmt.Errorf("scan stale trip: %w", err)
		}
		stale = append(stale, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read stale trips: %w", err)
	}
	return stale, nil
}
