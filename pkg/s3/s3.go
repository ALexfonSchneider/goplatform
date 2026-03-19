// Package s3 provides an S3/MinIO client built on AWS SDK v2 that implements
// platform.Component and platform.HealthChecker.
//
// Client wraps the AWS S3 SDK v2 and manages the full lifecycle: creating the
// S3 client and verifying bucket accessibility on Start, and performing health
// checks via HeadBucket. It provides convenient Upload, Download, Delete,
// Exists, and PresignedURL operations.
//
// Compatible with both AWS S3 and MinIO via a custom endpoint with path-style
// addressing.
//
// Usage:
//
//	c, _ := s3.New(
//	    s3.WithEndpoint("localhost:9000"),
//	    s3.WithCredentials("minioadmin", "minioadmin"),
//	    s3.WithBucket("my-bucket"),
//	    s3.WithLogger(logger),
//	)
//	app.Register("s3", c)
//
//	// Upload a file:
//	_ = c.Upload(ctx, "photos/cat.jpg", file, s3.WithContentType("image/jpeg"))
//
//	// Download a file:
//	rc, _ := c.Download(ctx, "photos/cat.jpg")
//	defer rc.Close()
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// Compile-time checks that Client implements platform.Component and
// platform.HealthChecker.
var (
	_ platform.Component     = (*Client)(nil)
	_ platform.HealthChecker = (*Client)(nil)
)

// Client wraps an AWS S3 SDK v2 client and implements platform.Component and
// platform.HealthChecker. It is compatible with both AWS S3 and MinIO via a
// custom endpoint with path-style addressing.
//
// Client is safe for concurrent use.
type Client struct {
	mu sync.Mutex

	// Configuration set via options.
	endpoint  string
	region    string
	accessKey string
	secretKey string
	bucket    string
	useSSL    bool
	logger    platform.Logger

	// Runtime state.
	s3Client *awss3.Client
	started  bool
}

// Option configures a Client instance.
type Option func(*Client)

// uploadOptions holds optional parameters for Upload.
type uploadOptions struct {
	contentType string
}

// UploadOption configures an Upload call.
type UploadOption func(*uploadOptions)

// WithEndpoint sets the S3-compatible endpoint address in "host:port" format.
// Use this for MinIO or other S3-compatible services (e.g., "localhost:9000").
// When set, path-style addressing is automatically enabled for MinIO compatibility.
func WithEndpoint(endpoint string) Option {
	return func(c *Client) {
		c.endpoint = endpoint
	}
}

// WithRegion sets the AWS region. The default is "us-east-1".
func WithRegion(region string) Option {
	return func(c *Client) {
		c.region = region
	}
}

// WithCredentials sets the access key ID and secret access key used for
// authentication with the S3 service.
func WithCredentials(accessKey, secretKey string) Option {
	return func(c *Client) {
		c.accessKey = accessKey
		c.secretKey = secretKey
	}
}

// WithBucket sets the target S3 bucket name. This option is required.
func WithBucket(bucket string) Option {
	return func(c *Client) {
		c.bucket = bucket
	}
}

// WithUseSSL enables or disables HTTPS for the endpoint connection.
// The default is false (HTTP). Only applies when a custom endpoint is set.
func WithUseSSL(useSSL bool) Option {
	return func(c *Client) {
		c.useSSL = useSSL
	}
}

// WithLogger sets the structured logger used by Client for lifecycle events
// and internal diagnostics.
func WithLogger(l platform.Logger) Option {
	return func(c *Client) {
		c.logger = l
	}
}

// WithContentType sets the Content-Type header for the uploaded object.
func WithContentType(ct string) UploadOption {
	return func(o *uploadOptions) {
		o.contentType = ct
	}
}

// New creates a new Client with the given options. The returned Client is not
// yet connected -- call Start to create the S3 client and verify bucket access.
//
// New returns an error if the bucket option is not set.
//
// Default values:
//   - region: "us-east-1"
//   - useSSL: false
//   - logger: platform.NopLogger()
func New(opts ...Option) (*Client, error) {
	c := &Client{
		region: "us-east-1",
		logger: platform.NopLogger(),
	}
	for _, opt := range opts {
		opt(c)
	}

	if c.bucket == "" {
		return nil, fmt.Errorf("s3: bucket is required")
	}

	return c, nil
}

