package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// TxOption configures transaction behavior.
type TxOption func(*pgx.TxOptions)

// WithIsolation sets the transaction isolation level.
// Common values: pgx.Serializable, pgx.RepeatableRead, pgx.ReadCommitted.
func WithIsolation(level pgx.TxIsoLevel) TxOption {
	return func(o *pgx.TxOptions) {
		o.IsoLevel = level
	}
}

// WithReadOnly marks the transaction as read-only.
func WithReadOnly() TxOption {
	return func(o *pgx.TxOptions) {
		o.AccessMode = pgx.ReadOnly
	}
}

// WithTx executes fn within a database transaction. On success (fn returns nil),
// the transaction is committed. On error or panic, the transaction is rolled back.
// If fn panics, WithTx re-panics after rolling back.
//
// WithTx acquires a connection from the pool, begins a transaction, and passes
// the pgx.Tx to fn. The caller should use the provided Tx for all database
// operations within the transaction scope.
//
// If the rollback itself fails, the rollback error is joined with the original
// error using ErrTxRollback.
func (db *DB) WithTx(ctx context.Context, fn func(pgx.Tx) error, opts ...TxOption) (err error) {
	db.mu.Lock()
	pool := db.pool
	db.mu.Unlock()

	if pool == nil {
		return fmt.Errorf("postgres: not connected: %w", ErrConnFailed)
	}

	var txOpts pgx.TxOptions
	for _, o := range opts {
		o(&txOpts)
	}

	tx, err := pool.BeginTx(ctx, txOpts)
	if err != nil {
		return fmt.Errorf("postgres: begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			// Attempt rollback on panic, then re-panic.
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				err = fmt.Errorf("postgres: rollback failed (%w) after: %w", rbErr, ErrTxRollback)
				return
			}
			err = fmt.Errorf("postgres: %w: %w", ErrTxRollback, err)
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit transaction: %w", err)
	}

	return nil
}
