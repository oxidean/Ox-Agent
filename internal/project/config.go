// Package project loads and validates the repository-local .ox-agent
// configuration files and locates the enclosing project root.
package project

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Base returns the .ox-agent directory path under root.
func Base(root string) string {
	return filepath.Join(root, ".ox-agent")
}

// FrameworkConfig mirrors the .ox-agent/framework.yaml schema.
type FrameworkConfig struct {
	SchemaVersion int `yaml:"schema_version"`
	Framework     struct {
		Name            string `yaml:"name"`
		Distribution    string `yaml:"distribution"`
		Binary          string `yaml:"binary"`
		RequiredVersion string `yaml:"required_version"`
	} `yaml:"framework"`
	Project struct {
		Name string `yaml:"name"`
	} `yaml:"project"`
}

// Settings mirrors the .ox-agent/config.yaml project-configuration schema;
// framework identity lives in framework.yaml.
type Settings struct {
	SchemaVersion int `yaml:"schema_version"`
	Project       struct {
		Name        string `yaml:"name"`
		TestCommand string `yaml:"test_command"`
	} `yaml:"project"`
	Workflow struct {
		DefaultProfile string              `yaml:"default_profile"`
		Profiles       map[string][]string `yaml:"profiles"`
	} `yaml:"workflow"`
	Skills struct {
		Enabled     []string            `yaml:"enabled"`
		StageSkills map[string][]string `yaml:"stage_skills"`
	} `yaml:"skills"`
}

// Policy mirrors the .ox-agent/policy.yaml schema.
type Policy struct {
	SchemaVersion int `yaml:"schema_version"`
	Autonomy      struct {
		ProfileChanges string `yaml:"profile_changes"`
		AllowDowngrade bool   `yaml:"allow_downgrade"`
	} `yaml:"autonomy"`
	Approvals struct {
		ProductionDeploy  string `yaml:"production_deploy"`
		DestructiveChange string `yaml:"destructive_change"`
		PolicyChange      string `yaml:"policy_change"`
	} `yaml:"approvals"`
	Budgets struct {
		MaxParallelAgents int `yaml:"max_parallel_agents"`
		MaxChildDepth     int `yaml:"max_child_depth"`
		MaxFixCycles      int `yaml:"max_fix_cycles"`
	} `yaml:"budgets"`
}

// LoadFramework parses .ox-agent/framework.yaml under root.
func LoadFramework(root string) (*FrameworkConfig, error) {
	return loadYAML[FrameworkConfig](filepath.Join(Base(root), "framework.yaml"))
}

// LoadSettings parses .ox-agent/config.yaml under root.
func LoadSettings(root string) (*Settings, error) {
	return loadYAML[Settings](filepath.Join(Base(root), "config.yaml"))
}

// LoadPolicy parses .ox-agent/policy.yaml under root.
func LoadPolicy(root string) (*Policy, error) {
	return loadYAML[Policy](filepath.Join(Base(root), "policy.yaml"))
}

// loadYAML unmarshals the YAML file at path into a new T.
func loadYAML[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value T
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &value, nil
}

// writeYAML marshals value and writes it to path with mode 0644.
func writeYAML(path string, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
