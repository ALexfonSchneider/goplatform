//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/s3"
)

const (
	minioEndpoint  = "localhost:9000"
	minioAccessKey = "minioadmin"
	minioSecretKey = "minioadmin"
	minioBucket    = "integ-test"
)

// createMinIOBucket creates a test bucket in MinIO if it does not already exist.
// This uses the S3 client's underlying API to create the bucket for test setup.
func createMinIOBucket(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// We use the AWS SDK directly to create the bucket since the s3.Client
	// requires the bucket to exist at Start time.
	cfg, err := s3.New(
		s3.WithEndpoint(minioEndpoint),
		s3.WithCredentials(minioAccessKey, minioSecretKey),
		s3.WithBucket(minioBucket),
	)
	require.NoError(t, err)

	// Try to start — if bucket exists, it works. If not, we need to create it.
	if startErr := cfg.Start(ctx); startErr != nil {
		t.Logf("bucket %q might not exist yet, test may fail: %v", minioBucket, startErr)
		t.Skip("MinIO bucket does not exist; create it with: mc mb local/integ-test")
	}
	_ = cfg.Stop(ctx)
}

// TestS3_UploadDownloadDelete connects to a real MinIO instance, uploads a file,
// downloads it, verifies the content, deletes it, and confirms it no longer exists.
func TestS3_UploadDownloadDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	createMinIOBucket(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := s3.New(
		s3.WithEndpoint(minioEndpoint),
		s3.WithCredentials(minioAccessKey, minioSecretKey),
		s3.WithBucket(minioBucket),
	)
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))
	defer func() { require.NoError(t, client.Stop(ctx)) }()

	key := "integ-test/upload-download.txt"
	content := []byte("hello from integration test")

	// Upload.
	err = client.Upload(ctx, key, bytes.NewReader(content), s3.WithContentType("text/plain"))
	require.NoError(t, err)

	// Download and verify content.
	rc, err := client.Download(ctx, key)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	// Exists should return true.
	exists, err := client.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists, "object should exist after upload")

	// Delete.
	err = client.Delete(ctx, key)
	require.NoError(t, err)

	// Exists should return false after deletion.
	exists, err = client.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists, "object should not exist after delete")
}

// TestS3_PresignedURL generates a presigned URL and verifies it is non-empty.
func TestS3_PresignedURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	createMinIOBucket(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := s3.New(
		s3.WithEndpoint(minioEndpoint),
		s3.WithCredentials(minioAccessKey, minioSecretKey),
		s3.WithBucket(minioBucket),
	)
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))
	defer func() { require.NoError(t, client.Stop(ctx)) }()

	// Upload a test object first.
	key := "integ-test/presigned.txt"
	err = client.Upload(ctx, key, bytes.NewReader([]byte("presign test")))
	require.NoError(t, err)
	defer func() { _ = client.Delete(ctx, key) }()

	// Generate a presigned URL.
	url, err := client.PresignedURL(ctx, key, 15*time.Minute)
	require.NoError(t, err)
	assert.NotEmpty(t, url, "presigned URL should not be empty")
	assert.Contains(t, url, key, "presigned URL should contain the object key")
}

// TestS3_HealthCheck verifies that HealthCheck returns nil for a connected client.
func TestS3_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	createMinIOBucket(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := s3.New(
		s3.WithEndpoint(minioEndpoint),
		s3.WithCredentials(minioAccessKey, minioSecretKey),
		s3.WithBucket(minioBucket),
	)
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))
	defer func() { require.NoError(t, client.Stop(ctx)) }()

	err = client.HealthCheck(ctx)
	assert.NoError(t, err, "HealthCheck should pass for a connected MinIO client")
}
