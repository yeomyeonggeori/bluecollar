package intake

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type syntheticTool struct {
	name        string
	providerID  string
	description string
}

func newProviderToolSet(syntheticTools []syntheticTool) *toolcontract.ToolSet {
	toolNames := []string{}
	for _, syntheticTool := range syntheticTools {
		toolNames = append(toolNames, syntheticTool.name)
	}
	toolSet := toolcontract.NewToolSet(toolNames)
	toolSet.AllowTestReplacement()
	for _, syntheticTool := range syntheticTools {
		definition := testToolDescriptor(syntheticTool.name)
		definition.ProviderID = syntheticTool.providerID
		definition.Description = syntheticTool.description
		toolSet.RegisterBoundTool(toolcontract.BoundTool{
			Definition:   definition,
			Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable},
			Handler: func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
				return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.NotFound, "test_tool", "tool is not registered"), nil
			},
		})
	}
	return toolSet
}

func TestALongDescriptionIsClippedForSelectionWhileTheToolKeepsIt(t *testing.T) {
	longDescription := strings.Repeat("긴 설명이다. ", 400)
	toolSet := newProviderToolSet([]syntheticTool{{name: "task_add", providerID: "internkim", description: longDescription}})

	described := decisionToolDescriptions(toolSet, []string{"task_add"})

	if described.clippedDescriptionCount != 1 {
		t.Fatalf("expected the clip to be counted, got %d", described.clippedDescriptionCount)
	}
	clippedDescription := described.tools[0].Description
	if len(clippedDescription) > selectionToolDescriptionByteLimit {
		t.Fatalf("expected the selection description to fit %d bytes, got %d", selectionToolDescriptionByteLimit, len(clippedDescription))
	}
	if !strings.HasPrefix(longDescription, clippedDescription) {
		t.Fatalf("expected the clip to cut on a rune boundary of the original, got %q", clippedDescription)
	}
	for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
		if toolDefinition.Description != longDescription {
			t.Fatalf("expected the tool the chat model sees to keep its whole description, got %d bytes", len(toolDefinition.Description))
		}
	}
}

func TestAShortDescriptionIsCarriedWhole(t *testing.T) {
	description := "Add a task to the company's task list."
	toolSet := newProviderToolSet([]syntheticTool{{name: "task_add", providerID: "internkim", description: description}})

	described := decisionToolDescriptions(toolSet, []string{"task_add"})

	if described.clippedDescriptionCount != 0 || described.tools[0].Description != description {
		t.Fatalf("expected a normal description to survive untouched, got %+v", described)
	}
}
