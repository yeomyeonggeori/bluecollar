package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

var findToolsInputSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "need": {"type": "string"}
  },
  "required": ["need"]
}`)

var findToolsOutputSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "selectedTools": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "name": {"type": "string"},
          "description": {"type": "string"}
        },
        "required": ["name", "description"]
      }
    }
  },
  "required": ["selectedTools"]
}`)

type findToolsInput struct {
	Need string `json:"need"`
}

func configuredToolSelector() agentcontract.ToolSelector {
	decisionModel := decisions.ConfiguredDecisionModel(os.Stderr)
	if decisionModel == nil {
		return nil
	}
	return intake.NewDecisionPlanner(decisionModel, nil, nil)
}

func registerFindToolsTool(toolSet *toolcontract.ToolSet, toolSelector agentcontract.ToolSelector) {
	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[findToolsInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/find_tools",
			Name:            toolcontract.FindToolsToolName,
			Description:     "Describe in one sentence what you need a tool to do, and get back the tools that do it with a one-line description each. They become callable on your next step.",
			WhenToUse:       "the tools in this step do not cover what the work needs.",
			WhenNotToUse:    "a tool in this step already does the job.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     findToolsInputSchema,
			OutputSchema:    findToolsOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: findToolsOutputSchema},
			SideEffectClass: toolcontract.ToolSideEffectRead,
		},
		Handler: func(toolContext context.Context, input findToolsInput) (toolcontract.ToolResult, error) {
			return findTools(toolContext, input, toolSelector, toolSet)
		},
		Result: toolcontract.IdentityToolResult,
	})
}

func findTools(toolContext context.Context, input findToolsInput, toolSelector agentcontract.ToolSelector, availableToolSet *toolcontract.ToolSet) (toolcontract.ToolResult, error) {
	if strings.TrimSpace(input.Need) == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, toolcontract.FindToolsToolName, "need must say what the tool has to do"), nil
	}
	selectedTools, errorValue := toolSelector.SelectToolNames(toolContext, agentcontract.ToolSelectionNeed{Need: input.Need, ToolSet: availableToolSet})
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, toolcontract.FindToolsToolName, "tool selection failed: "+errorValue.Error()), nil
	}
	document, marshalError := json.Marshal(agentcontract.FoundTools{SelectedTools: selectedTools})
	if marshalError != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, toolcontract.FindToolsToolName, marshalError.Error()), nil
	}
	return toolcontract.ToolSuccessData(foundToolsSummary(selectedTools), document), nil
}

func foundToolsSummary(selectedTools []agentcontract.SelectedTool) string {
	if len(selectedTools) == 0 {
		return "No tool in the catalog matches that need."
	}
	lines := make([]string, 0, len(selectedTools)+1)
	lines = append(lines, "Callable from your next step:")
	for _, selectedTool := range selectedTools {
		lines = append(lines, "- "+selectedTool.Name+describedToolSuffix(selectedTool.Description))
	}
	return strings.Join(lines, "\n")
}

func describedToolSuffix(description string) string {
	if strings.TrimSpace(description) == "" {
		return ""
	}
	return " — " + strings.TrimSpace(description)
}
