package cueutil

import (
	"bytes"
	"fmt"

	"cuelang.org/go/cue/cuecontext"
	cueyaml "cuelang.org/go/encoding/yaml"
	yamlv3 "gopkg.in/yaml.v3"
)

// GenerateJSONSchemaFromYAML is a proof-of-concept placeholder that converts a
// YAML node to a CUE instance. Full JSON Schema generation is left as a future
// improvement.
func GenerateJSONSchemaFromYAML(node *yamlv3.Node) ([]byte, error) {
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}

	var buf bytes.Buffer
	enc := yamlv3.NewEncoder(&buf)
	if err := enc.Encode(node); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	ctx := cuecontext.New()
	cueValue, err := cueyaml.Extract("values.yaml", buf.Bytes())
	if err != nil {
		return nil, nil, err
	}

	// TODO: use encoding/jsonschema to generate JSON schema from the CUE value.
	// For now, return the original YAML and the parsed CUE value as a demonstration of CUE parsing.
	return buf.Bytes(), cueValue, nil
}
