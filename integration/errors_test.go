//go:build integration

package integration

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// TestPlatformError_NewAndInspect creates platform errors and verifies their
// properties including Code(), Message(), and Error() string formatting.
func TestPlatformError_NewAndInspect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	tests := []struct {
		name    string
		err     platform.Error
		code    platform.ErrorCode
		message string
	}{
		{
			name:    "not found",
			err:     platform.NewError(platform.CodeNotFound, "user not found"),
			code:    platform.CodeNotFound,
			message: "user not found",
		},
		{
			name:    "already exists",
			err:     platform.NewError(platform.CodeAlreadyExists, "order already exists"),
			code:    platform.CodeAlreadyExists,
			message: "order already exists",
		},
		{
			name:    "invalid argument",
			err:     platform.NewErrorf(platform.CodeInvalidArgument, "field %q is required", "email"),
			code:    platform.CodeInvalidArgument,
			message: `field "email" is required`,
		},
		{
			name:    "internal",
			err:     platform.NewError(platform.CodeInternal, "unexpected failure"),
			code:    platform.CodeInternal,
			message: "unexpected failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.code, tt.err.Code())
			assert.Equal(t, tt.message, tt.err.Message())
			assert.Contains(t, tt.err.Error(), tt.message)
		})
	}
}

// TestPlatformError_WrapAndUnwrap verifies that WrapError creates a chain
// compatible with errors.Is and errors.As.
func TestPlatformError_WrapAndUnwrap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	cause := fmt.Errorf("connection refused")
	wrapped := platform.WrapError(platform.CodeUnavailable, "database unavailable", cause)

	// errors.Is should traverse the chain to the cause.
	assert.True(t, errors.Is(wrapped, cause), "wrapped error should contain cause")

	// errors.As should extract the platform.Error.
	pe, ok := platform.IsError(wrapped)
	require.True(t, ok, "IsError should succeed on wrapped error")
	assert.Equal(t, platform.CodeUnavailable, pe.Code())
	assert.Equal(t, "database unavailable", pe.Message())
}

// TestPlatformError_GetCode verifies GetCode extracts the code from platform
// errors and returns CodeInternal for plain errors.
func TestPlatformError_GetCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	// Platform error.
	pe := platform.NewError(platform.CodePermissionDenied, "access denied")
	assert.Equal(t, platform.CodePermissionDenied, platform.GetCode(pe))

	// Plain error.
	plain := fmt.Errorf("some error")
	assert.Equal(t, platform.CodeInternal, platform.GetCode(plain),
		"GetCode should return CodeInternal for non-platform errors")
}

// TestPlatformError_CodeString verifies the string representation of error codes.
func TestPlatformError_CodeString(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	tests := []struct {
		code platform.ErrorCode
		want string
	}{
		{platform.CodeInternal, "internal"},
		{platform.CodeNotFound, "not_found"},
		{platform.CodeAlreadyExists, "already_exists"},
		{platform.CodeInvalidArgument, "invalid_argument"},
		{platform.CodePermissionDenied, "permission_denied"},
		{platform.CodeUnauthenticated, "unauthenticated"},
		{platform.CodeUnavailable, "unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.code.String())
		})
	}
}
