package trip

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
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
	Status     Status
	StartedAt  time.Time
	FinishedAt *time.Time
}

type NewParams struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	DriverID   uuid.UUID
	StartPoint Point
	EndPoint   Point
	Price      int64
	StartedAt  time.Time
}

// New validates p and returns a trip in the active status.
func New(p NewParams) (Trip, error) {
	if p.UserID == uuid.Nil {
		return Trip{}, &ValidationError{Field: "user_id", Reason: "is required"}
	}
	if p.DriverID == uuid.Nil {
		return Trip{}, &ValidationError{Field: "driver_id", Reason: "is required"}
	}
	if !p.StartPoint.valid() {
		return Trip{}, &ValidationError{Field: "start_point", Reason: "is out of range"}
	}
	if !p.EndPoint.valid() {
		return Trip{}, &ValidationError{Field: "end_point", Reason: "is out of range"}
	}
	if p.Price < 0 {
		return Trip{}, &ValidationError{Field: "price", Reason: "must be >= 0"}
	}

	return Trip{
		ID:         p.ID,
		UserID:     p.UserID,
		DriverID:   p.DriverID,
		StartPoint: p.StartPoint,
		EndPoint:   p.EndPoint,
		Price:      p.Price,
		Status:     StatusActive,
		StartedAt:  p.StartedAt,
	}, nil
}

// Finish moves an active trip to completed. A trip that is already completed
// is left untouched and ErrTripCompleted is returned.
func (t *Trip) Finish(at time.Time) error {
	if t.Status != StatusActive {
		return ErrTripCompleted
	}
	t.Status = StatusCompleted
	t.FinishedAt = &at
	return nil
}

// valid is false for NaN as well, since every comparison with NaN is false.
func (p Point) valid() bool {
	return p.Latitude >= -90 && p.Latitude <= 90 &&
		p.Longitude >= -180 && p.Longitude <= 180
}
