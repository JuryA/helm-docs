package cueutil

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/encoding/jsonschema"
	cueyaml "cuelang.org/go/encoding/yaml"
	yamlv3 "gopkg.in/yaml.v3"
)

// GenerateJSONSchemaFromYAML converts a YAML node to JSON Schema using CUE.
// It normalizes the node by decoding it into generic Go values so comments and
// other YAML metadata are discarded before CUE processing.
func GenerateJSONSchemaFromYAML(ctx *cue.Context, node *yamlv3.Node) ([]byte, error) {
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}
	if ctx == nil {
		ctx = cuecontext.New()
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

	out, err := schemaVal.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return out, nil
}
