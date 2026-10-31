package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

const (
	tripsTable  = "trips"
	timeoutType = "timeout"
)

func TestCollectionName(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"select", `SELECT id FROM trips WHERE id = $1`, tripsTable},
		{"insert", `INSERT INTO trip_status_history (trip_id) VALUES ($1)`, "trip_status_history"},
		{"update", `UPDATE trips SET status = $1`, tripsTable},
		{"delete", `DELETE FROM idempotency_keys WHERE expires_at < now()`, "idempotency_keys"},
		{"lock", "SELECT *\n  FROM trips\n WHERE id = $1 FOR UPDATE", tripsTable},
		{"mixed case", `select count(*) From Trips where status = 'active'`, tripsTable},
		{"schema qualified", `SELECT 1 FROM public.trips`, "public.trips"},
		{"no table", `SELECT 1`, ""},
		{"empty", ``, ""},
		{"ping", `;`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, collectionName(tt.sql))
		})
	}
}

func TestOperationName(t *testing.T) {
	tests := []struct {
		sql  string
		want string
	}{
		{`SELECT 1 FROM trips`, "SELECT"},
		{`insert into trips values ($1)`, "INSERT"},
		{`/* comment */ UPDATE trips SET a = 1`, "UPDATE"},
		{`DELETE FROM trips`, "DELETE"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			require.Equal(t, tt.want, operationName(context.Background(), tt.sql))
		})
	}
}

func TestErrorType(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"sqlstate", &pgconn.PgError{Code: "23505"}, "23505"},
		{"wrapped sqlstate", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "40001"}), "40001"},
		{timeoutType, context.DeadlineExceeded, timeoutType},
		{"wrapped timeout", fmt.Errorf("query: %w", context.DeadlineExceeded), timeoutType},
		{"canceled", context.Canceled, "canceled"},
		{"anything else", errors.New("socket closed"), "_OTHER"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, errorType(tt.err))
		})
	}
}
