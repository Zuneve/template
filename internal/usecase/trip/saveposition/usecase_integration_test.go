//go:build integration

package saveposition_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/infra/postgres/transaction"
	triprepo "github.com/Zuneve/template/internal/repository/trip"
	"github.com/Zuneve/template/internal/testutil"
	"github.com/Zuneve/template/internal/usecase/trip/saveposition"
)

type noopMetrics struct{}

func (noopMetrics) PositionSaved(context.Context) {}

type noopQueue struct{}

func (noopQueue) Cancel(uuid.UUID) {}

// failingTouch lets AddPosition run for real and then fails the second write
// of the transaction, so the test can see the first one rolled back.
type failingTouch struct{ *triprepo.Repository }

func (failingTouch) TouchLastPosition(context.Context, uuid.UUID, time.Time) error { return errBoom }

// integration keeps the pool out of this package's types (usecase tests do not
// import the driver), so database checks go through closures.
type integration struct {
	repo           *triprepo.Repository
	tx             *transaction.Manager
	cleanup        func(t *testing.T, tripID uuid.UUID)
	lastPositionAt func(t *testing.T, tripID uuid.UUID) *time.Time
	countPositions func(t *testing.T, tripID uuid.UUID) int
}

func setupIntegration(t *testing.T) integration {
	t.Helper()
	pool := testutil.Pool(t)

	return integration{
		repo: triprepo.New(pool),
		tx:   transaction.NewManager(pool),
		cleanup: func(t *testing.T, tripID uuid.UUID) {
			testutil.CleanupTrips(t, pool, tripID)
		},
		lastPositionAt: func(t *testing.T, tripID uuid.UUID) *time.Time {
			t.Helper()

			var at *time.Time
			err := pool.QueryRow(context.Background(), `SELECT last_position_at FROM trips WHERE id = $1`, tripID).Scan(&at)
			require.NoError(t, err)
			if at != nil {
				utc := at.UTC()
				at = &utc
			}
			return at
		},
		countPositions: func(t *testing.T, tripID uuid.UUID) int {
			t.Helper()

			var n int
			err := pool.QueryRow(context.Background(), `SELECT count(*) FROM trip_positions WHERE trip_id = $1`, tripID).Scan(&n)
			require.NoError(t, err)
			return n
		},
	}
}

func (i integration) createTrip(t *testing.T) domain.Trip {
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
	i.cleanup(t, tr.ID)
	require.NoError(t, i.repo.Create(context.Background(), tr))
	return tr
}

func positionInput(tripID uuid.UUID, recordedAt time.Time) saveposition.Input {
	return saveposition.Input{
		TripID:     tripID,
		Point:      domain.Point{Latitude: 59.935, Longitude: 30.335},
		RecordedAt: recordedAt.UTC().Truncate(time.Microsecond),
	}
}

func TestIntegration_SaveUpdatesLastPositionAt(t *testing.T) {
	i := setupIntegration(t)
	ctx := context.Background()
	tr := i.createTrip(t)
	uc := saveposition.New(i.repo, i.repo, i.tx, noopMetrics{}, noopQueue{})
	require.Nil(t, i.lastPositionAt(t, tr.ID))

	newer := time.Now().Add(-time.Minute)
	older := newer.Add(-time.Hour)

	saved, err := uc.Execute(ctx, positionInput(tr.ID, newer))
	require.NoError(t, err)
	require.NotZero(t, saved.ID)
	require.Equal(t, newer.UTC().Truncate(time.Microsecond), *i.lastPositionAt(t, tr.ID))

	// The device was offline and delivers an older point after a newer one.
	_, err = uc.Execute(ctx, positionInput(tr.ID, older))
	require.NoError(t, err)
	require.Equal(t, newer.UTC().Truncate(time.Microsecond), *i.lastPositionAt(t, tr.ID), "a late point must not age the trip")
	require.Equal(t, 2, i.countPositions(t, tr.ID), "the late point is still part of the route")
}

func TestIntegration_SaveRollsBackWhenSecondWriteFails(t *testing.T) {
	i := setupIntegration(t)
	tr := i.createTrip(t)
	uc := saveposition.New(i.repo, failingTouch{i.repo}, i.tx, noopMetrics{}, noopQueue{})

	_, err := uc.Execute(context.Background(), positionInput(tr.ID, time.Now().Add(-time.Minute)))

	require.ErrorIs(t, err, errBoom)
	require.Equal(t, 0, i.countPositions(t, tr.ID), "the inserted point must be rolled back with the failed update")
	require.Nil(t, i.lastPositionAt(t, tr.ID))
}

func TestIntegration_SaveRejectsCompletedTripAndStoresNothing(t *testing.T) {
	i := setupIntegration(t)
	ctx := context.Background()
	tr := i.createTrip(t)
	require.NoError(t, tr.Finish(time.Now().UTC().Truncate(time.Microsecond)))
	require.NoError(t, i.repo.Update(ctx, tr))
	uc := saveposition.New(i.repo, i.repo, i.tx, noopMetrics{}, noopQueue{})

	_, err := uc.Execute(ctx, positionInput(tr.ID, time.Now().Add(-time.Minute)))

	require.ErrorIs(t, err, domain.ErrTripCompleted)
	require.Equal(t, 0, i.countPositions(t, tr.ID))
	require.Nil(t, i.lastPositionAt(t, tr.ID))
}

func TestIntegration_SaveUnknownTrip(t *testing.T) {
	i := setupIntegration(t)
	uc := saveposition.New(i.repo, i.repo, i.tx, noopMetrics{}, noopQueue{})

	_, err := uc.Execute(context.Background(), positionInput(uuid.New(), time.Now().Add(-time.Minute)))

	require.ErrorIs(t, err, domain.ErrTripNotFound)
}
