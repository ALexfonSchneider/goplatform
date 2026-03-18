package redis

import "errors"

// ErrKeyNotFound is returned when a key does not exist in Redis.
// Callers can use errors.Is(err, ErrKeyNotFound) to detect missing keys.
var ErrKeyNotFound = errors.New("redis: key not found")
