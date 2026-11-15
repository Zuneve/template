package trip

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

// AddPosition stores one point of the trip's route and returns it with the
// generated id. A trip that does not exist gives domain.ErrTripNotFound.
func (r *Repository) AddPosition(ctx context.Context, p domain.Position) (domain.Position, error) {
	query, args, err := psql.Insert("trip_positions").
		Columns("trip_id", "latitude", "longitude", "recorded_at").
		Values(p.TripID, p.Point.Latitude, p.Point.Longitude, p.RecordedAt).
		Suffix("RETURNING id").
		ToSql()
	if err != nil {
		return domain.Position{}, fmt.Errorf("build insert trip position query: %w", err)
	}

	if err := r.conn(ctx).QueryRow(ctx, query, args...).Scan(&p.ID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return domain.Position{}, domain.ErrTripNotFound
		}
		return domain.Position{}, fmt.Errorf("insert trip position: %w", err)
	}
	return p, nil
}

// TouchLastPosition moves trips.last_position_at forward to recordedAt. The
// comparison happens inside the UPDATE: GREATEST keeps the newer of the stored
// and the new value (and ignores NULL, so the first point simply sets it). A
// late point recorded before the current last_position_at therefore leaves the
// trip as fresh as it was, with no read-then-write window for a race.
func (r *Repository) TouchLastPosition(ctx context.Context, tripID uuid.UUID, recordedAt time.Time) error {
	query, args, err := psql.Update("trips").
		Set("last_position_at", sq.Expr("GREATEST(last_position_at, ?)", recordedAt)).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": tripID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("build update last position query: %w", err)
	}

	tag, err := r.conn(ctx).Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update last position: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTripNotFound
	}
	return nil
}

// ListPositions returns the whole route of the trip ordered by recorded_at,
// then id. A trip without points, or an unknown one, gives an empty slice.
func (r *Repository) ListPositions(ctx context.Context, tripID uuid.UUID) ([]domain.Position, error) {
	query, args, err := psql.Select("id", "trip_id", "latitude", "longitude", "recorded_at").
		From("trip_positions").
		Where(sq.Eq{"trip_id": tripID}).
		OrderBy("recorded_at ASC", "id ASC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build select trip positions query: %w", err)
	}

	rows, err := r.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select trip positions: %w", err)
	}
	defer rows.Close()

	positions := []domain.Position{}
	for rows.Next() {
		var p domain.Position
		if err := rows.Scan(&p.ID, &p.TripID, &p.Point.Latitude, &p.Point.Longitude, &p.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan trip position: %w", err)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read trip positions: %w", err)
	}
	return positions, nil
}
