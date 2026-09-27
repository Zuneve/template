package repository

import "errors"

var (
	// ErrTripNotFound means no trip with the given id exists.
	ErrTripNotFound = errors.New("trip not found")
	// ErrTripCompleted means the trip is already finished.
	ErrTripCompleted = errors.New("trip already completed")
	// ErrDriverBusy means the driver already has an active trip.
	ErrDriverBusy = errors.New("driver already has an active trip")
	// ErrIdempotencyKeyNotFound means no record exists for this key yet.
	ErrIdempotencyKeyNotFound = errors.New("idempotency key not found")
	// ErrIdempotencyKeyConflict means a concurrent request already claimed
	// this key; the caller should look it up again to resolve.
	ErrIdempotencyKeyConflict = errors.New("idempotency key already claimed")
)
