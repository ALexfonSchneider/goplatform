package config

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Loader loads configuration from multiple sources and unmarshals it into a target struct.
// Sources are applied in order: defaults, YAML files, environment variables.
// Multiple files are loaded in order — later files override earlier ones.
type Loader struct {
	mu            sync.RWMutex
	k             *koanf.Koanf
	filePaths     []string
	envPrefix     string
	defaults      map[string]any
	onReloadError func(error)
}

// Option configures a Loader.
type Option func(*Loader)

// WithFile adds a YAML file path to load configuration from.
// Can be called multiple times — files are loaded in order, later files
// override values from earlier ones.
func WithFile(path string) Option {
	return func(l *Loader) {
		l.filePaths = append(l.filePaths, path)
	}
}

// WithFiles adds multiple YAML file paths to load configuration from.
// Files are loaded in order — later files override values from earlier ones.
func WithFiles(paths ...string) Option {
	return func(l *Loader) {
		l.filePaths = append(l.filePaths, paths...)
	}
}

// WithEnvPrefix sets the environment variable prefix.
// For example, "MYAPP" causes MYAPP_SERVER_ADDR to map to server.addr.
func WithEnvPrefix(prefix string) Option {
	return func(l *Loader) {
		l.envPrefix = prefix
	}
}

// WithDefaults sets default configuration values.
// Keys should use dot notation, e.g. "server.addr".
func WithDefaults(defaults map[string]any) Option {
	return func(l *Loader) {
		l.defaults = defaults
	}
}

// WithOnReloadError sets a callback invoked when config file reload fails
// during Watch. Without this, reload errors are silently ignored and the
// previous configuration remains in effect.
func WithOnReloadError(fn func(error)) Option {
	return func(l *Loader) {
		l.onReloadError = fn
	}
}

// NewLoader creates a new Loader with the given options.
// It immediately loads all configured sources in priority order.
func NewLoader(opts ...Option) (*Loader, error) {
	l := &Loader{
		k: koanf.New("."),
	}

	for _, opt := range opts {
		opt(l)
	}

	if err := l.loadSources(); err != nil {
		return nil, err
	}

	return l, nil
}

// loadSources loads all configuration sources in priority order:
// defaults → YAML file → environment variables.
func (l *Loader) loadSources() error {
	if l.defaults != nil {
		if err := l.k.Load(confmap.Provider(l.defaults, "."), nil); err != nil {
			return fmt.Errorf("config: load defaults: %w", err)
		}
	}

	for _, fp := range l.filePaths {
		if err := l.k.Load(file.Provider(fp), yaml.Parser()); err != nil {
			return fmt.Errorf("config: load file %q: %w", fp, err)
		}
	}

	if l.envPrefix != "" {
		// Use __ (double underscore) as level separator in env vars.
		// Single _ stays as part of the key name.
		// Example: MYAPP__OBSERVE__SERVICE_NAME → observe.service_name
		prefix := l.envPrefix + "__"
		if err := l.k.Load(env.Provider(prefix, ".", func(s string) string {
			key := strings.TrimPrefix(s, prefix)
			key = strings.ToLower(key)
			key = strings.ReplaceAll(key, "__", ".")
			return key
		}), nil); err != nil {
			return fmt.Errorf("config: load env: %w", err)
		}
	}

	return nil
}

// Load unmarshals the merged configuration into the target struct.
// Target must be a pointer to a struct with koanf struct tags.
func (l *Loader) Load(target any) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if err := l.k.Unmarshal("", target); err != nil {
		return fmt.Errorf("config: unmarshal: %w", err)
	}

	return nil
}

// Watch starts watching for configuration file changes and calls onChange when detected.
// It blocks until ctx is cancelled and should be run in a goroutine.
// Returns an error if no file path is configured.
func (l *Loader) Watch(ctx context.Context, onChange func()) error {
	if len(l.filePaths) == 0 {
		return fmt.Errorf("config: watch: no files configured")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("config: watch: create watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	for _, fp := range l.filePaths {
		if err := watcher.Add(fp); err != nil {
			return fmt.Errorf("config: watch: add file %q: %w", fp, err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if event.Has(fsnotify.Write) {
				// Reload all sources into a new instance so priority order is preserved.
				// On failure, roll back to the previous working config.
				l.mu.Lock()
				oldK := l.k
				l.k = koanf.New(".")
				if err := l.loadSources(); err != nil {
					l.k = oldK
					l.mu.Unlock()
					if l.onReloadError != nil {
						l.onReloadError(err)
					}
					continue
				}
				l.mu.Unlock()

				onChange()
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
		}
	}
}
