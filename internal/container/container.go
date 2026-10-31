// Package container wires the service together: it is the only place that
// knows every concrete type and builds the dependency graph.
package container

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Zuneve/template/internal/config"
	healthhandler "github.com/Zuneve/template/internal/http/health"
	"github.com/Zuneve/template/internal/http/server"
	createhandler "github.com/Zuneve/template/internal/http/trip/create"
	finishhandler "github.com/Zuneve/template/internal/http/trip/finish"
	gethandler "github.com/Zuneve/template/internal/http/trip/get"
	"github.com/Zuneve/template/internal/infra/postgres"
	"github.com/Zuneve/template/internal/infra/postgres/transaction"
	"github.com/Zuneve/template/internal/infra/telemetry"
	idempotencyrepo "github.com/Zuneve/template/internal/repository/idempotency"
	triprepo "github.com/Zuneve/template/internal/repository/trip"
	createusecase "github.com/Zuneve/template/internal/usecase/trip/create"
	finishusecase "github.com/Zuneve/template/internal/usecase/trip/finish"
	getusecase "github.com/Zuneve/template/internal/usecase/trip/get"
)

type Container struct {
	pool   *pgxpool.Pool
	router http.Handler
}

// New opens the database and builds the HTTP handler. The caller owns the
// result and must Close it.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger, otel *telemetry.Provider) (*Container, error) {
	metrics, err := telemetry.NewMetrics(otel.MeterProvider())
	if err != nil {
		return nil, fmt.Errorf("create metrics: %w", err)
	}

	pool, err := postgres.NewPool(ctx, cfg.Database, otel.TracerProvider(), otel.MeterProvider())
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	trips := triprepo.New(pool)
	idempotency := idempotencyrepo.New(pool)
	txManager := transaction.NewManager(pool)

	if err := metrics.ObserveActiveTrips(trips.CountActive); err != nil {
		pool.Close()
		return nil, fmt.Errorf("observe active trips: %w", err)
	}

	create := createusecase.New(trips, trips, idempotency, txManager, metrics, logger)
	get := getusecase.New(trips)
	finish := finishusecase.New(trips, trips, txManager, metrics, logger)

	router := server.NewRouter(
		createhandler.New(create, logger),
		gethandler.New(get, logger),
		finishhandler.New(finish, logger),
		healthhandler.New(pool),
		server.Deps{
			Logger:         logger,
			RequestTimeout: cfg.Database.QueryTimeout,
			TracerProvider: otel.TracerProvider(),
			Propagator:     otel.Propagator(),
			Metrics:        metrics,
		},
	)

	return &Container{pool: pool, router: router}, nil
}

func (c *Container) Handler() http.Handler {
	return c.router
}

func (c *Container) Close() {
	c.pool.Close()
}
