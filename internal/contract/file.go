package contract

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var fileSchemas = map[string]*jsonschema.Schema{}

// ValidateFile validates a JSON document against the schema at schemaPath.
// Schemas are compiled once per path.
func ValidateFile(schemaPath string, data []byte) error {
	abs, err := filepath.Abs(schemaPath)
	if err != nil {
		return err
	}
	schemasMu.Lock()
	schema, ok := fileSchemas[abs]
	schemasMu.Unlock()
	if !ok {
		raw, err := os.ReadFile(schemaPath)
		if err != nil {
			return fmt.Errorf("reading schema %s: %w", schemaPath, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("parsing schema %s: %w", schemaPath, err)
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource(abs, doc); err != nil {
			return err
		}
		schema, err = compiler.Compile(abs)
		if err != nil {
			return fmt.Errorf("compiling schema %s: %w", schemaPath, err)
		}
		schemasMu.Lock()
		fileSchemas[abs] = schema
		schemasMu.Unlock()
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parsing json: %w", err)
	}
	return schema.Validate(doc)
}
