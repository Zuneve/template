package finish_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/model"
	"github.com/Zuneve/template/internal/usecase/trip/finish"
)

var (
	tripID  = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	errBoom = errors.New("boom")
)

type fixture struct {
	trips   *MocktripRepository
	history *MockhistoryRepository
	tx      *MocktxManager
	metrics *MocktripMetrics
	usecase *finish.Usecase
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctrl := gomock.NewController(t)
	f := &fixture{
		trips:   NewMocktripRepository(ctrl),
		history: NewMockhistoryRepository(ctrl),
		tx:      NewMocktxManager(ctrl),
		metrics: NewMocktripMetrics(ctrl),
	}
	f.tx.EXPECT().Do(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).AnyTimes()
	f.usecase = finish.New(f.trips, f.history, f.tx, f.metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

func tripWithStatus(status domain.Status) domain.Trip {
	return domain.Trip{ID: tripID, Status: status}
}

func TestExecute_FinishesActiveTrip(t *testing.T) {
	f := newFixture(t)

	f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(tripWithStatus(domain.StatusActive), nil)
	f.trips.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, trip domain.Trip) error {
			require.Equal(t, domain.StatusCompleted, trip.Status)
			require.NotNil(t, trip.FinishedAt)
			return nil
		})
	f.history.EXPECT().AddStatusHistory(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, entry model.StatusHistoryEntry) error {
			require.Equal(t, tripID, entry.TripID)
			require.NotNil(t, entry.FromStatus)
			require.Equal(t, domain.StatusActive, *entry.FromStatus)
			require.Equal(t, domain.StatusCompleted, entry.ToStatus)
			return nil
		})
	f.metrics.EXPECT().TripCompleted(gomock.Any())

	trip, err := f.usecase.Execute(t.Context(), tripID)

	require.NoError(t, err)
	require.Equal(t, tripID, trip.ID)
	require.Equal(t, domain.StatusCompleted, trip.Status)
	require.NotNil(t, trip.FinishedAt)
}

func TestExecute_Errors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fixture)
		want  error
	}{
		{
			name: "trip does not exist",
			setup: func(f *fixture) {
				f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(domain.Trip{}, domain.ErrTripNotFound)
			},
			want: domain.ErrTripNotFound,
		},
		{
			name: "trip is already completed",
			setup: func(f *fixture) {
				f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(tripWithStatus(domain.StatusCompleted), nil)
			},
			want: domain.ErrTripCompleted,
		},
		{
			name: "update fails",
			setup: func(f *fixture) {
				f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(tripWithStatus(domain.StatusActive), nil)
				f.trips.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errBoom)
			},
			want: errBoom,
		},
		{
			name: "history insert fails",
			setup: func(f *fixture) {
				f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(tripWithStatus(domain.StatusActive), nil)
				f.trips.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
				f.history.EXPECT().AddStatusHistory(gomock.Any(), gomock.Any()).Return(errBoom)
			},
			want: errBoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t) // TripCompleted is not expected: a failed finish is not counted
			tt.setup(f)

			trip, err := f.usecase.Execute(t.Context(), tripID)

			require.ErrorIs(t, err, tt.want)
			require.Equal(t, domain.Trip{}, trip)
		})
	}
}
