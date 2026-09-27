package repository

import (
	"time"

	"github.com/google/uuid"
)

type TripStatus string

const (
	TripStatusActive    TripStatus = "active"
	TripStatusCompleted TripStatus = "completed"
)

type Point struct {
	Latitude  float64
	Longitude float64
}

type Trip struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	DriverID   uuid.UUID
	StartPoint Point
	EndPoint   Point
	Price      int64
	Status     TripStatus
	StartedAt  time.Time
	FinishedAt *time.Time
}

// StatusHistoryEntry is one row of trip_status_history: the record of a
// status transition. FromStatus is nil for the very first transition, made
// when the trip is created.
type StatusHistoryEntry struct {
	TripID     uuid.UUID
	FromStatus *TripStatus
	ToStatus   TripStatus
	Reason     *string
}
