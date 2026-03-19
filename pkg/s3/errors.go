package s3

import "errors"

// ErrNotFound is returned when a requested object does not exist in the bucket.
// Callers can use errors.Is(err, ErrNotFound) to detect missing objects.
var ErrNotFound = errors.New("s3: object not found")

// ErrBucketNotFound is returned when the configured bucket does not exist
// or is not accessible.
var ErrBucketNotFound = errors.New("s3: bucket not found")
