package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var version = "dev"

//go:embed all:assets
var embeddedAssets embed.FS

type projectConfig struct {
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

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "--help" {
		printHelp()
		return
	}
	if os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Println(version)
		return
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = initProject(".")
	case "doctor":
		err = doctor(".")
	case "start":
		err = start(".", strings.Join(os.Args[2:], " "))
	case "status":
		err = status(".")
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`ox-agent - repository-local AI-SDLC

Usage:
  ox-agent version
  ox-agent init
  ox-agent doctor
  ox-agent start <title>
  ox-agent status`)
}

func initProject(root string) error {
	base := filepath.Join(root, ".ox-agent")
	if _, err := os.Stat(base); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite it", base)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dirs := []string{
		"skills", "work/active", "work/archived", "context/standards",
		"lifecycle", "workflows", "pipelines", "state", "integrations",
		"runtime/packets", "runtime/runs", "runtime/cache", "runtime/locks",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := materializeAssets(base); err != nil {
		return err
	}
	project := filepath.Base(root)
	config := projectConfig{SchemaVersion: 1}
	config.Framework.Name = "ox-agent"
	config.Framework.Distribution = "standalone"
	config.Framework.Binary = "ox-agent"
	config.Framework.RequiredVersion = ">=0.2.0 <0.3.0"
	config.Project.Name = project
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encoding framework configuration: %w", err)
	}
	if err := os.WriteFile(filepath.Join(base, "framework.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("writing framework configuration: %w", err)
	}
	if err := os.WriteFile(filepath.Join(base, "state", "events.ndjson"), nil, 0o644); err != nil {
		return fmt.Errorf("creating event journal: %w", err)
	}
	fmt.Printf("Initialized %s\n", base)
	return nil
}

func materializeAssets(base string) error {
	return fs.WalkDir(embeddedAssets, "assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative := strings.TrimPrefix(path, "assets/")
		if relative == "assets" || relative == "" {
			return nil
		}
		target := filepath.Join(base, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(embeddedAssets, path)
		if err != nil {
			return fmt.Errorf("reading embedded asset %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func doctor(root string) error {
	base := filepath.Join(root, ".ox-agent")
	configData, err := os.ReadFile(filepath.Join(base, "framework.yaml"))
	if err != nil {
		return fmt.Errorf("reading .ox-agent/framework.yaml: %w", err)
	}
	var config projectConfig
	if err := yaml.Unmarshal(configData, &config); err != nil {
		return fmt.Errorf("parsing .ox-agent/framework.yaml: %w", err)
	}
	if config.SchemaVersion != 1 || config.Framework.Name != "ox-agent" {
		return errors.New("unsupported or invalid Ox-Agent framework configuration")
	}
	for _, file := range []string{"state/events.ndjson", "work/active", "skills", "workflows", "pipelines"} {
		if _, err := os.Stat(filepath.Join(base, file)); err != nil {
			return fmt.Errorf("missing .ox-agent/%s: %w", file, err)
		}
	}
	fmt.Printf("Ox-Agent doctor: healthy (binary %s)\n", version)
	return nil
}

func start(root, title string) error {
	if strings.TrimSpace(title) == "" {
		return errors.New("start requires a work-item title")
	}
	base := filepath.Join(root, ".ox-agent", "work", "active")
	entries, err := os.ReadDir(base)
	if err != nil {
		return fmt.Errorf("reading active work items: %w", err)
	}
	id := fmt.Sprintf("WI-%04d", len(entries)+1)
	slug := strings.Trim(strings.ToLower(strings.NewReplacer(" ", "-", "/", "-", "_", "-").Replace(title)), "-")
	if slug == "" {
		slug = "work-item"
	}
	dir := filepath.Join(base, id+"-"+slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating work item: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	index := map[string]any{"schema_version": 1, "id": id, "title": title, "state": "intake", "profile": "normal", "current_stage": "intake", "created_at": now}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding work item: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing work item: %w", err)
	}
	fmt.Printf("%s: %s\n", id, title)
	return nil
}

func status(root string) error {
	entries, err := os.ReadDir(filepath.Join(root, ".ox-agent", "work", "active"))
	if err != nil {
		return fmt.Errorf("reading active work items: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			fmt.Printf("active: %s\n", entry.Name())
		}
	}
	return nil
}
