//go:generate go tool mockgen -source ${GOFILE} -destination mocks_test.go -package ${GOPACKAGE}_test
package finish

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/model"
)

type (
	tripRepository interface {
		GetByIDForUpdate(ctx context.Context, id uuid.UUID) (domain.Trip, error)
		Update(ctx context.Context, trip domain.Trip) error
	}

	historyRepository interface {
		AddStatusHistory(ctx context.Context, entry model.StatusHistoryEntry) error
	}

	txManager interface {
		Do(ctx context.Context, fn func(ctx context.Context) error) error
	}

	tripMetrics interface {
		TripCompleted(ctx context.Context)
	}
)
