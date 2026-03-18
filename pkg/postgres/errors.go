package postgres

import "errors"

// ErrConnFailed is returned when the database connection cannot be established
// or the initial ping fails during Start.
var ErrConnFailed = errors.New("postgres: connection failed")

// ErrTxRollback is returned when a transaction is rolled back due to an error
// returned by the function passed to WithTx.
var ErrTxRollback = errors.New("postgres: transaction rolled back")
