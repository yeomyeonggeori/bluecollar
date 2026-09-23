package agentcontract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestATurnRequestSerializesWithTheToolsItMayCall(t *testing.T) {
	toolSet := toolcontract.NewToolSet([]string{"file.read"})
	if errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{Name: "file.read", Visibility: toolcontract.ToolVisibilityModel, ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)}}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{}, nil
	}); errorValue != nil {
		t.Fatal(errorValue)
	}
	request := AgentTurnRequest{
		Prompt:           "회의록 정리해줘",
		ToolSet:          toolSet,
		CheckpointSender: func(context.Context, AgentCheckpoint) error { return nil },
	}

	document, errorValue := json.Marshal(request)

	if errorValue != nil {
		t.Fatalf("expected a turn request to serialize for the ledger: %v", errorValue)
	}
	if !strings.Contains(string(document), `"ToolSet":{"toolNames":["file.read"]}`) {
		t.Fatalf("expected the tool set to name the tools the turn may call, got %s", document)
	}
}
