package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// CompileSchema compiles a JSON Schema document. Draft 2020-12 is assumed when the
// schema doesn't say ($schema is honoured when present).
func CompileSchema(name string, schema json.RawMessage) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("%s schema is not valid JSON: %w", name, err)
	}
	c := jsonschema.NewCompiler()
	url := "mem://" + name + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("%s schema: %w", name, err)
	}
	s, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("%s schema: %w", name, err)
	}
	return s, nil
}

// Validate checks value against schema and returns a short, readable error.
func Validate(name string, schema json.RawMessage, value json.RawMessage) error {
	s, err := CompileSchema(name, schema)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(value))
	if err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", name, err)
	}
	if err := s.Validate(inst); err != nil {
		msg := strings.ReplaceAll(err.Error(), "mem://"+name+".json", name)
		return fmt.Errorf("%s does not match its schema: %s", name, msg)
	}
	return nil
}

// SchemaType returns the top-level "type" of a schema, or "" if absent.
func SchemaType(schema json.RawMessage) string {
	var s struct {
		Type any `json:"type"`
	}
	json.Unmarshal(schema, &s)
	if t, ok := s.Type.(string); ok {
		return t
	}
	return ""
}
