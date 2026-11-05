// Package contract validates project documents against the JSON schemas
// embedded in the binary, and evaluates binary-version compatibility.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/oxidean/Ox-Agent/internal/assets"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

var (
	schemasMu sync.Mutex
	schemas   = map[string]*jsonschema.Schema{}
)

// Schema compiles (once) the embedded schema named name, e.g. "config".
func Schema(name string) (*jsonschema.Schema, error) {
	schemasMu.Lock()
	defer schemasMu.Unlock()
	if schema, ok := schemas[name]; ok {
		return schema, nil
	}
	data, err := assets.ReadFile("schemas/" + name + ".schema.json")
	if err != nil {
		return nil, fmt.Errorf("reading schema %s: %w", name, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parsing schema %s: %w", name, err)
	}
	compiler := jsonschema.NewCompiler()
	url := "https://ox-agent.dev/schemas/" + name + ".schema.json"
	if err := compiler.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("registering schema %s: %w", name, err)
	}
	schema, err := compiler.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compiling schema %s: %w", name, err)
	}
	schemas[name] = schema
	return schema, nil
}

// ValidateYAML validates a YAML document against the named embedded schema.
func ValidateYAML(name string, data []byte) error {
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("parsing yaml: %w", err)
	}
	return ValidateValue(name, value)
}

// ValidateJSON validates a JSON document against the named embedded schema.
func ValidateJSON(name string, data []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parsing json: %w", err)
	}
	schema, err := Schema(name)
	if err != nil {
		return err
	}
	return schema.Validate(doc)
}

// ValidateValue validates an already-decoded document, normalizing values
// through JSON so yaml-decoded types take their JSON shapes.
func ValidateValue(name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("normalizing document: %w", err)
	}
	return ValidateJSON(name, raw)
}
