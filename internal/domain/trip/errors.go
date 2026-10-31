package trip

import "errors"

var (
	ErrTripNotFound  = errors.New("trip not found")
	ErrTripCompleted = errors.New("trip already completed")
	ErrDriverBusy    = errors.New("driver already has an active trip")

	ErrIdempotencyKeyNotFound = errors.New("idempotency key not found")
	// ErrIdempotencyKeyConflict means a concurrent request already claimed the key.
	ErrIdempotencyKeyConflict = errors.New("idempotency key already claimed")
	// ErrIdempotencyKeyReused means the key came again with a different request body.
	ErrIdempotencyKeyReused = errors.New("idempotency key used with a different request body")
)

// ValidationError tells which field of the trip is invalid and why. The text
// reads as "<field> <reason>", e.g. "price must be >= 0".
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return e.Field + " " + e.Reason
}
