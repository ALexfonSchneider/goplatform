//go:build integration

package integration

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/ALexfonSchneider/goplatform/pkg/s3"
)

// createMinIOBucket creates a bucket via the raw AWS SDK before the s3.Client
// is started, because Start calls HeadBucket and will fail if the bucket does
// not exist.
func createMinIOBucketTC(ctx context.Context, t *testing.T, endpoint, bucket string) {
	t.Helper()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("minioadmin", "minioadmin", ""),
		),
	)
	require.NoError(t, err)

	rawS3 := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String("http://" + endpoint)
		o.UsePathStyle = true
	})

	_, err = rawS3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
}

func TestTC_S3_UploadDownloadDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcminio.Run(ctx, "minio/minio:latest")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	endpoint, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)

	const bucket = "test-bucket"
	createMinIOBucketTC(ctx, t, endpoint, bucket)

	c, err := s3.New(
		s3.WithEndpoint(endpoint),
		s3.WithCredentials("minioadmin", "minioadmin"),
		s3.WithBucket(bucket),
	)
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	defer func() { require.NoError(t, c.Stop(ctx)) }()

	// Upload
	content := "hello s3 world"
	err = c.Upload(ctx, "test/file.txt", strings.NewReader(content), s3.WithContentType("text/plain"))
	require.NoError(t, err)

	// Download
	rc, err := c.Download(ctx, "test/file.txt")
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))

	// Delete
	require.NoError(t, c.Delete(ctx, "test/file.txt"))

	// Verify deleted: Exists should return false.
	exists, err := c.Exists(ctx, "test/file.txt")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestTC_S3_Exists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcminio.Run(ctx, "minio/minio:latest")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	endpoint, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)

	const bucket = "test-bucket"
	createMinIOBucketTC(ctx, t, endpoint, bucket)

	c, err := s3.New(
		s3.WithEndpoint(endpoint),
		s3.WithCredentials("minioadmin", "minioadmin"),
		s3.WithBucket(bucket),
	)
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	defer func() { require.NoError(t, c.Stop(ctx)) }()

	// Object does not exist yet.
	exists, err := c.Exists(ctx, "nonexistent.txt")
	require.NoError(t, err)
	assert.False(t, exists)

	// Upload and check existence.
	err = c.Upload(ctx, "exists-test.txt", strings.NewReader("data"))
	require.NoError(t, err)

	exists, err = c.Exists(ctx, "exists-test.txt")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestTC_S3_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcminio.Run(ctx, "minio/minio:latest")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	endpoint, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)

	const bucket = "test-bucket"
	createMinIOBucketTC(ctx, t, endpoint, bucket)

	c, err := s3.New(
		s3.WithEndpoint(endpoint),
		s3.WithCredentials("minioadmin", "minioadmin"),
		s3.WithBucket(bucket),
	)
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	defer func() { require.NoError(t, c.Stop(ctx)) }()

	require.NoError(t, c.HealthCheck(ctx))
}
