//go:generate go tool mockgen -source ${GOFILE} -destination mocks_test.go -package ${GOPACKAGE}_test
package listpositions

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

type (
	tripRepository interface {
		GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error)
	}

	positionRepository interface {
		ListPositions(ctx context.Context, tripID uuid.UUID) ([]domain.Position, error)
	}
)
