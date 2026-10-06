package loop

import (
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestToolSideEffectClassUsesOnlyDescriptorMetadata(t *testing.T) {
	tests := []struct {
		toolName           string
		sideEffectClass    string
		expectedSideEffect string
		requiresCompletion bool
	}{
		{toolName: "task_add", sideEffectClass: toolcontract.ToolSideEffectStateChange, expectedSideEffect: toolcontract.ToolSideEffectStateChange, requiresCompletion: true},
		{toolName: "task_list", sideEffectClass: toolcontract.ToolSideEffectRead, expectedSideEffect: toolcontract.ToolSideEffectRead, requiresCompletion: false},
		{toolName: "message_send", sideEffectClass: toolcontract.ToolSideEffectExternalWrite, expectedSideEffect: toolcontract.ToolSideEffectExternalWrite, requiresCompletion: true},
		{toolName: "llm_structured", sideEffectClass: toolcontract.ToolSideEffectComputation, expectedSideEffect: toolcontract.ToolSideEffectComputation, requiresCompletion: false},
		{toolName: "looks_like_write", expectedSideEffect: "", requiresCompletion: false},
	}

	for _, test := range tests {
		toolDefinition := toolcontract.ToolDefinition{Name: test.toolName, SideEffectClass: test.sideEffectClass}
		if actualSideEffect := toolcontract.ToolDefinitionSideEffectClass(toolDefinition); actualSideEffect != test.expectedSideEffect {
			t.Fatalf("expected %s side effect for %s, got %s", test.expectedSideEffect, test.toolName, actualSideEffect)
		}
		if actualRequirement := ToolDefinitionRequiresSideEffectEvidence(toolDefinition); actualRequirement != test.requiresCompletion {
			t.Fatalf("expected requiresCompletion=%v for %s, got %v", test.requiresCompletion, test.toolName, actualRequirement)
		}
	}
}
