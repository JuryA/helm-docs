package cueutil

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/encoding/jsonschema"
	cueyaml "cuelang.org/go/encoding/yaml"
	yamlv3 "gopkg.in/yaml.v3"
)

// GenerateJSONSchemaFromYAML is a proof-of-concept placeholder that converts a
// YAML node to a CUE instance. Full JSON Schema generation is left as a future
// improvement.
func GenerateJSONSchemaFromYAML(ctx *cue.Context, node *yamlv3.Node) ([]byte, error) {
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}
	if ctx == nil {
		ctx = cuecontext.New()
	}

	data, err := yamlv3.Marshal(node)
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
