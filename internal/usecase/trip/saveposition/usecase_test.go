package saveposition_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/usecase/trip/saveposition"
)

var (
	tripID  = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	errBoom = errors.New("boom")
)

type fixture struct {
	trips     *MocktripRepository
	positions *MockpositionRepository
	tx        *MocktxManager
	metrics   *MockpositionMetrics
	queue     *MockpositionQueue
	usecase   *saveposition.Usecase
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctrl := gomock.NewController(t)
	f := &fixture{
		trips:     NewMocktripRepository(ctrl),
		positions: NewMockpositionRepository(ctrl),
		tx:        NewMocktxManager(ctrl),
		metrics:   NewMockpositionMetrics(ctrl),
		queue:     NewMockpositionQueue(ctrl),
	}
	f.tx.EXPECT().Do(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).AnyTimes()
	f.usecase = saveposition.New(f.trips, f.positions, f.tx, f.metrics, f.queue)
	return f
}

func input() saveposition.Input {
	return saveposition.Input{
		TripID:     tripID,
		Point:      domain.Point{Latitude: 59.935, Longitude: 30.335},
		RecordedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond),
	}
}

func tripWithStatus(status domain.Status) domain.Trip {
	return domain.Trip{ID: tripID, Status: status}
}

func TestExecute_SavesPositionAndTouchesTrip(t *testing.T) {
	f := newFixture(t)
	in := input()

	gomock.InOrder(
		f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(tripWithStatus(domain.StatusActive), nil),
		f.positions.EXPECT().AddPosition(gomock.Any(), domain.Position{TripID: tripID, Point: in.Point, RecordedAt: in.RecordedAt}).
			Return(domain.Position{ID: 42, TripID: tripID, Point: in.Point, RecordedAt: in.RecordedAt}, nil),
		f.positions.EXPECT().TouchLastPosition(gomock.Any(), tripID, in.RecordedAt).Return(nil),
		f.queue.EXPECT().Cancel(tripID),
		f.metrics.EXPECT().PositionSaved(gomock.Any()),
	)

	got, err := f.usecase.Execute(t.Context(), in)

	require.NoError(t, err)
	require.Equal(t, domain.Position{ID: 42, TripID: tripID, Point: in.Point, RecordedAt: in.RecordedAt}, got)
}

func TestExecute_InvalidInputDoesNotTouchStorage(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *saveposition.Input)
	}{
		{"latitude out of range", func(in *saveposition.Input) { in.Point.Latitude = 91 }},
		{"longitude out of range", func(in *saveposition.Input) { in.Point.Longitude = -181 }},
		{"recorded_at far in the future", func(in *saveposition.Input) { in.RecordedAt = time.Now().Add(time.Hour) }},
		{"recorded_at missing", func(in *saveposition.Input) { in.RecordedAt = time.Time{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			in := input()
			tt.mutate(&in)

			_, err := f.usecase.Execute(t.Context(), in)

			var validationErr *domain.ValidationError
			require.ErrorAs(t, err, &validationErr)
		})
	}
}

func TestExecute_Errors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fixture, in saveposition.Input)
		want  error
	}{
		{
			name: "trip does not exist",
			setup: func(f *fixture, _ saveposition.Input) {
				f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(domain.Trip{}, domain.ErrTripNotFound)
			},
			want: domain.ErrTripNotFound,
		},
		{
			name: "trip is completed",
			setup: func(f *fixture, _ saveposition.Input) {
				f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(tripWithStatus(domain.StatusCompleted), nil)
			},
			want: domain.ErrTripCompleted,
		},
		{
			name: "storing the point fails",
			setup: func(f *fixture, _ saveposition.Input) {
				f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(tripWithStatus(domain.StatusActive), nil)
				f.positions.EXPECT().AddPosition(gomock.Any(), gomock.Any()).Return(domain.Position{}, errBoom)
			},
			want: errBoom,
		},
		{
			name: "updating last_position_at fails",
			setup: func(f *fixture, in saveposition.Input) {
				f.trips.EXPECT().GetByIDForUpdate(gomock.Any(), tripID).Return(tripWithStatus(domain.StatusActive), nil)
				f.positions.EXPECT().AddPosition(gomock.Any(), gomock.Any()).Return(domain.Position{ID: 1}, nil)
				f.positions.EXPECT().TouchLastPosition(gomock.Any(), tripID, in.RecordedAt).Return(errBoom)
			},
			want: errBoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			in := input()
			tt.setup(f, in)

			got, err := f.usecase.Execute(t.Context(), in)

			require.ErrorIs(t, err, tt.want)
			require.Equal(t, domain.Position{}, got)
		})
	}
}
