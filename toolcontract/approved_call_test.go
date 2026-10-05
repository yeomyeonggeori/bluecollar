package toolcontract

import (
	"context"
	"encoding/json"
	"testing"
)

type approvingToolCallGate struct {
	holdID string
}

func (gate approvingToolCallGate) ReviewToolCall(context.Context, ToolInvocation, ToolDefinition) (ToolCallReview, error) {
	return ToolCallReview{MayProceed: true, HoldID: gate.holdID}, nil
}

func toolSetReadingTheApproval(t *testing.T, gate ToolCallGate) (*ToolSet, *[]string) {
	t.Helper()
	readHoldIDs := []string{}
	toolSet := NewToolSet([]string{"message_send"})
	errorValue := toolSet.RegisterTool(ToolDefinition{
		ID:              "test:message_send",
		Name:            "message_send",
		Description:     "send a message",
		Visibility:      ToolVisibilityModel,
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
		SideEffectClass: ToolSideEffectExternalSend,
		ResultContract:  &ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)},
	}, func(ctx context.Context, _ ToolInvocation) (ToolResult, error) {
		readHoldIDs = append(readHoldIDs, HoldIDFromContext(ctx))
		return ToolSuccessData("sent", json.RawMessage(`{}`)), nil
	})
	if errorValue != nil {
		t.Fatalf("expected the tool to register: %v", errorValue)
	}
	if gate != nil {
		toolSet.UseToolCallGate(gate)
	}
	return toolSet, &readHoldIDs
}

func TestAnApprovedCallTellsItsHandlerWhichApprovalItSpends(t *testing.T) {
	toolSet, readHoldIDs := toolSetReadingTheApproval(t, approvingToolCallGate{holdID: "held-1"})

	if _, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "message_send", Input: json.RawMessage(`{"message":"보냅니다"}`)}); errorValue != nil {
		t.Fatal(errorValue)
	}

	if len(*readHoldIDs) != 1 || (*readHoldIDs)[0] != "held-1" {
		t.Fatalf("the handler was told %v, so a backend that must know which approval it runs under was told nothing", *readHoldIDs)
	}
}

func TestACallNoGateApprovedTellsItsHandlerNothing(t *testing.T) {
	toolSet, readHoldIDs := toolSetReadingTheApproval(t, nil)

	if _, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "message_send", Input: json.RawMessage(`{"message":"보냅니다"}`)}); errorValue != nil {
		t.Fatal(errorValue)
	}

	if len(*readHoldIDs) != 1 || (*readHoldIDs)[0] != "" {
		t.Fatalf("a call nobody approved carried %v", *readHoldIDs)
	}
}
