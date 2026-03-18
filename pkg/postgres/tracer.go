package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ctxKey is an unexported type used for context keys in this package.
type ctxKey int

const (
	// queryStartTimeKey stores the query start time in the context.
	queryStartTimeKey ctxKey = iota

	// queryDataKey stores the SQL and args in the context for the end hook.
	queryDataKey
)

// queryStartData holds query metadata stored in context between TraceQueryStart and TraceQueryEnd.
type queryStartData struct {
	sql  string
	args []any
}

// queryTracer implements pgx.QueryTracer to invoke registered QueryHook functions
// and optionally create OpenTelemetry spans for each query execution.
type queryTracer struct {
	hooks []QueryHook
	tp    trace.TracerProvider
}

// TraceQueryStart is called before a query is executed. It records the start time
// in the context and optionally starts an OTel span.
func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx = context.WithValue(ctx, queryStartTimeKey, time.Now())
	ctx = context.WithValue(ctx, queryDataKey, &queryStartData{
		sql:  data.SQL,
		args: data.Args,
	})

	if t.tp != nil {
		tracer := t.tp.Tracer("github.com/ALexfonSchneider/goplatform/pkg/postgres")
		ctx, _ = tracer.Start(ctx, "postgres.query",
			trace.WithAttributes(
				attribute.String("db.system", "postgresql"),
				attribute.String("db.statement", data.SQL),
			),
		)
		return ctx
	}

	return ctx
}

// TraceQueryEnd is called after a query completes. It calculates the duration,
// invokes all registered hooks, and ends any active OTel span.
func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	startTime, _ := ctx.Value(queryStartTimeKey).(time.Time)
	duration := time.Since(startTime)

	startData, _ := ctx.Value(queryDataKey).(*queryStartData)

	var sql string
	var args []any
	if startData != nil {
		sql = startData.sql
		args = startData.args
	}

	hookData := QueryHookData{
		SQL:      sql,
		Args:     args,
		Duration: duration,
		Err:      data.Err,
	}

	for _, hook := range t.hooks {
		hook(ctx, hookData)
	}

	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		if data.Err != nil {
			span.RecordError(data.Err)
			span.SetStatus(codes.Error, data.Err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}
}
