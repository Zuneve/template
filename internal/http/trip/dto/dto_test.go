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
