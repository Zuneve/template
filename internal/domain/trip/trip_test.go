package trip_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Zuneve/template/internal/domain/trip"
)

const (
	fieldStartPoint = "start_point"
	fieldEndPoint   = "end_point"
	reasonRange     = "is out of range"
)

func validParams() trip.NewParams {
	return trip.NewParams{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DriverID:   uuid.New(),
		StartPoint: trip.Point{Latitude: 55.75, Longitude: 37.61},
		EndPoint:   trip.Point{Latitude: 59.93, Longitude: 30.33},
		Price:      1500,
		StartedAt:  time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("valid params give an active trip", func(t *testing.T) {
		t.Parallel()

		p := validParams()

		got, err := trip.New(p)

		require.NoError(t, err)
		require.Equal(t, trip.Trip{
			ID:         p.ID,
			UserID:     p.UserID,
			DriverID:   p.DriverID,
			StartPoint: p.StartPoint,
			EndPoint:   p.EndPoint,
			Price:      p.Price,
			Status:     trip.StatusActive,
			StartedAt:  p.StartedAt,
		}, got)
		require.Nil(t, got.FinishedAt)
	})

	t.Run("boundary values are accepted", func(t *testing.T) {
		t.Parallel()

		p := validParams()
		p.StartPoint = trip.Point{Latitude: -90, Longitude: -180}
		p.EndPoint = trip.Point{Latitude: 90, Longitude: 180}
		p.Price = 0

		_, err := trip.New(p)

		require.NoError(t, err)
	})

	invalid := []struct {
		name      string
		mutate    func(*trip.NewParams)
		wantField string
		wantText  string
	}{
		{"nil user", func(p *trip.NewParams) { p.UserID = uuid.Nil }, "user_id", "user_id is required"},
		{"nil driver", func(p *trip.NewParams) { p.DriverID = uuid.Nil }, "driver_id", "driver_id is required"},
		{"start latitude too high", func(p *trip.NewParams) { p.StartPoint.Latitude = 90.1 }, fieldStartPoint, fieldStartPoint + " " + reasonRange},
		{"start latitude too low", func(p *trip.NewParams) { p.StartPoint.Latitude = -90.1 }, fieldStartPoint, fieldStartPoint + " " + reasonRange},
		{"start longitude too high", func(p *trip.NewParams) { p.StartPoint.Longitude = 180.1 }, fieldStartPoint, fieldStartPoint + " " + reasonRange},
		{"start longitude too low", func(p *trip.NewParams) { p.StartPoint.Longitude = -180.1 }, fieldStartPoint, fieldStartPoint + " " + reasonRange},
		{"start latitude NaN", func(p *trip.NewParams) { p.StartPoint.Latitude = math.NaN() }, fieldStartPoint, fieldStartPoint + " " + reasonRange},
		{"end latitude too high", func(p *trip.NewParams) { p.EndPoint.Latitude = 91 }, fieldEndPoint, fieldEndPoint + " " + reasonRange},
		{"end longitude too low", func(p *trip.NewParams) { p.EndPoint.Longitude = -181 }, fieldEndPoint, fieldEndPoint + " " + reasonRange},
		{"negative price", func(p *trip.NewParams) { p.Price = -1 }, "price", "price must be >= 0"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := validParams()
			tc.mutate(&p)

			got, err := trip.New(p)

			var verr *trip.ValidationError
			require.True(t, errors.As(err, &verr), "want *ValidationError, got %v", err)
			require.Equal(t, tc.wantField, verr.Field)
			require.EqualError(t, err, tc.wantText)
			require.Equal(t, trip.Trip{}, got)
		})
	}
}

func TestTrip_Finish(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 31, 13, 0, 0, 0, time.UTC)
	earlier := time.Date(2026, 10, 31, 12, 30, 0, 0, time.UTC)

	t.Run("active trip becomes completed", func(t *testing.T) {
		t.Parallel()

		tr, err := trip.New(validParams())
		require.NoError(t, err)

		err = tr.Finish(at)

		require.NoError(t, err)
		require.Equal(t, trip.StatusCompleted, tr.Status)
		require.NotNil(t, tr.FinishedAt)
		require.Equal(t, at, *tr.FinishedAt)
	})

	t.Run("completed trip is not finished again", func(t *testing.T) {
		t.Parallel()

		tr := trip.Trip{Status: trip.StatusCompleted, FinishedAt: &earlier}

		err := tr.Finish(at)

		require.ErrorIs(t, err, trip.ErrTripCompleted)
		require.Equal(t, trip.StatusCompleted, tr.Status)
		require.Equal(t, earlier, *tr.FinishedAt, "finished_at must keep the first value")
	})
}
