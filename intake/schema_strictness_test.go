package intake

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func objectSchemasIn(node any, found *[]map[string]any) {
	switch typed := node.(type) {
	case map[string]any:
		if properties, hasProperties := typed["properties"].(map[string]any); hasProperties && len(properties) > 0 {
			*found = append(*found, typed)
		}
		for _, child := range typed {
			objectSchemasIn(child, found)
		}
	case []any:
		for _, child := range typed {
			objectSchemasIn(child, found)
		}
	}
}

func TestEveryPropertyIsRequiredSomewhereInTheRouterSchema(t *testing.T) {
	var schema any
	if errorValue := json.Unmarshal([]byte(turnRouterSchema(agentcontract.AgentRequest{})), &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	objectSchemas := []map[string]any{}
	objectSchemasIn(schema, &objectSchemas)

	for _, objectSchema := range objectSchemas {
		properties := objectSchema["properties"].(map[string]any)
		required := map[string]bool{}
		for _, name := range objectSchema["required"].([]any) {
			required[name.(string)] = true
		}
		for name := range properties {
			if !required[name] {
				t.Fatalf("%q is described and not required, and a strict response_format is refused for exactly that — the refusal reaches the runtime as an empty response, one layer from the schema that caused it", name)
			}
		}
	}
}
