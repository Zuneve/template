package httpapi

import (
	"context"
	"time"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/postgres"
	"github.com/Zuneve/template/internal/repository"
)

// API implements api.ServerInterface (generated from
// contracts/openapi/trip-service.openapi.yaml). Endpoints outside this lab's
// scope (trip positions) fall through to the embedded api.Unimplemented and
// answer 501.
type API struct {
	api.Unimplemented

	trips        *repository.TripRepository
	history      *repository.TripStatusHistoryRepository
	idempotency  *repository.IdempotencyRepository
	txManager    *postgres.TxManager
	ready        func(ctx context.Context) error
	queryTimeout time.Duration
}

func NewAPI(
	trips *repository.TripRepository,
	history *repository.TripStatusHistoryRepository,
	idempotency *repository.IdempotencyRepository,
	txManager *postgres.TxManager,
	ready func(ctx context.Context) error,
	queryTimeout time.Duration,
) *API {
	return &API{
		trips:        trips,
		history:      history,
		idempotency:  idempotency,
		txManager:    txManager,
		ready:        ready,
		queryTimeout: queryTimeout,
	}
}

func (a *API) withQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, a.queryTimeout)
}
