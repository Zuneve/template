package get

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

type Usecase struct {
	trips tripRepository
}

func New(trips tripRepository) *Usecase {
	return &Usecase{trips: trips}
}

func (u *Usecase) Execute(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	return u.trips.GetByID(ctx, id)
}
