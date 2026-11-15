package dto_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/http/trip/dto"
)

func TestTrip(t *testing.T) {
	moscow := time.FixedZone("MSK", 3*60*60)
	started := time.Date(2026, 10, 1, 12, 0, 0, 0, moscow)
	finished := started.Add(time.Hour)
	id, userID, driverID := uuid.New(), uuid.New(), uuid.New()

	t.Run("active trip has no finished_at, times are UTC", func(t *testing.T) {
		got := dto.Trip(domain.Trip{
			ID: id, UserID: userID, DriverID: driverID,
			StartPoint: domain.Point{Latitude: 1, Longitude: 2},
			EndPoint:   domain.Point{Latitude: 3, Longitude: 4},
			Price:      700, Status: domain.StatusActive, StartedAt: started,
		})

		require.Equal(t, id, got.Id)
		require.Equal(t, userID, got.UserId)
		require.Equal(t, driverID, got.DriverId)
		require.InDelta(t, 1, got.StartPoint.Latitude, 0)
		require.InDelta(t, 2, got.StartPoint.Longitude, 0)
		require.InDelta(t, 3, got.EndPoint.Latitude, 0)
		require.InDelta(t, 4, got.EndPoint.Longitude, 0)
		require.Equal(t, int64(700), got.Price)
		require.EqualValues(t, "active", got.Status)
		require.Equal(t, time.UTC, got.StartedAt.Location())
		require.True(t, got.StartedAt.Equal(started))
		require.Nil(t, got.FinishedAt)
	})

	t.Run("completed trip", func(t *testing.T) {
		got := dto.Trip(domain.Trip{Status: domain.StatusCompleted, StartedAt: started, FinishedAt: &finished})

		require.EqualValues(t, "completed", got.Status)
		require.NotNil(t, got.FinishedAt)
		require.Equal(t, time.UTC, got.FinishedAt.Location())
		require.True(t, got.FinishedAt.Equal(finished))
	})
}

func TestPosition(t *testing.T) {
	moscow := time.FixedZone("MSK", 3*60*60)
	recorded := time.Date(2026, 8, 24, 15, 0, 5, 0, moscow)
	tripID := uuid.New()

	got := dto.Position(domain.Position{
		ID: 42, TripID: tripID,
		Point:      domain.Point{Latitude: 59.935, Longitude: 30.335},
		RecordedAt: recorded,
	})

	require.Equal(t, int64(42), got.Id)
	require.Equal(t, tripID, got.TripId)
	require.InDelta(t, 59.935, got.Coordinates.Latitude, 0)
	require.InDelta(t, 30.335, got.Coordinates.Longitude, 0)
	require.Equal(t, time.UTC, got.RecordedAt.Location())
	require.True(t, got.RecordedAt.Equal(recorded))
}

func TestPositions(t *testing.T) {
	t.Run("keeps the order", func(t *testing.T) {
		got := dto.Positions([]domain.Position{{ID: 1}, {ID: 2}, {ID: 3}})

		require.Len(t, got.Positions, 3)
		require.Equal(t, int64(1), got.Positions[0].Id)
		require.Equal(t, int64(3), got.Positions[2].Id)
	})

	t.Run("empty route is an empty list, not nil", func(t *testing.T) {
		require.NotNil(t, dto.Positions(nil).Positions)
		require.Empty(t, dto.Positions([]domain.Position{}).Positions)
	})
}
