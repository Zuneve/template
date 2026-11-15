package listpositions

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

type Usecase struct {
	trips     tripRepository
	positions positionRepository
}

func New(trips tripRepository, positions positionRepository) *Usecase {
	return &Usecase{trips: trips, positions: positions}
}

// Execute returns the whole route of the trip. The trip is looked up first:
// an unknown trip is 404, while a known trip with no points is an empty route.
func (u *Usecase) Execute(ctx context.Context, tripID uuid.UUID) ([]domain.Position, error) {
	if _, err := u.trips.GetByID(ctx, tripID); err != nil {
		return nil, err
	}
	return u.positions.ListPositions(ctx, tripID)
}