// Start creates the S3 client and verifies bucket accessibility via HeadBucket.
// It implements platform.Component.
//
// Start configures static credentials and, when a custom endpoint is set,
// enables path-style addressing for MinIO compatibility. Calling Start on an
// already-started Client returns an error.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return fmt.Errorf("s3: already started")
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(c.region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.accessKey, c.secretKey, ""),
		),
	)
	if err != nil {
		return fmt.Errorf("s3: load aws config: %w", err)
	}

	c.s3Client = awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		if c.endpoint != "" {
			scheme := "http"
			if c.useSSL {
				scheme = "https"
			}
			o.BaseEndpoint = aws.String(scheme + "://" + c.endpoint)
			o.UsePathStyle = true // required for MinIO
		}
	})

	// Verify bucket accessibility.
	_, err = c.s3Client.HeadBucket(ctx, &awss3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		c.s3Client = nil
		return fmt.Errorf("s3: head bucket %q: %w", c.bucket, err)
	}

	c.started = true
	c.logger.Info("s3: connected", "endpoint", c.endpoint, "bucket", c.bucket, "region", c.region)
	return nil
}

// Stop is a no-op for S3 since the HTTP client is stateless.
// It implements platform.Component. Stop is safe to call multiple times.
func (c *Client) Stop(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return nil
	}

	c.started = false
	c.s3Client = nil
	c.logger.Info("s3: stopped")
	return nil
}

// HealthCheck verifies bucket accessibility via HeadBucket.
// It implements platform.HealthChecker.
// Returns nil if the bucket is accessible, or an error if the check fails
// or the client has not been started.
func (c *Client) HealthCheck(ctx context.Context) error {
	client := c.getClient()
	if client == nil {
		return fmt.Errorf("s3: not started")
	}

	_, err := client.HeadBucket(ctx, &awss3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		return fmt.Errorf("s3: health check failed: %w", err)
	}
	return nil
}

// Upload puts an object into the configured bucket. The key is the object's
// path within the bucket. The reader provides the object's content.
//
// Use UploadOption values such as WithContentType to set additional metadata.
// Upload returns an error if the client has not been started.
func (c *Client) Upload(ctx context.Context, key string, reader io.Reader, opts ...UploadOption) error {
	client := c.getClient()
	if client == nil {
		return fmt.Errorf("s3: not started")
	}

	uo := &uploadOptions{}
	for _, opt := range opts {
		opt(uo)
	}

	input := &awss3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   reader,
	}
	if uo.contentType != "" {
		input.ContentType = aws.String(uo.contentType)
	}

	_, err := client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("s3: upload %q: %w", key, err)
	}
	return nil
}

// Download gets an object from the configured bucket. The caller must close
// the returned io.ReadCloser when done reading.
//
// Download returns ErrNotFound (wrapped) if the object does not exist.
// Download returns an error if the client has not been started.
func (c *Client) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	client := c.getClient()
	if client == nil {
		return nil, fmt.Errorf("s3: not started")
	}

	output, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFoundError(err) {
			return nil, fmt.Errorf("s3: download %q: %w", key, ErrNotFound)
		}
		return nil, fmt.Errorf("s3: download %q: %w", key, err)
	}

	return output.Body, nil
}

// Delete removes an object from the configured bucket. Deleting a
// non-existent object is not considered an error (S3 DELETE is idempotent).
//
// Delete returns an error if the client has not been started.
func (c *Client) Delete(ctx context.Context, key string) error {
	client := c.getClient()
	if client == nil {
		return fmt.Errorf("s3: not started")
	}

	_, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3: delete %q: %w", key, err)
	}
	return nil
}

// Exists checks if an object exists in the configured bucket. It returns true
// if the object is found, false if it does not exist, and an error for any
// other failure.
//
// Exists returns an error if the client has not been started.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	client := c.getClient()
	if client == nil {
		return false, fmt.Errorf("s3: not started")
	}

	_, err := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFoundError(err) {
			return false, nil
		}
		return false, fmt.Errorf("s3: exists %q: %w", key, err)
	}
	return true, nil
}

// PresignedURL generates a presigned GET URL for the object identified by key.
// The URL is valid for the specified duration.
//
// PresignedURL returns an error if the client has not been started.
func (c *Client) PresignedURL(ctx context.Context, key string, expires time.Duration) (string, error) {
	client := c.getClient()
	if client == nil {
		return "", fmt.Errorf("s3: not started")
	}

	presigner := awss3.NewPresignClient(client)

	req, err := presigner.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, func(opts *awss3.PresignOptions) {
		opts.Expires = expires
	})
	if err != nil {
		return "", fmt.Errorf("s3: presign %q: %w", key, err)
	}

	return req.URL, nil
}

// S3Client returns the underlying AWS S3 client for direct access to any
// operation not covered by the convenience methods. Returns nil if the client
// has not been started.
func (c *Client) S3Client() *awss3.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.s3Client
}

// getClient returns the underlying S3 client in a thread-safe manner.
// Returns nil if the client has not been started.
func (c *Client) getClient() *awss3.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.s3Client
}

// isNotFoundError checks whether the given error indicates that the requested
// object or bucket was not found. It inspects the Smithy API error code.
func isNotFoundError(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NotFound", "404":
		return true
	}
	return false
}
