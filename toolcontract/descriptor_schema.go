package toolcontract

import (
	"encoding/json"
	"reflect"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// json.RawMessage is a []byte, which inference reads as an array of numbers.
// On the wire it is whatever JSON the field carries, so it is described as any.
var rawJSONTypeSchemas = map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[json.RawMessage](): {},
}

var descriptorSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	return jsonschema.For[ToolDescriptor](&jsonschema.ForOptions{TypeSchemas: rawJSONTypeSchemas})
})

// ToolDescriptorSchema describes the descriptor a provider registers, generated
// from the struct rather than written beside it, so a reader in another language
// derives the shape instead of retyping it.
func ToolDescriptorSchema() (*jsonschema.Schema, error) {
	schema, errorValue := descriptorSchema()
	if errorValue != nil {
		return nil, errorValue
	}
	cloned := *schema
	return &cloned, nil
}

// A reader in another language cannot import this package, so the schema is also
// a tracked artifact it reads by path.
const DescriptorSchemaPath = "toolcontract/generated/tool-descriptor.schema.json"

func ToolDescriptorSchemaDocument() ([]byte, error) {
	schema, errorValue := ToolDescriptorSchema()
	if errorValue != nil {
		return nil, errorValue
	}
	return json.MarshalIndent(schema, "", "  ")
}
