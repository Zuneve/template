//go:build integration

package idempotency_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/model"
	"github.com/Zuneve/template/internal/repository/idempotency"
	tripRepo "github.com/Zuneve/template/internal/repository/trip"
	"github.com/Zuneve/template/internal/testutil"
)

// existingTrip creates a trip, because idempotency_keys.trip_id references trips.
func existingTrip(t *testing.T, r *tripRepo.Repository) uuid.UUID {
	t.Helper()

	tr, err := domain.New(domain.NewParams{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DriverID:   uuid.New(),
		StartPoint: domain.Point{Latitude: 1, Longitude: 2},
		EndPoint:   domain.Point{Latitude: 3, Longitude: 4},
		StartedAt:  time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, r.Create(context.Background(), tr))
	return tr.ID
}

func TestInsertAndGet(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	trips := tripRepo.New(pool)
	r := idempotency.New(pool)
	tripID := existingTrip(t, trips)
	testutil.CleanupTrips(t, pool, tripID)
	rec := model.IdempotencyRecord{Key: uuid.New(), RequestHash: "hash", TripID: tripID}

	require.NoError(t, r.Insert(ctx, rec))

	got, err := r.Get(ctx, rec.Key)
	require.NoError(t, err)
	require.Equal(t, rec, got)
}

func TestGet_NotFound(t *testing.T) {
	r := idempotency.New(testutil.Pool(t))

	_, err := r.Get(context.Background(), uuid.New())

	require.ErrorIs(t, err, domain.ErrIdempotencyKeyNotFound)
}

func TestInsert_SameKeyTwice(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	trips := tripRepo.New(pool)
	r := idempotency.New(pool)
	tripID := existingTrip(t, trips)
	testutil.CleanupTrips(t, pool, tripID)
	rec := model.IdempotencyRecord{Key: uuid.New(), RequestHash: "hash", TripID: tripID}
	require.NoError(t, r.Insert(ctx, rec))

	err := r.Insert(ctx, rec)

	require.ErrorIs(t, err, domain.ErrIdempotencyKeyConflict)
}

func TestDeleteExpired(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	trips := tripRepo.New(pool)
	r := idempotency.New(pool)
	tripID := existingTrip(t, trips)
	testutil.CleanupTrips(t, pool, tripID)
	old := model.IdempotencyRecord{Key: uuid.New(), RequestHash: "old", TripID: tripID}
	fresh := model.IdempotencyRecord{Key: uuid.New(), RequestHash: "fresh", TripID: tripID}
	require.NoError(t, r.Insert(ctx, old))
	require.NoError(t, r.Insert(ctx, fresh))
	_, err := pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours' WHERE key = $1`, old.Key)
	require.NoError(t, err)

	require.NoError(t, r.DeleteExpired(ctx))

	_, err = r.Get(ctx, old.Key)
	require.ErrorIs(t, err, domain.ErrIdempotencyKeyNotFound)
	_, err = r.Get(ctx, fresh.Key)
	require.NoError(t, err)
}
