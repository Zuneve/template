package trip

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

// GetByID returns the trip or domain.ErrTripNotFound.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	return r.fetch(ctx, id, "")
}

// GetByIDForUpdate is GetByID that also locks the row until the end of the
// surrounding transaction, so a concurrent finish waits instead of racing.
func (r *Repository) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	return r.fetch(ctx, id, "FOR UPDATE")
}

func (r *Repository) fetch(ctx context.Context, id uuid.UUID, suffix string) (domain.Trip, error) {
	query, args, err := psql.Select(tripColumns...).
		From("trips").
		Where(sq.Eq{"id": id}).
		Suffix(suffix).
		ToSql()
	if err != nil {
		return domain.Trip{}, fmt.Errorf("build select trip query: %w", err)
	}

	t, err := scanTrip(r.conn(ctx).QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Trip{}, domain.ErrTripNotFound
		}
		return domain.Trip{}, fmt.Errorf("select trip: %w", err)
	}
	return t, nil
}

// CountActive returns how many trips are currently in the active status.
func (r *Repository) CountActive(ctx context.Context) (int64, error) {
	query, args, err := psql.Select("count(*)").
		From("trips").
		Where(sq.Eq{"status": string(domain.StatusActive)}).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("build count active trips query: %w", err)
	}

	var n int64
	if err := r.conn(ctx).QueryRow(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active trips: %w", err)
	}
	return n, nil
}
