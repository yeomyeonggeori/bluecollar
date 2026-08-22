package toolcontract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestARejectedToolInputNamesWhatWasWrongAndWhatExists(t *testing.T) {
	toolSet := NewToolSet([]string{"file_edit"})
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"oldText":{"type":"string"},"newText":{"type":"string"}},"required":["path","oldText","newText"],"additionalProperties":false}`)
	if errorValue := registerTestTool(toolSet, ToolDefinition{Name: "file_edit", InputSchema: schema},
		func(context.Context, ToolInvocation) (ToolResult, error) { return testToolSuccess("ok"), nil }); errorValue != nil {
		t.Fatalf("register failed: %v", errorValue)
	}

	result, _ := toolSet.Invoke(context.Background(), ToolInvocation{
		ToolName: "file_edit",
		Input:    json.RawMessage(`{"path":"a.go","oldText":"x","newText":"y","oldText2":"z","requireUnique":true}`),
	})

	heard := result.ContentText()
	if !strings.Contains(heard, "oldText2") && !strings.Contains(heard, "requireUnique") {
		t.Fatalf("a model that invented a parameter cannot see the descriptor, so a rejection naming nothing leaves it guessing, and a guess costs a round trip: %q", heard)
	}
	if !strings.Contains(heard, "This tool takes: newText, oldText, path") {
		t.Fatalf("and it has to be told what does exist: %q", heard)
	}
	if strings.Contains(heard, "\n") {
		t.Fatalf("the message is one line, because it is read inside an observation: %q", heard)
	}
}
