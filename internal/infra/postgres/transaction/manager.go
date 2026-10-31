package transaction

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txKey struct{}

// TxFromContext returns the transaction opened by Manager.Do, if ctx is
// inside one. Repositories use it to run their queries in that transaction.
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// Manager runs a function inside a database transaction and passes the
// transaction to repositories through ctx, so they never see a *pgx.Tx as an
// argument.
type Manager struct {
	pool *pgxpool.Pool
}

func NewManager(pool *pgxpool.Pool) *Manager {
	return &Manager{pool: pool}
}

// Do opens a transaction, stashes it in ctx and calls fn with the enriched
// context. fn returning nil commits; fn returning an error, or panicking,
// rolls back and the error (or panic) propagates to the caller. Calling Do
// again while already inside a transaction reuses it instead of opening a
// nested one.
//
// Isolation level: Read Committed, Postgres's default. The invariant "one
// active trip per driver" is enforced by a partial unique index (see
// migrations), and a trip is finished under SELECT ... FOR UPDATE, so no
// stronger isolation level is needed.
func (m *Manager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := TxFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
