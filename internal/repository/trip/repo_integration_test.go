//go:build integration

package trip_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/infra/postgres/transaction"
	"github.com/Zuneve/template/internal/model"
	repo "github.com/Zuneve/template/internal/repository/trip"
	"github.com/Zuneve/template/internal/testutil"
)

// newTrip builds an active trip for a fresh driver; the database stores
// microseconds, so the time is truncated to compare equal after a round trip.
func newTrip(t *testing.T) domain.Trip {
	t.Helper()

	tr, err := domain.New(domain.NewParams{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DriverID:   uuid.New(),
		StartPoint: domain.Point{Latitude: 55.75, Longitude: 37.61},
		EndPoint:   domain.Point{Latitude: 59.93, Longitude: 30.33},
		Price:      1500,
		StartedAt:  time.Now().UTC().Truncate(time.Microsecond),
	})
	require.NoError(t, err)
	return tr
}

func setup(t *testing.T) (*repo.Repository, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.Pool(t)
	return repo.New(pool), pool
}

func fromUTC(tr domain.Trip) domain.Trip {
	tr.StartedAt = tr.StartedAt.UTC()
	if tr.FinishedAt != nil {
		f := tr.FinishedAt.UTC()
		tr.FinishedAt = &f
	}
	return tr
}

func TestCreateAndGetByID(t *testing.T) {
	r, pool := setup(t)
	ctx := context.Background()
	tr := newTrip(t)
	testutil.CleanupTrips(t, pool, tr.ID)

	require.NoError(t, r.Create(ctx, tr))

	got, err := r.GetByID(ctx, tr.ID)
	require.NoError(t, err)
	require.Equal(t, tr, fromUTC(got))
}

func TestCreate_DriverBusy(t *testing.T) {
	r, pool := setup(t)
	ctx := context.Background()
	first := newTrip(t)
	second := newTrip(t)
	second.DriverID = first.DriverID
	testutil.CleanupTrips(t, pool, first.ID, second.ID)

	require.NoError(t, r.Create(ctx, first))

	err := r.Create(ctx, second)
	require.ErrorIs(t, err, domain.ErrDriverBusy)

	require.NoError(t, first.Finish(time.Now().UTC().Truncate(time.Microsecond)))
	require.NoError(t, r.Update(ctx, first))
	require.NoError(t, r.Create(ctx, second), "a driver is free again once the trip is completed")
}

func TestGetByID_NotFound(t *testing.T) {
	r, _ := setup(t)

	_, err := r.GetByID(context.Background(), uuid.New())

	require.ErrorIs(t, err, domain.ErrTripNotFound)
}

func TestUpdate(t *testing.T) {
	t.Run("stores the finished trip", func(t *testing.T) {
		r, pool := setup(t)
		ctx := context.Background()
		tr := newTrip(t)
		testutil.CleanupTrips(t, pool, tr.ID)
		require.NoError(t, r.Create(ctx, tr))

		locked, err := r.GetByIDForUpdate(ctx, tr.ID)
		require.NoError(t, err)
		require.NoError(t, locked.Finish(time.Now().UTC().Truncate(time.Microsecond)))
		require.NoError(t, r.Update(ctx, locked))

		got, err := r.GetByID(ctx, tr.ID)
		require.NoError(t, err)
		require.Equal(t, domain.StatusCompleted, got.Status)
		require.Equal(t, fromUTC(locked), fromUTC(got))
	})

	t.Run("unknown trip", func(t *testing.T) {
		r, _ := setup(t)
		tr := newTrip(t)

		err := r.Update(context.Background(), tr)

		require.ErrorIs(t, err, domain.ErrTripNotFound)
	})
}

func TestGetByIDForUpdate_NotFound(t *testing.T) {
	r, _ := setup(t)

	err := transaction.NewManager(testutil.Pool(t)).Do(context.Background(), func(ctx context.Context) error {
		_, err := r.GetByIDForUpdate(ctx, uuid.New())
		return err
	})

	require.ErrorIs(t, err, domain.ErrTripNotFound)
}

func TestAddStatusHistory(t *testing.T) {
	r, pool := setup(t)
	ctx := context.Background()
	tr := newTrip(t)
	testutil.CleanupTrips(t, pool, tr.ID)
	require.NoError(t, r.Create(ctx, tr))
	active := domain.StatusActive

	require.NoError(t, r.AddStatusHistory(ctx, model.StatusHistoryEntry{TripID: tr.ID, ToStatus: domain.StatusActive}))
	require.NoError(t, r.AddStatusHistory(ctx, model.StatusHistoryEntry{TripID: tr.ID, FromStatus: &active, ToStatus: domain.StatusCompleted}))

	rows, err := pool.Query(ctx, `SELECT from_status, to_status FROM trip_status_history WHERE trip_id = $1 ORDER BY id`, tr.ID)
	require.NoError(t, err)
	defer rows.Close()

	type row struct {
		from *string
		to   string
	}
	var got []row
	for rows.Next() {
		var x row
		require.NoError(t, rows.Scan(&x.from, &x.to))
		got = append(got, x)
	}
	require.NoError(t, rows.Err())

	require.Len(t, got, 2)
	require.Nil(t, got[0].from)
	require.Equal(t, "active", got[0].to)
	require.Equal(t, "active", *got[1].from)
	require.Equal(t, "completed", got[1].to)
}

func TestTransaction_CommitsBothInserts(t *testing.T) {
	r, pool := setup(t)
	ctx := context.Background()
	tr := newTrip(t)
	testutil.CleanupTrips(t, pool, tr.ID)

	err := transaction.NewManager(pool).Do(ctx, func(ctx context.Context) error {
		if err := r.Create(ctx, tr); err != nil {
			return err
		}
		return r.AddStatusHistory(ctx, model.StatusHistoryEntry{TripID: tr.ID, ToStatus: domain.StatusActive})
	})
	require.NoError(t, err)

	_, err = r.GetByID(ctx, tr.ID)
	require.NoError(t, err)
	require.Equal(t, 1, countHistory(t, pool, tr.ID))
}

// The second INSERT violates a CHECK constraint, so the first one must be
// rolled back with it: no trip row and no history row may remain.
func TestTransaction_RollsBackBothInserts(t *testing.T) {
	r, pool := setup(t)
	ctx := context.Background()
	tr := newTrip(t)
	testutil.CleanupTrips(t, pool, tr.ID)

	err := transaction.NewManager(pool).Do(ctx, func(ctx context.Context) error {
		if err := r.Create(ctx, tr); err != nil {
			return err
		}
		return r.AddStatusHistory(ctx, model.StatusHistoryEntry{TripID: tr.ID, ToStatus: "no-such-status"})
	})
	require.Error(t, err)
	require.False(t, errors.Is(err, domain.ErrDriverBusy))

	_, err = r.GetByID(ctx, tr.ID)
	require.ErrorIs(t, err, domain.ErrTripNotFound)
	require.Equal(t, 0, countHistory(t, pool, tr.ID))
}

func countHistory(t *testing.T, pool *pgxpool.Pool, tripID uuid.UUID) int {
	t.Helper()

	var n int
	err := pool.QueryRow(context.Background(), `SELECT count(*) FROM trip_status_history WHERE trip_id = $1`, tripID).Scan(&n)
	require.NoError(t, err)
	return n
}
