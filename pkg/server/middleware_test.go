package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// ---------- helpers ----------

// memIdempotencyStore is a simple in-memory IdempotencyStore for tests.
type memIdempotencyStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemIdempotencyStore() *memIdempotencyStore {
	return &memIdempotencyStore{data: make(map[string][]byte)}
}

func (m *memIdempotencyStore) SetNX(_ context.Context, key string, value any, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.data[key]; exists {
		return false, nil
	}

	b, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	m.data[key] = b
	return true, nil
}

func (m *memIdempotencyStore) Get(_ context.Context, key string, dest any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.data[key]
	if !ok {
		return nil
	}
	return json.Unmarshal(b, dest)
}

func (m *memIdempotencyStore) Set(_ context.Context, key string, value any, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.data[key] = b
	return nil
}

// ---------- Recovery tests ----------

func TestRecovery_PanicHandler(t *testing.T) {
	log := platform.NopLogger()
	handler := Recovery(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	}))

	// First request: handler panics, middleware returns 500.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]string
	err := json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "internal server error", body["error"])

	// Second request: middleware still works after previous panic.
	okHandler := Recovery(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/ok", nil)
	okHandler.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "ok", rec2.Body.String())
}

// ---------- RequestLogging tests ----------

func TestRequestLogging_LogsRequest(t *testing.T) {
	var buf bytes.Buffer
	log := platform.NewSlogLogger(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := RequestLogging(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/things", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	// Parse the JSON log line and verify fields.
	var logEntry map[string]any
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "POST", logEntry["method"])
	assert.Equal(t, "/api/things", logEntry["path"])
	assert.Equal(t, float64(http.StatusCreated), logEntry["status"])
	assert.Contains(t, logEntry, "duration")
	assert.Contains(t, logEntry, "request_id")
}

func TestRequestLogging_SkipPaths(t *testing.T) {
	var buf bytes.Buffer
	log := platform.NewSlogLogger(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := RequestLogging(log, "/healthz/live")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz/live", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, buf.String(), "skipped path should not produce a log entry")
}

func TestRequestLogging_UsesExistingRequestID(t *testing.T) {
	var buf bytes.Buffer
	log := platform.NewSlogLogger(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := RequestLogging(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-Id", "my-custom-id")
	handler.ServeHTTP(rec, req)

	var logEntry map[string]any
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)
	assert.Equal(t, "my-custom-id", logEntry["request_id"])
}

// ---------- Idempotency tests ----------

func TestIdempotencyMiddleware_CachedResponse(t *testing.T) {
	store := newMemIdempotencyStore()
	callCount := 0

	handler := IdempotencyMiddleware(store, 5*time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"created"}`))
	}))

	// First request with idempotency key.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req1.Header.Set("Idempotency-Key", "key-123")
	handler.ServeHTTP(rec1, req1)

	assert.Equal(t, http.StatusOK, rec1.Code)
	assert.Equal(t, `{"result":"created"}`, rec1.Body.String())
	assert.Equal(t, 1, callCount)

	// Second request with the same idempotency key: should return cached response.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req2.Header.Set("Idempotency-Key", "key-123")
	handler.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, `{"result":"created"}`, rec2.Body.String())
	assert.Equal(t, 1, callCount, "handler should not be called for duplicate key")
}

func TestIdempotencyMiddleware_ConcurrentConflict(t *testing.T) {
	store := newMemIdempotencyStore()

	// Pre-set the key as "processing" to simulate an in-flight request.
	err := store.Set(context.Background(), "idem:key-conflict", "processing", 5*time.Minute)
	require.NoError(t, err)

	// Manually set SetNX to return false (key already exists).
	handler := IdempotencyMiddleware(store, 5*time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("should not reach"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req.Header.Set("Idempotency-Key", "key-conflict")
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)

	body, err := io.ReadAll(rec.Result().Body)
	require.NoError(t, err)
	defer func() { _ = rec.Result().Body.Close() }()

	var respBody map[string]string
	err = json.Unmarshal(body, &respBody)
	require.NoError(t, err)
	assert.Equal(t, "concurrent request with same idempotency key", respBody["error"])
}

func TestIdempotencyMiddleware_NoHeader(t *testing.T) {
	store := newMemIdempotencyStore()
	callCount := 0

	handler := IdempotencyMiddleware(store, 5*time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("no idempotency"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/orders", nil)
	// No Idempotency-Key header set.
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "no idempotency", rec.Body.String())
	assert.Equal(t, 1, callCount)
}
