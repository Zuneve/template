// Package container wires the service together: it is the only place that
// knows every concrete type and builds the dependency graph.
package container

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Zuneve/template/internal/config"
	"github.com/Zuneve/template/internal/gateway/push"
	healthhandler "github.com/Zuneve/template/internal/http/health"
	"github.com/Zuneve/template/internal/http/server"
	createhandler "github.com/Zuneve/template/internal/http/trip/create"
	finishhandler "github.com/Zuneve/template/internal/http/trip/finish"
	gethandler "github.com/Zuneve/template/internal/http/trip/get"
	listpositionshandler "github.com/Zuneve/template/internal/http/trip/listpositions"
	savepositionhandler "github.com/Zuneve/template/internal/http/trip/saveposition"
	"github.com/Zuneve/template/internal/infra/postgres"
	"github.com/Zuneve/template/internal/infra/postgres/transaction"
	"github.com/Zuneve/template/internal/infra/telemetry"
	idempotencyrepo "github.com/Zuneve/template/internal/repository/idempotency"
	triprepo "github.com/Zuneve/template/internal/repository/trip"
	createusecase "github.com/Zuneve/template/internal/usecase/trip/create"
	finishusecase "github.com/Zuneve/template/internal/usecase/trip/finish"
	getusecase "github.com/Zuneve/template/internal/usecase/trip/get"
	listpositionsusecase "github.com/Zuneve/template/internal/usecase/trip/listpositions"
	savepositionusecase "github.com/Zuneve/template/internal/usecase/trip/saveposition"
	workerpositions "github.com/Zuneve/template/internal/worker/positions"
)

type Container struct {
	pool       *pgxpool.Pool
	pushClient *push.Client
	router     http.Handler

	scanner *workerpositions.Scanner
	workers *workerpositions.Pool
	started bool
}

// New opens the database and builds the HTTP handler and the background
// components (they do not run until StartBackground). The caller owns the
// result and must Close it.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger, tel *telemetry.Provider) (*Container, error) {
	metrics, err := telemetry.NewMetrics(tel.MeterProvider())
	if err != nil {
		return nil, fmt.Errorf("create metrics: %w", err)
	}

	pool, err := postgres.NewPool(ctx, cfg.Database, tel.TracerProvider(), tel.MeterProvider())
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	pushClient, err := push.New(cfg.Push.URL, cfg.Push.RequestTimeout, tel.TracerProvider(), tel.Propagator())
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create push client: %w", err)
	}

	trips := triprepo.New(pool)
	idempotency := idempotencyrepo.New(pool)
	txManager := transaction.NewManager(pool)

	closeResources := func() {
		pushClient.Close()
		pool.Close()
	}

	if err := metrics.ObserveActiveTrips(trips.CountActive); err != nil {
		closeResources()
		return nil, fmt.Errorf("observe active trips: %w", err)
	}

	queue := workerpositions.NewQueue(cfg.Positions.QueueSize)
	workers := workerpositions.NewPool(workerpositions.PoolParams{
		Queue:          queue,
		Pusher:         pushClient,
		Metrics:        metrics,
		Workers:        cfg.Positions.Workers,
		RequestTimeout: cfg.Push.RequestTimeout,
		Logger:         logger,
		Tracer:         tel.TracerProvider().Tracer(workerTracerName),
	})
	scanner := workerpositions.NewScanner(workerpositions.ScannerParams{
		Finder:     trips,
		Queue:      queue,
		Metrics:    metrics,
		Ticker:     workerpositions.NewTicker(cfg.Positions.CheckInterval),
		StaleAfter: cfg.Positions.StaleAfter,
		Logger:     logger,
		Tracer:     tel.TracerProvider().Tracer(workerTracerName),
	})
	err = metrics.ObservePositionRequests(func() int64 { return int64(queue.Len()) }, workers.Active)
	if err != nil {
		closeResources()
		return nil, fmt.Errorf("observe position requests: %w", err)
	}

	create := createusecase.New(trips, trips, idempotency, txManager, metrics, logger)
	get := getusecase.New(trips)
	finish := finishusecase.New(trips, trips, txManager, metrics, logger)
	savePosition := savepositionusecase.New(trips, trips, txManager, metrics, queue)
	listPositions := listpositionsusecase.New(trips, trips)

	router := server.NewRouter(
		createhandler.New(create, logger),
		gethandler.New(get, logger),
		finishhandler.New(finish, logger),
		savepositionhandler.New(savePosition, logger),
		listpositionshandler.New(listPositions, logger),
		healthhandler.New(pool),
		server.Deps{
			Logger:         logger,
			RequestTimeout: cfg.Database.QueryTimeout,
			TracerProvider: tel.TracerProvider(),
			Propagator:     tel.Propagator(),
			Metrics:        metrics,
		},
	)

	return &Container{
		pool:       pool,
		pushClient: pushClient,
		router:     router,
		scanner:    scanner,
		workers:    workers,
	}, nil
}

const workerTracerName = "github.com/Zuneve/template/internal/worker/positions"

func (c *Container) Handler() http.Handler {
	return c.router
}

// StartBackground launches the stale-trip scanner and the push workers. ctx is
// their whole lifetime: do not pass a context that a shutdown signal cancels,
// or they would die before the HTTP server has finished its requests. Stop
// them with StopBackground.
func (c *Container) StartBackground(ctx context.Context) {
	c.workers.Start(ctx)
	c.scanner.Start(ctx)
	c.started = true
}

// StopBackground stops the scanner first, so nothing new is queued, and then
// lets the workers drain the queue until ctx runs out. It does nothing if the
// background was never started.
func (c *Container) StopBackground(ctx context.Context) error {
	if !c.started {
		return nil
	}
	c.started = false

	scannerErr := c.scanner.Stop(ctx)
	workersErr := c.workers.Shutdown(ctx)
	return errors.Join(scannerErr, workersErr)
}

// Close releases the HTTP client and the database pool. Call it last, after
// StopBackground: workers still use both.
func (c *Container) Close() {
	c.pushClient.Close()
	c.pool.Close()
}
