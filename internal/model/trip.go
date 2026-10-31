package model

import (
	"github.com/google/uuid"

	"github.com/Zuneve/template/internal/domain/trip"
)

// StatusHistoryEntry is one row of trip_status_history. FromStatus is nil for
// the first transition, made when the trip is created.
type StatusHistoryEntry struct {
	TripID     uuid.UUID
	FromStatus *trip.Status
	ToStatus   trip.Status
}

// IdempotencyRecord remembers which trip was created for an Idempotency-Key
// and the hash of the request body it came with.
type IdempotencyRecord struct {
	Key         uuid.UUID
	RequestHash string
	TripID      uuid.UUID
}
