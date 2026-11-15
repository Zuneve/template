package trip

import (
	"time"

	"github.com/google/uuid"
)

// maxFutureSkew is how far ahead of the server clock a point's recorded_at may
// be. Device and server clocks drift a little, so a point stamped a few seconds
// "in the future" is normal; a minute or more is a broken clock or a bad request.
const maxFutureSkew = time.Minute

// Position is one point of a trip's route. RecordedAt is when the device
// captured it, not when the service received it: devices buffer points while
// offline and send them later, out of order.
type Position struct {
	ID         int64
	TripID     uuid.UUID
	Point      Point
	RecordedAt time.Time
}

// NewPosition validates a point received at now and returns it with RecordedAt
// in UTC. A point recorded exactly maxFutureSkew ahead is still accepted.
func NewPosition(tripID uuid.UUID, point Point, recordedAt, now time.Time) (Position, error) {
	if !point.valid() {
		return Position{}, &ValidationError{Field: "coordinates", Reason: "are out of range"}
	}
	if recordedAt.IsZero() {
		return Position{}, &ValidationError{Field: "recorded_at", Reason: reasonRequired}
	}
	if recordedAt.After(now.Add(maxFutureSkew)) {
		return Position{}, &ValidationError{Field: "recorded_at", Reason: "must not be more than a minute in the future"}
	}

	return Position{TripID: tripID, Point: point, RecordedAt: recordedAt.UTC()}, nil
}

// StaleTrip is an active trip that stopped sending points: what is needed to
// ask its driver's device for a fresh one.
type StaleTrip struct {
	TripID   uuid.UUID
	DriverID uuid.UUID
}
