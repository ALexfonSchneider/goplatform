package platform

//go:generate mockgen -destination=mocks/mock_component.go -package=mocks github.com/ALexfonSchneider/goplatform/pkg/platform Component,Named,HealthChecker
//go:generate mockgen -destination=mocks/mock_plugin.go -package=mocks github.com/ALexfonSchneider/goplatform/pkg/platform Plugin,PluginContext
//go:generate mockgen -destination=mocks/mock_composites.go -package=mocks github.com/ALexfonSchneider/goplatform/pkg/platform/mocks NamedComponent,HealthCheckComponent
