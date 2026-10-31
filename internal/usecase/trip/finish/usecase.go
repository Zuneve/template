package finish

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/model"
)

type Usecase struct {
	trips   tripRepository
	history historyRepository
	tx      txManager
	metrics tripMetrics
	logger  *slog.Logger
}

func New(
	trips tripRepository,
	history historyRepository,
	tx txManager,
	metrics tripMetrics,
	logger *slog.Logger,
) *Usecase {
	return &Usecase{
		trips:   trips,
		history: history,
		tx:      tx,
		metrics: metrics,
		logger:  logger,
	}
}

// Execute finishes the trip. The row is locked with SELECT ... FOR UPDATE, so
// of several concurrent finishes exactly one succeeds and the rest see the
// trip already completed.
func (u *Usecase) Execute(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	var trip domain.Trip
	err := u.tx.Do(ctx, func(ctx context.Context) error {
		locked, err := u.trips.GetByIDForUpdate(ctx, id)
		if err != nil {
			return err
		}

		from := locked.Status
		if err := locked.Finish(time.Now().UTC()); err != nil {
			return err
		}
		if err := u.trips.Update(ctx, locked); err != nil {
			return err
		}
		if err := u.history.AddStatusHistory(ctx, model.StatusHistoryEntry{
			TripID:     locked.ID,
			FromStatus: &from,
			ToStatus:   locked.Status,
		}); err != nil {
			return err
		}

		trip = locked
		return nil
	})
	if err != nil {
		return domain.Trip{}, err
	}

	u.metrics.TripCompleted(ctx)
	u.logger.InfoContext(ctx, "trip completed", slog.String("trip_id", trip.ID.String()))
	return trip, nil
}
