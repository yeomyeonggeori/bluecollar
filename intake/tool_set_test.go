package intake

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func newTestToolSet(allowedToolNames []string) *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet(allowedToolNames)
	toolSet.AllowTestReplacement()
	for _, toolName := range allowedToolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		toolSet.RegisterBoundTool(toolcontract.BoundTool{
			Definition:   testToolDescriptor(trimmedToolName),
			Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable},
			Handler: func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
				return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.NotFound, "test_tool", "tool is not registered"), nil
			},
		})
	}
	return toolSet
}

func testToolDescriptor(toolName string) toolcontract.ToolDefinition {
	return toolcontract.ToolDefinition{
		ID:                "test:" + toolName,
		Name:              toolName,
		Visibility:        toolcontract.ToolVisibilityModel,
		InputSchema:       json.RawMessage(`{"type":"object","properties":{}}`),
		InputIntentSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema:      json.RawMessage(`{"type":"object","properties":{}}`),
		ResultContract:    testToolResultContract(),
		SideEffectClass:   testToolSideEffectClass(toolName),
	}
}

func testToolResultContract() *toolcontract.ToolResultContract {
	return &toolcontract.ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func testToolSideEffectClass(toolName string) string {
	for _, suffix := range []string{"_list", "_read", "_search", "_status", "_history", "_preview", "_snapshot"} {
		if strings.HasSuffix(toolName, suffix) {
			return toolcontract.ToolSideEffectRead
		}
	}
	for _, suffix := range []string{"_calculate", "_compare", "_classify"} {
		if strings.HasSuffix(toolName, suffix) {
			return toolcontract.ToolSideEffectComputation
		}
	}
	return toolcontract.ToolSideEffectStateChange
}
