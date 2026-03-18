package platform

// Plugin is an extension point for the App. Plugins are initialized before
// components start and closed after components stop.
type Plugin interface {
	// Name returns the plugin's unique identifier.
	Name() string

	// Init initializes the plugin. The PluginContext provides access to
	// component registration, hook registration, and the application logger.
	Init(reg PluginContext) error

	// Close releases any resources held by the plugin.
	Close() error
}

// PluginContext provides a restricted view of the App to plugins during initialization.
type PluginContext interface {
	// Register adds a named component to the application.
	Register(name string, c Component) error

	// Component retrieves a previously registered component by name.
	Component(name string) (Component, bool)

	// OnBeforeStart registers a hook that runs before each component starts.
	OnBeforeStart(h BeforeStartHook)

	// OnAfterStart registers a hook that runs after all components have started.
	OnAfterStart(h AfterStartHook)

	// OnBeforeStop registers a hook that runs before components begin stopping.
	OnBeforeStop(h BeforeStopHook)

	// OnAfterStop registers a hook that runs after all components have stopped.
	OnAfterStop(h AfterStopHook)

	// Logger returns the application's logger.
	Logger() Logger
}
