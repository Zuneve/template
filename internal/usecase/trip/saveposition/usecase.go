package saveposition

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

type Input struct {
	TripID     uuid.UUID
	Point      domain.Point
	RecordedAt time.Time
}

type Usecase struct {
	trips     tripRepository
	positions positionRepository
	tx        txManager
	metrics   positionMetrics
	queue     positionQueue
}

func New(
	trips tripRepository,
	positions positionRepository,
	tx txManager,
	metrics positionMetrics,
	queue positionQueue,
) *Usecase {
	return &Usecase{trips: trips, positions: positions, tx: tx, metrics: metrics, queue: queue}
}

// Execute stores the point and moves the trip's last_position_at in one
// transaction: either both happen or neither does. The trip row is locked with
// SELECT ... FOR UPDATE, so a concurrent finish cannot slip in between the
// "is it still active" check and the insert and leave a point on a completed trip.
func (u *Usecase) Execute(ctx context.Context, in Input) (domain.Position, error) {
	position, err := domain.NewPosition(in.TripID, in.Point, in.RecordedAt, time.Now().UTC())
	if err != nil {
		return domain.Position{}, err
	}

	var saved domain.Position
	err = u.tx.Do(ctx, func(ctx context.Context) error {
		trip, err := u.trips.GetByIDForUpdate(ctx, in.TripID)
		if err != nil {
			return err
		}
		if trip.Status != domain.StatusActive {
			return domain.ErrTripCompleted
		}

		saved, err = u.positions.AddPosition(ctx, position)
		if err != nil {
			return err
		}
		return u.positions.TouchLastPosition(ctx, in.TripID, position.RecordedAt)
	})
	if err != nil {
		return domain.Position{}, err
	}

	// Only after the commit: a rolled-back point must not cancel a push the
	// trip still needs.
	u.queue.Cancel(in.TripID)
	u.metrics.PositionSaved(ctx)
	return saved, nil
}
