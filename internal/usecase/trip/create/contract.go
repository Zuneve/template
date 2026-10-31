//go:generate go tool mockgen -source ${GOFILE} -destination mocks_test.go -package ${GOPACKAGE}_test
package create

import (
	"context"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/model"
)

type (
	tripRepository interface {
		Create(ctx context.Context, trip domain.Trip) error
		GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error)
	}

	historyRepository interface {
		AddStatusHistory(ctx context.Context, entry model.StatusHistoryEntry) error
	}

	idempotencyRepository interface {
		DeleteExpired(ctx context.Context) error
		Get(ctx context.Context, key uuid.UUID) (model.IdempotencyRecord, error)
		Insert(ctx context.Context, rec model.IdempotencyRecord) error
	}

	txManager interface {
		Do(ctx context.Context, fn func(ctx context.Context) error) error
	}

	tripMetrics interface {
		TripCreated(ctx context.Context)
	}
)
