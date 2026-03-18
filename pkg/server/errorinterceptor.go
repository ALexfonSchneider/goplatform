package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// mapPlatformCode converts a platform.ErrorCode to the corresponding connect.Code.
//
// Mapping:
//
//	platform.CodeNotFound        → connect.CodeNotFound        (HTTP 404)
//	platform.CodeAlreadyExists   → connect.CodeAlreadyExists   (HTTP 409)
//	platform.CodeInvalidArgument → connect.CodeInvalidArgument (HTTP 400)
//	platform.CodePermissionDenied→ connect.CodePermissionDenied(HTTP 403)
//	platform.CodeUnauthenticated → connect.CodeUnauthenticated (HTTP 401)
//	platform.CodeUnavailable     → connect.CodeUnavailable     (HTTP 503)
//	anything else                → connect.CodeInternal        (HTTP 500)
func mapPlatformCode(code platform.ErrorCode) connect.Code {
	switch code {
	case platform.CodeNotFound:
		return connect.CodeNotFound
	case platform.CodeAlreadyExists:
		return connect.CodeAlreadyExists
	case platform.CodeInvalidArgument:
		return connect.CodeInvalidArgument
	case platform.CodePermissionDenied:
		return connect.CodePermissionDenied
	case platform.CodeUnauthenticated:
		return connect.CodeUnauthenticated
	case platform.CodeUnavailable:
		return connect.CodeUnavailable
	default:
		return connect.CodeInternal
	}
}

// ErrorInterceptor returns a ConnectRPC unary interceptor that maps
// platform.Error codes to connect.Code.
//
// When a handler returns a platform.Error, the interceptor creates a
// connect.Error with the mapped code and the error's Message() (safe
// for clients). When a handler returns a plain Go error, the interceptor
// maps it to connect.CodeInternal with a generic "internal error" message —
// internal details (DB errors, stack traces, etc.) are never exposed to clients.
//
// Usage:
//
//	path, handler := myv1connect.NewMyServiceHandler(svc,
//	    connect.WithInterceptors(server.ErrorInterceptor()),
//	)
//	srv.Mount(path, handler)
func ErrorInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err == nil {
				return resp, nil
			}

			// Check if the error is a domain error with a known code.
			var pe platform.Error
			if errors.As(err, &pe) {
				return nil, connect.NewError(mapPlatformCode(pe.Code()), pe)
			}

			// Unknown errors → generic internal error. Never leak internals.
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
	}
}
