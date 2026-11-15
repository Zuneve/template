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

func TestNewPosition(t *testing.T) {
	t.Parallel()

	tripID := uuid.New()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	point := trip.Point{Latitude: 59.935, Longitude: 30.335}

	t.Run("valid point", func(t *testing.T) {
		t.Parallel()

		moscow := time.FixedZone("MSK", 3*60*60)
		recordedAt := now.Add(-time.Hour).In(moscow)

		got, err := trip.NewPosition(tripID, point, recordedAt, now)

		require.NoError(t, err)
		require.Equal(t, tripID, got.TripID)
		require.Equal(t, point, got.Point)
		require.True(t, recordedAt.Equal(got.RecordedAt))
		require.Equal(t, time.UTC, got.RecordedAt.Location())
	})

	t.Run("future skew boundary", func(t *testing.T) {
		t.Parallel()

		_, err := trip.NewPosition(tripID, point, now.Add(time.Minute), now)
		require.NoError(t, err, "exactly a minute ahead is still accepted")

		_, err = trip.NewPosition(tripID, point, now.Add(time.Minute+time.Nanosecond), now)
		var validationErr *trip.ValidationError
		require.True(t, errors.As(err, &validationErr))
		require.Equal(t, "recorded_at", validationErr.Field)
	})

	t.Run("a missing recorded_at", func(t *testing.T) {
		t.Parallel()

		_, err := trip.NewPosition(tripID, point, time.Time{}, now)
		var validationErr *trip.ValidationError
		require.True(t, errors.As(err, &validationErr))
		require.Equal(t, "recorded_at", validationErr.Field)
	})

	t.Run("coordinates out of range", func(t *testing.T) {
		t.Parallel()

		for name, bad := range map[string]trip.Point{
			"latitude above 90":    {Latitude: 90.1, Longitude: 0},
			"latitude below -90":   {Latitude: -90.1, Longitude: 0},
			"longitude above 180":  {Latitude: 0, Longitude: 180.1},
			"longitude below -180": {Latitude: 0, Longitude: -180.1},
			"NaN":                  {Latitude: math.NaN(), Longitude: 0},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := trip.NewPosition(tripID, bad, now, now)
				var validationErr *trip.ValidationError
				require.True(t, errors.As(err, &validationErr))
				require.Equal(t, "coordinates", validationErr.Field)
			})
		}

		_, err := trip.NewPosition(tripID, trip.Point{Latitude: 90, Longitude: -180}, now, now)
		require.NoError(t, err, "the range borders are valid")
	})
}
