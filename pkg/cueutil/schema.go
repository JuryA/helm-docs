package cueutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/encoding/jsonschema"
	cueyaml "cuelang.org/go/encoding/yaml"
	yamlv3 "gopkg.in/yaml.v3"

	log "github.com/sirupsen/logrus"
)

// ErrIncompleteSchema indicates the values used to generate the schema were incomplete.
var ErrIncompleteSchema = errors.New("incomplete schema")

func inferSchema(val interface{}) map[string]interface{} {
	switch v := val.(type) {
	case map[string]interface{}:
		props := make(map[string]interface{}, len(v))
		for k, elem := range v {
			props[k] = inferSchema(elem)
		}
		return map[string]interface{}{
			"type":       "object",
			"properties": props,
		}
	case []interface{}:
		schema := map[string]interface{}{
			"type": "array",
		}
		if len(v) > 0 {
			schema["items"] = inferSchema(v[0])
		}
		return schema
	case string:
		return map[string]interface{}{"type": "string"}
	case bool:
		return map[string]interface{}{"type": "boolean"}
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return map[string]interface{}{"type": "number"}
	case nil:
		return map[string]interface{}{"type": "null"}
	default:
		return map[string]interface{}{"type": "string"}
	}
}

func fallbackJSONSchema(val interface{}) ([]byte, error) {
	schema := inferSchema(val)
	return json.MarshalIndent(schema, "", "  ")
}

func logCueValue(msg string, v cue.Value) {
	if !log.IsLevelEnabled(log.DebugLevel) {
		return
	}
	syn := v.Syntax(cue.Concrete(false), cue.Definitions(true))
	b, err := format.Node(syn)
	if err != nil {
		log.Debugf("%s (failed to format CUE: %v)", msg, err)
		return
	}
	log.Debugf("%s:\n%s", msg, string(b))
}

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
		logCueValue("CUE value from YAML", v)
		return nil, err
	}

	schemaAST, err := jsonschema.Extract(v, &jsonschema.Config{})
	if err != nil {
		logCueValue("CUE value used for schema generation", v)
		return nil, err
	}

	schemaVal := ctx.BuildFile(schemaAST)
	if err := schemaVal.Err(); err != nil {
		logCueValue("generated schema CUE", schemaVal)
		return nil, err
	}

	if err := schemaVal.Validate(); err != nil {
		logCueValue("generated schema CUE", schemaVal)
		if strings.Contains(err.Error(), "incomplete") {
			log.Debug("falling back to naive schema inference")
			return fallbackJSONSchema(val)
		}
		return nil, err
	}

	out, err := schemaVal.MarshalJSON()
	if err != nil {
		logCueValue("generated schema CUE", schemaVal)
		if strings.Contains(err.Error(), "incomplete") {
			log.Debug("falling back to naive schema inference")
			return fallbackJSONSchema(val)
		}
		return nil, err
	}
	return out, nil
}
