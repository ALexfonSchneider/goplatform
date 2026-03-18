package broker

import "errors"

// ErrBrokerUnavailable is returned when the broker cannot be reached or is not
// ready to accept requests. Callers should treat this as a transient failure
// and retry after a back-off period.
var ErrBrokerUnavailable = errors.New("broker: unavailable")

// ErrDLQFailed is returned when a message that failed processing could not be
// forwarded to the dead-letter queue. This typically indicates a severe
// infrastructure problem that requires operator attention.
var ErrDLQFailed = errors.New("broker: dlq publish failed")
