package cueutil

import (
	"errors"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/encoding/jsonschema"
	cueyaml "cuelang.org/go/encoding/yaml"
	yamlv3 "gopkg.in/yaml.v3"
)

// ErrIncompleteSchema indicates the values used to generate the schema were incomplete.
var ErrIncompleteSchema = errors.New("incomplete schema")

// GenerateJSONSchemaFromYAML converts a YAML node to JSON Schema using CUE.
// It normalizes the node by decoding it into generic Go values so comments and
// other YAML metadata are discarded before CUE processing. The ctx parameter
// must be non-nil.
func GenerateJSONSchemaFromYAML(ctx *cue.Context, node *yamlv3.Node) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil cue.Context")
	}
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}

	var val interface{}
	if err := node.Decode(&val); err != nil {
		return nil, err
	}
	data, err := yamlv3.Marshal(val)
	if err != nil {
		return nil, err
	}

	astFile, err := cueyaml.Extract("values.yaml", data)
	if err != nil {
		return nil, err
	}

	v := ctx.BuildFile(astFile)
	if err := v.Err(); err != nil {
		return nil, err
	}

	schemaAST, err := jsonschema.Extract(v, &jsonschema.Config{})
	if err != nil {
		return nil, err
	}

	schemaVal := ctx.BuildFile(schemaAST)
	if err := schemaVal.Err(); err != nil {
		return nil, err
	}

	if err := schemaVal.Validate(); err != nil {
		if strings.Contains(err.Error(), "incomplete") {
			return nil, fmt.Errorf("%w: %v", ErrIncompleteSchema, err)
		}
		return nil, err
	}

	out, err := schemaVal.MarshalJSON()
	if err != nil {
		if strings.Contains(err.Error(), "incomplete") {
			return nil, fmt.Errorf("%w: %v", ErrIncompleteSchema, err)
		}
		return nil, err
	}
	return out, nil
}
