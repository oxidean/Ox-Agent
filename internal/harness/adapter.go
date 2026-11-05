// Package harness dispatches compiled skill packets to agent harnesses and
// collects their structured outputs through the project's integrations.
package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Integration mirrors one .ox-agent/integrations/<name>.yaml file.
type Integration struct {
	SchemaVersion  int    `yaml:"schema_version"`
	Name           string `yaml:"name"`
	Adapter        string `yaml:"adapter"`
	Command        string `yaml:"command"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	Default        bool   `yaml:"default"`
	Description    string `yaml:"description"`
}

// integrationsDir returns the integrations directory under root.
func integrationsDir(root string) string {
	return filepath.Join(root, ".ox-agent", "integrations")
}

// List loads every integration configured in the project.
func List(root string) ([]*Integration, error) {
	entries, err := os.ReadDir(integrationsDir(root))
	if err != nil {
		return nil, fmt.Errorf("reading integrations: %w", err)
	}
	var out []*Integration
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		integration, err := load(filepath.Join(integrationsDir(root), entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, integration)
	}
	return out, nil
}

// Resolve loads the named integration, or the project default when name is
// empty. With no marked default, the manual adapter applies.
func Resolve(root, name string) (*Integration, error) {
	all, err := List(root)
	if err != nil {
		return nil, err
	}
	if name != "" {
		for _, integration := range all {
			if integration.Name == name {
				return integration, nil
			}
		}
		return nil, fmt.Errorf("no integration named %q", name)
	}
	for _, integration := range all {
		if integration.Default {
			return integration, nil
		}
	}
	for _, integration := range all {
		if integration.Adapter == "manual" {
			return integration, nil
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no integrations configured; run init --repair")
	}
	return all[0], nil
}

// load parses one integration file.
func load(path string) (*Integration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var integration Integration
	if err := yaml.Unmarshal(data, &integration); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if integration.Name == "" {
		integration.Name = integrationName(path)
	}
	return &integration, nil
}

// integrationName derives a name from the file basename.
func integrationName(path string) string {
	base := filepath.Base(path)
	return base[:len(base)-len(filepath.Ext(base))]
}
