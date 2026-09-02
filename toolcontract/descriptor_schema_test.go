package toolcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func resolvedDescriptorSchema(t *testing.T) interface {
	Validate(any) error
} {
	t.Helper()
	schema, errorValue := ToolDescriptorSchema()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	resolved, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return resolved
}

func validateAsJSON(t *testing.T, value any) error {
	t.Helper()
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var instance any
	if errorValue := json.Unmarshal(document, &instance); errorValue != nil {
		t.Fatal(errorValue)
	}
	return resolvedDescriptorSchema(t).Validate(instance)
}

func TestTheDescriptorSchemaAcceptsADescriptor(t *testing.T) {
	descriptor := ToolDescriptor{
		ID:                "capabilityd/task/task_add",
		ProviderID:        "capabilityd",
		Namespace:         "task",
		Name:              "task_add",
		Description:       "Create a task.",
		PrivacyClass:      "workspace_task",
		InputSchema:       json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		ResultContract: &ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
		Visibility:       ToolVisibilityModel,
		PolicyResource:   "tool:task_add",
		SideEffectClass:  ToolSideEffectWorkspaceWrite,
		RequiresApproval: true,
		ApprovalScope:    "task",
		Completion:       ToolCompletion{Mode: ToolCompletionObservation},
		Idempotency:      ToolIdempotencySupported,
		IdempotencyScope: "operation",
		TimeoutMS:        30000,
	}

	if errorValue := validateAsJSON(t, descriptor); errorValue != nil {
		t.Fatalf("the schema refuses a descriptor this package builds: %v", errorValue)
	}
}

// A schema inferred from a []byte field describes an array of numbers, which no
// caller writing a JSON Schema into that field would satisfy.
func TestTheDescriptorSchemaDescribesRawJSONAsJSON(t *testing.T) {
	schema, errorValue := ToolDescriptorSchema()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, fieldName := range []string{"inputSchema", "inputIntentSchema", "outputSchema"} {
		property, isDefined := schema.Properties[fieldName]
		if !isDefined {
			t.Errorf("the schema names no %s", fieldName)
			continue
		}
		if property.Type == "array" || len(property.Types) > 0 {
			t.Errorf("%s is described as %v, so a JSON Schema in that field is refused", fieldName, append(property.Types, property.Type))
		}
	}
}

func TestTheDescriptorSchemaRefusesADocumentMissingTheRequiredFields(t *testing.T) {
	if errorValue := validateAsJSON(t, map[string]any{"description": "Create a task."}); errorValue == nil {
		t.Error("the schema accepts a document carrying no name")
	}
	if errorValue := validateAsJSON(t, map[string]any{"name": "task_add"}); errorValue == nil {
		t.Error("the schema accepts a document carrying no description")
	}
	if errorValue := validateAsJSON(t, map[string]any{"name": "task_add", "description": "Create a task.", "unknown": true}); errorValue == nil {
		t.Error("the schema accepts a field the descriptor does not carry")
	}
}

func TestTheTrackedSchemaIsWhatTheDescriptorGenerates(t *testing.T) {
	generated, errorValue := ToolDescriptorSchemaDocument()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	tracked, errorValue := os.ReadFile(filepath.Join("..", DescriptorSchemaPath))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if string(tracked) != string(append(generated, '\n')) {
		t.Fatalf("%s is stale: go run %s", DescriptorSchemaPath, "toolcontract/generate_descriptor_schema.go")
	}
}
