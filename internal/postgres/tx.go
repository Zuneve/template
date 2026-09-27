package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TxManager runs fn inside a database transaction and makes it available to
// repositories through ctx (see ExecutorFromContext), so repositories never
// see a *pgx.Tx directly.
type TxManager struct {
	pool *pgxpool.Pool
}

func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// Do opens a transaction, stashes it in ctx and calls fn with the enriched
// context. fn returning nil commits; fn returning an error, or panicking,
// rolls back and the error (or panic) propagates to the caller. Calling Do
// again while already inside a transaction reuses it instead of opening a
// nested one.
//
// Isolation level: Read Committed, Postgres's default. The only invariant in
// this service that isn't a plain read-then-write is "one active trip per
// driver", and that's enforced by a partial unique index at the database
// level (see migrations), not by a SELECT inside the transaction — so no
// stronger isolation level is needed to avoid races here.
func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
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
