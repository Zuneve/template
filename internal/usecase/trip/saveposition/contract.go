//go:generate go tool mockgen -source ${GOFILE} -destination mocks_test.go -package ${GOPACKAGE}_test
package saveposition

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

type (
	tripRepository interface {
		GetByIDForUpdate(ctx context.Context, id uuid.UUID) (domain.Trip, error)
	}

	positionRepository interface {
		AddPosition(ctx context.Context, p domain.Position) (domain.Position, error)
		TouchLastPosition(ctx context.Context, tripID uuid.UUID, recordedAt time.Time) error
	}

	txManager interface {
		Do(ctx context.Context, fn func(ctx context.Context) error) error
	}

	positionMetrics interface {
		PositionSaved(ctx context.Context)
	}

	// positionQueue holds the trips waiting for a REQUEST_POSITION push. A
	// trip that just reported a position no longer needs one.
	positionQueue interface {
		Cancel(tripID uuid.UUID)
	}
)
