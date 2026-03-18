// Package scaffold provides project generation from embedded templates.
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed all:templates
var templateFS embed.FS

//go:embed validate.proto
var validateProto []byte

// Data holds the template variables for project generation.
type Data struct {
	Name     string // project name (e.g. "orderservice")
	Module   string // Go module path (e.g. "github.com/user/orderservice")
	Postgres bool
	Kafka    bool
	NATS     bool
	Redis    bool
	Temporal bool
	Connect  bool
}

// Generator renders scaffold templates into a target directory.
type Generator struct {
	funcs template.FuncMap
}

// New creates a new Generator.
func New() *Generator {
	return &Generator{
		funcs: template.FuncMap{
			"upper":  strings.ToUpper,
			"title":  titleCase,
			"pascal": pascalCase,
		},
	}
}

// titleCase converts a string to title case (first letter uppercase).
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// pascalCase converts a kebab-case or snake_case string to PascalCase.
// "order-service" → "OrderService", "my_app" → "MyApp", "simple" → "Simple".
func pascalCase(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		if r == '-' || r == '_' {
			upper = true
			continue
		}
		if upper {
			b.WriteString(strings.ToUpper(string(r)))
			upper = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// componentEnabled returns true if the given component directory should be
// rendered for the provided Data.
func componentEnabled(component string, data Data) bool {
	switch component {
	case "postgres":
		return data.Postgres
	case "kafka":
		return data.Kafka
	case "nats":
		return data.NATS
	case "redis":
		return data.Redis
	case "temporal":
		return data.Temporal
	case "connect":
		return data.Connect
	default:
		return false
	}
}

// Render generates the project files in the given output directory.
func (g *Generator) Render(outputDir string, data Data) error {
	// Copy validate.proto for ConnectRPC projects.
	if data.Connect {
		validateDir := filepath.Join(outputDir, "proto", "buf", "validate")
		if err := os.MkdirAll(validateDir, 0o755); err != nil {
			return fmt.Errorf("creating validate proto directory: %w", err)
		}
		if err := os.WriteFile(filepath.Join(validateDir, "validate.proto"), validateProto, 0o644); err != nil {
			return fmt.Errorf("writing validate.proto: %w", err)
		}
	}

	return fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		// Only process .tmpl files.
		if !strings.HasSuffix(path, ".tmpl") {
			return nil
		}

		// path is like "templates/<component>/rest/of/path.tmpl"
		// Strip the "templates/" prefix.
		rel := strings.TrimPrefix(path, "templates/")

		// Determine the component (first path segment).
		component := rel
		if idx := strings.IndexByte(rel, '/'); idx >= 0 {
			component = rel[:idx]
		}

		// Skip component-specific templates if the flag is false.
		if component != "base" {
			if !componentEnabled(component, data) {
				return nil
			}
		}

		// Strip the component prefix from the relative path.
		var relPath string
		if idx := strings.IndexByte(rel, '/'); idx >= 0 {
			relPath = rel[idx+1:]
		} else {
			relPath = rel
		}

		// Remove the .tmpl suffix and substitute __name__ placeholder in paths.
		outRel := strings.TrimSuffix(relPath, ".tmpl")
		outRel = strings.ReplaceAll(outRel, "__name__", data.Name)
		outPath := filepath.Join(outputDir, outRel)

		// Create parent directories.
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", outPath, err)
		}

		// Read and parse the template.
		content, err := templateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading template %s: %w", path, err)
		}

		tmpl, err := template.New(filepath.Base(path)).Funcs(g.funcs).Parse(string(content))
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", path, err)
		}

		// Execute the template.
		var buf strings.Builder
		if err := tmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("executing template %s: %w", path, err)
		}

		// Write the output file.
		if err := os.WriteFile(outPath, []byte(buf.String()), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", outPath, err)
		}

		return nil
	})
}
