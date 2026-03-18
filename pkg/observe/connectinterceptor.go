package observe

import (
	"context"

	"connectrpc.com/connect"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ConnectInterceptor returns a ConnectRPC unary interceptor that creates spans
// for each RPC call with rpc.method and rpc.service attributes.
func ConnectInterceptor(tp trace.TracerProvider) connect.UnaryInterceptorFunc {
	tracer := tp.Tracer("observe.connect")

	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := req.Spec().Procedure
			ctx, span := tracer.Start(ctx, procedure,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("rpc.system", "connect"),
					attribute.String("rpc.method", procedure),
					attribute.String("rpc.service", req.Spec().Procedure),
				),
			)
			defer span.End()

			resp, err := next(ctx, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}

			return resp, err
		})
	})
}
