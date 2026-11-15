package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Zuneve/template/internal/config"
)

// NewPool builds a connection pool from cfg and confirms the database is
// reachable. The service must not start against a database it can't reach.
// Every query of the pool is traced and measured, so repositories carry no
// telemetry code.
func NewPool(
	ctx context.Context,
	cfg config.DatabaseConfig,
	tp trace.TracerProvider,
	mp metric.MeterProvider,
) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	tracer, err := newQueryTracer(tp, mp)
	if err != nil {
		return nil, fmt.Errorf("create query tracer: %w", err)
	}
	poolCfg.ConnConfig.Tracer = tracer

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
