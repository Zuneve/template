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

// oneActiveTripPerDriverConstraint is the partial unique index (driver_id
// WHERE status = 'active') that enforces the one-active-trip-per-driver rule
// at the database level; see migrations/20260927181004_create_trips.sql.
const oneActiveTripPerDriverConstraint = "trips_one_active_per_driver_idx"

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

const uniqueViolation = "23505"

type TripRepository struct {
	pool *pgxpool.Pool
}

func NewTripRepository(pool *pgxpool.Pool) *TripRepository {
	return &TripRepository{pool: pool}
}

func (r *TripRepository) executor(ctx context.Context) postgres.Executor {
	return postgres.ExecutorFromContext(ctx, r.pool)
}

func (r *TripRepository) Create(ctx context.Context, t Trip) error {
	query, args, err := psql.Insert("trips").
		Columns(
			"id", "user_id", "driver_id",
			"start_latitude", "start_longitude", "end_latitude", "end_longitude",
			"price", "status", "started_at",
		).
		Values(
			t.ID, t.UserID, t.DriverID,
			t.StartPoint.Latitude, t.StartPoint.Longitude, t.EndPoint.Latitude, t.EndPoint.Longitude,
			t.Price, t.Status, t.StartedAt,
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert trip query: %w", err)
	}

	if _, err := r.executor(ctx).Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == oneActiveTripPerDriverConstraint {
			return ErrDriverBusy
		}
		return fmt.Errorf("insert trip: %w", err)
	}
	return nil
}

func (r *TripRepository) GetByID(ctx context.Context, id uuid.UUID) (Trip, error) {
	query, args, err := psql.Select(
		"id", "user_id", "driver_id",
		"start_latitude", "start_longitude", "end_latitude", "end_longitude",
		"price", "status", "started_at", "finished_at",
	).From("trips").Where(sq.Eq{"id": id}).ToSql()
	if err != nil {
		return Trip{}, fmt.Errorf("build select trip query: %w", err)
	}

	t, err := scanTrip(r.executor(ctx).QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Trip{}, ErrTripNotFound
		}
		return Trip{}, fmt.Errorf("select trip: %w", err)
	}
	return t, nil
}

// Finish transitions the trip from active to completed in a single
// statement that checks the current status in the WHERE clause, so a
// concurrent finish can't race a read-then-write. Zero rows affected means
// either the trip doesn't exist or it's already completed; a follow-up read
// tells the two apart.
func (r *TripRepository) Finish(ctx context.Context, id uuid.UUID) (Trip, error) {
	query, args, err := psql.Update("trips").
		Set("status", TripStatusCompleted).
		Set("finished_at", sq.Expr("now()")).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id, "status": TripStatusActive}).
		Suffix(`RETURNING id, user_id, driver_id,
			start_latitude, start_longitude, end_latitude, end_longitude,
			price, status, started_at, finished_at`).
		ToSql()
	if err != nil {
		return Trip{}, fmt.Errorf("build finish trip query: %w", err)
	}

	t, err := scanTrip(r.executor(ctx).QueryRow(ctx, query, args...))
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Trip{}, fmt.Errorf("finish trip: %w", err)
	}

	if _, getErr := r.GetByID(ctx, id); getErr != nil {
		return Trip{}, getErr
	}
	return Trip{}, ErrTripCompleted
}

func scanTrip(row pgx.Row) (Trip, error) {
	var t Trip
	err := row.Scan(
		&t.ID, &t.UserID, &t.DriverID,
		&t.StartPoint.Latitude, &t.StartPoint.Longitude, &t.EndPoint.Latitude, &t.EndPoint.Longitude,
		&t.Price, &t.Status, &t.StartedAt, &t.FinishedAt,
	)
	return t, err
}
