package platform

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewError(t *testing.T) {
	err := NewError(CodeNotFound, "thing missing")

	pe, ok := IsError(err)
	require.True(t, ok)
	assert.Equal(t, CodeNotFound, pe.Code())
	assert.Equal(t, "thing missing", pe.Message())
}

func TestNewErrorf(t *testing.T) {
	err := NewErrorf(CodeInvalidArgument, "field %q invalid: %d", "age", 42)

	pe, ok := IsError(err)
	require.True(t, ok)
	assert.Equal(t, CodeInvalidArgument, pe.Code())
	assert.Equal(t, `field "age" invalid: 42`, pe.Message())
}

func TestWrapError(t *testing.T) {
	cause := errors.New("connection refused")
	err := WrapError(CodeUnavailable, "db down", cause)

	pe, ok := IsError(err)
	require.True(t, ok)
	assert.Equal(t, CodeUnavailable, pe.Code())
	assert.Equal(t, "db down", pe.Message())

	// errors.Is must see through the wrapping.
	assert.ErrorIs(t, err, cause)
}

func TestIsError(t *testing.T) {
	t.Run("platform error", func(t *testing.T) {
		pe := NewError(CodeInternal, "oops")
		found, ok := IsError(pe)
		assert.True(t, ok)
		assert.Equal(t, CodeInternal, found.Code())
	})

	t.Run("plain error", func(t *testing.T) {
		plain := errors.New("plain")
		_, ok := IsError(plain)
		assert.False(t, ok)
	})

	t.Run("wrapped platform error", func(t *testing.T) {
		inner := NewError(CodeNotFound, "not here")
		wrapped := fmt.Errorf("outer: %w", inner)
		found, ok := IsError(wrapped)
		assert.True(t, ok)
		assert.Equal(t, CodeNotFound, found.Code())
	})
}

func TestGetCode(t *testing.T) {
	t.Run("platform error", func(t *testing.T) {
		err := NewError(CodePermissionDenied, "nope")
		assert.Equal(t, CodePermissionDenied, GetCode(err))
	})

	t.Run("plain error defaults to CodeInternal", func(t *testing.T) {
		err := errors.New("plain")
		assert.Equal(t, CodeInternal, GetCode(err))
	})
}

func TestErrorString(t *testing.T) {
	t.Run("without cause", func(t *testing.T) {
		err := NewError(CodeNotFound, "widget gone")
		s := err.Error()
		assert.Contains(t, s, "not_found")
		assert.Contains(t, s, "widget gone")
	})

	t.Run("with cause", func(t *testing.T) {
		cause := errors.New("timeout")
		err := WrapError(CodeUnavailable, "service down", cause)
		s := err.Error()
		assert.Contains(t, s, "unavailable")
		assert.Contains(t, s, "service down")
		assert.Contains(t, s, "timeout")
	})
}

func TestErrorCodes(t *testing.T) {
	tests := []struct {
		code ErrorCode
		str  string
	}{
		{CodeInternal, "internal"},
		{CodeNotFound, "not_found"},
		{CodeAlreadyExists, "already_exists"},
		{CodeInvalidArgument, "invalid_argument"},
		{CodePermissionDenied, "permission_denied"},
		{CodeUnauthenticated, "unauthenticated"},
		{CodeUnavailable, "unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.str, func(t *testing.T) {
			assert.Equal(t, tt.str, tt.code.String())
		})
	}

	// Unknown code should produce "unknown(N)" string.
	unknown := ErrorCode(999)
	assert.Contains(t, unknown.String(), "unknown")
	assert.Contains(t, unknown.String(), "999")
}
