package postgres

import (
	"context"
	"time"
)

// QueryHookData contains information about a completed query, passed to query hooks.
// It is populated after each query execution and delivered to all registered QueryHook
// functions. Duration measures wall-clock time from query start to completion.
type QueryHookData struct {
	// SQL is the query string that was executed.
	SQL string

	// Args contains the bind parameters passed to the query.
	Args []any

	// Duration is the wall-clock time the query took to execute.
	Duration time.Duration

	// Err is the error returned by the query, or nil on success.
	Err error
}

// QueryHook is a function called after each query execution. Hooks receive the
// request context and a QueryHookData describing the completed query. Hooks
// must not block for extended periods as they run synchronously in the query path.
type QueryHook func(ctx context.Context, data QueryHookData)
