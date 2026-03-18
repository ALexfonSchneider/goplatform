package platform

import "context"

// BeforeStartHook is called before each component is started.
// Returning an error aborts the startup sequence.
type BeforeStartHook func(ctx context.Context, name string) error

// AfterStartHook is called after all components have been successfully started.
type AfterStartHook func(ctx context.Context, name string)

// BeforeStopHook is called before components begin shutting down.
type BeforeStopHook func(ctx context.Context, name string)

// AfterStopHook is called after all components have been stopped.
// The err parameter carries any errors collected during the stop phase.
type AfterStopHook func(ctx context.Context, name string, err error)
