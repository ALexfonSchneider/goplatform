package s3

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

func TestNew_Options(t *testing.T) {
	c, err := New(
		WithEndpoint("minio.example.com:9000"),
		WithRegion("eu-west-1"),
		WithCredentials("myaccess", "mysecret"),
		WithBucket("test-bucket"),
		WithUseSSL(true),
		WithLogger(platform.NopLogger()),
	)
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.Equal(t, "minio.example.com:9000", c.endpoint)
	assert.Equal(t, "eu-west-1", c.region)
	assert.Equal(t, "myaccess", c.accessKey)
	assert.Equal(t, "mysecret", c.secretKey)
	assert.Equal(t, "test-bucket", c.bucket)
	assert.True(t, c.useSSL)
}

func TestNew_Defaults(t *testing.T) {
	c, err := New(WithBucket("my-bucket"))
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.Equal(t, "us-east-1", c.region)
	assert.False(t, c.useSSL)
	assert.Equal(t, "", c.endpoint)
	assert.Equal(t, "", c.accessKey)
	assert.Equal(t, "", c.secretKey)
	assert.NotNil(t, c.logger)
}

func TestNew_NoBucket(t *testing.T) {
	c, err := New()
	require.Error(t, err)
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), "bucket is required")
}

func TestClient_UploadBeforeStart(t *testing.T) {
	c, err := New(WithBucket("test-bucket"))
	require.NoError(t, err)

	err = c.Upload(context.Background(), "key", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestClient_DownloadBeforeStart(t *testing.T) {
	c, err := New(WithBucket("test-bucket"))
	require.NoError(t, err)

	rc, err := c.Download(context.Background(), "key")
	require.Error(t, err)
	assert.Nil(t, rc)
	assert.Contains(t, err.Error(), "not started")
}

func TestClient_StopBeforeStart(t *testing.T) {
	c, err := New(WithBucket("test-bucket"))
	require.NoError(t, err)

	// Stop before Start should be a no-op without error.
	err = c.Stop(context.Background())
	assert.NoError(t, err)
}

func TestClient_S3ClientBeforeStart(t *testing.T) {
	c, err := New(WithBucket("test-bucket"))
	require.NoError(t, err)

	assert.Nil(t, c.S3Client())
}

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		target error
	}{
		{
			name:   "ErrNotFound via errors.Is",
			err:    ErrNotFound,
			target: ErrNotFound,
		},
		{
			name:   "ErrBucketNotFound via errors.Is",
			err:    ErrBucketNotFound,
			target: ErrBucketNotFound,
		},
		{
			name:   "wrapped ErrNotFound",
			err:    errors.Join(ErrNotFound, errors.New("extra")),
			target: ErrNotFound,
		},
		{
			name:   "wrapped ErrBucketNotFound",
			err:    errors.Join(ErrBucketNotFound, errors.New("extra")),
			target: ErrBucketNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, errors.Is(tt.err, tt.target))
		})
	}
}

func TestClient_InterfaceCompliance(t *testing.T) {
	// These are also checked at compile time via the var _ declarations,
	// but explicit test assertions make the requirement visible in test output.
	var client interface{} = &Client{}

	_, ok := client.(platform.Component)
	assert.True(t, ok, "Client should implement platform.Component")

	_, ok = client.(platform.HealthChecker)
	assert.True(t, ok, "Client should implement platform.HealthChecker")
}

func TestClient_DeleteBeforeStart(t *testing.T) {
	c, err := New(WithBucket("test-bucket"))
	require.NoError(t, err)

	err = c.Delete(context.Background(), "key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestClient_ExistsBeforeStart(t *testing.T) {
	c, err := New(WithBucket("test-bucket"))
	require.NoError(t, err)

	exists, err := c.Exists(context.Background(), "key")
	require.Error(t, err)
	assert.False(t, exists)
	assert.Contains(t, err.Error(), "not started")
}

func TestClient_PresignedURLBeforeStart(t *testing.T) {
	c, err := New(WithBucket("test-bucket"))
	require.NoError(t, err)

	url, err := c.PresignedURL(context.Background(), "key", 0)
	require.Error(t, err)
	assert.Empty(t, url)
	assert.Contains(t, err.Error(), "not started")
}

func TestClient_HealthCheckBeforeStart(t *testing.T) {
	c, err := New(WithBucket("test-bucket"))
	require.NoError(t, err)

	err = c.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestWithContentType(t *testing.T) {
	uo := &uploadOptions{}
	WithContentType("application/json")(uo)
	assert.Equal(t, "application/json", uo.contentType)
}
