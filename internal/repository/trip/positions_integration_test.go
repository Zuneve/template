//go:build integration

package trip_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/testutil"
)

var baseTime = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

func lastPositionAt(t *testing.T, pool *pgxpool.Pool, tripID uuid.UUID) *time.Time {
	t.Helper()
	var at *time.Time
	err := pool.QueryRow(context.Background(), `SELECT last_position_at FROM trips WHERE id = $1`, tripID).Scan(&at)
	require.NoError(t, err)
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at
}

func TestAddPositionAndListPositions(t *testing.T) {
	r, pool := setup(t)
	ctx := context.Background()
	tr := newTrip(t)
	testutil.CleanupTrips(t, pool, tr.ID)
	require.NoError(t, r.Create(ctx, tr))

	// Stored out of order: the route must come back by recorded_at, then id.
	late, err := r.AddPosition(ctx, domain.Position{TripID: tr.ID, Point: domain.Point{Latitude: 1, Longitude: 1}, RecordedAt: baseTime.Add(2 * time.Minute)})
	require.NoError(t, err)
	early, err := r.AddPosition(ctx, domain.Position{TripID: tr.ID, Point: domain.Point{Latitude: 2, Longitude: 2}, RecordedAt: baseTime})
	require.NoError(t, err)
	sameTimeFirst, err := r.AddPosition(ctx, domain.Position{TripID: tr.ID, Point: domain.Point{Latitude: 3, Longitude: 3}, RecordedAt: baseTime.Add(time.Minute)})
	require.NoError(t, err)
	sameTimeSecond, err := r.AddPosition(ctx, domain.Position{TripID: tr.ID, Point: domain.Point{Latitude: 4, Longitude: 4}, RecordedAt: baseTime.Add(time.Minute)})
	require.NoError(t, err)
	require.NotZero(t, late.ID)
	require.Less(t, sameTimeFirst.ID, sameTimeSecond.ID)

	got, err := r.ListPositions(ctx, tr.ID)
	require.NoError(t, err)
	require.Len(t, got, 4)
	for i := range got {
		got[i].RecordedAt = got[i].RecordedAt.UTC()
	}
	require.Equal(t, []domain.Position{early, sameTimeFirst, sameTimeSecond, late}, got)
}

func TestListPositions_NoPoints(t *testing.T) {
	r, _ := setup(t)

	got, err := r.ListPositions(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got, "an empty route is an empty list, not nil")
	require.Empty(t, got)
}

func TestAddPosition_TripNotFound(t *testing.T) {
	r, _ := setup(t)

	_, err := r.AddPosition(context.Background(), domain.Position{TripID: uuid.New(), RecordedAt: baseTime})
	require.ErrorIs(t, err, domain.ErrTripNotFound)
}

func TestTouchLastPosition(t *testing.T) {
	r, pool := setup(t)
	ctx := context.Background()
	tr := newTrip(t)
	testutil.CleanupTrips(t, pool, tr.ID)
	require.NoError(t, r.Create(ctx, tr))
	require.Nil(t, lastPositionAt(t, pool, tr.ID), "no points yet")

	require.NoError(t, r.TouchLastPosition(ctx, tr.ID, baseTime.Add(time.Minute)))
	require.Equal(t, baseTime.Add(time.Minute), *lastPositionAt(t, pool, tr.ID), "the first point sets it")

	require.NoError(t, r.TouchLastPosition(ctx, tr.ID, baseTime.Add(2*time.Minute)))
	require.Equal(t, baseTime.Add(2*time.Minute), *lastPositionAt(t, pool, tr.ID), "a newer point moves it forward")

	require.NoError(t, r.TouchLastPosition(ctx, tr.ID, baseTime))
	require.Equal(t, baseTime.Add(2*time.Minute), *lastPositionAt(t, pool, tr.ID), "a late, older point must not make the trip look stale")
}

func TestTouchLastPosition_TripNotFound(t *testing.T) {
	r, _ := setup(t)

	err := r.TouchLastPosition(context.Background(), uuid.New(), baseTime)
	require.ErrorIs(t, err, domain.ErrTripNotFound)
}
