//go:build integration

// Package testutil holds helpers for integration tests that need a real PostgreSQL.
package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool connects to the database from DATABASE_URL (tripgoctl writes it to
// .env, `make test-integration` exports it) and closes the pool after the test.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is not set: start the environment with `tripgoctl environment start` and run `make test-integration`")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// CleanupTrips deletes the given trips, and everything that references them,
// when the test ends, so a test leaves no data behind and can be run again.
func CleanupTrips(t *testing.T, pool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Helper()

	t.Cleanup(func() {
		ctx := context.Background()
		// idempotency_keys has no ON DELETE CASCADE; trip_status_history does.
		if _, err := pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE trip_id = ANY($1)`, ids); err != nil {
			t.Errorf("cleanup idempotency_keys: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM trips WHERE id = ANY($1)`, ids); err != nil {
			t.Errorf("cleanup trips: %v", err)
		}
	})
}
