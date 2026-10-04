package intake

import (
	"encoding/json"
	"testing"
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

func TestEveryPropertyIsRequiredSomewhereInTheTurnWordsSchema(t *testing.T) {
	for _, schemaDocument := range []string{turnWordsSchema(), clarificationTurnWordsSchema()} {
		var schema any
		if errorValue := json.Unmarshal([]byte(schemaDocument), &schema); errorValue != nil {
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
}

func TestClarificationSchemaRequiresADispositionAndUsesAnEmptyStringForNoQuestion(t *testing.T) {
	var schema map[string]any
	if errorValue := json.Unmarshal([]byte(clarificationTurnWordsSchema()), &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	properties := schema["properties"].(map[string]any)
	question := properties["clarificationQuestion"].(map[string]any)
	if question["type"] != "string" {
		t.Fatalf("expected clarificationQuestion to use an empty string when no question is needed, got %+v", question)
	}
	if _, hasAnyOf := question["anyOf"]; hasAnyOf {
		t.Fatalf("expected the clarification schema not to allow null questions, got %+v", question)
	}
	disposition := properties["clarificationDisposition"].(map[string]any)
	if len(disposition["enum"].([]any)) != 2 {
		t.Fatalf("expected a finite clarification disposition, got %+v", disposition)
	}
	required := map[string]bool{}
	for _, property := range schema["required"].([]any) {
		required[property.(string)] = true
	}
	if !required["clarificationDisposition"] {
		t.Fatal("expected clarificationDisposition to be required")
	}
}
