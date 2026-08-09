package main

import (
	"context"
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type planStepInput struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

type planUpdateInput struct {
	Goal  string          `json:"goal"`
	Level string          `json:"level"`
	Steps []planStepInput `json:"steps"`
}

var planUpdateInputSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "goal": {"type": "string"},
    "level": {"type": "string", "enum": ["low", "medium", "high", "xhigh", "max"]},
    "steps": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "title": {"type": "string"},
          "status": {"type": "string", "enum": ["pending", "in_progress", "done", "skipped"]}
        },
        "required": ["title", "status"]
      }
    }
  },
  "required": ["goal", "level", "steps"]
}`)

var planUpdateOutputSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "goal": {"type": "string"},
    "level": {"type": "string"},
    "steps": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "title": {"type": "string"},
          "status": {"type": "string"}
        },
        "required": ["title", "status"]
      }
    }
  },
  "required": ["goal", "level", "steps"]
}`)

func registerPlanTool(toolSet *toolcontract.ToolSet) {
	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[planUpdateInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/plan_update",
			Name:            toolcontract.PlanUpdateToolName,
			SideEffectClass: toolcontract.ToolSideEffectNone,
			OutputSchema:    planUpdateOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: planUpdateOutputSchema},
			Description:     "Record the goal and the steps this task takes, and size it. Set level from the steps you just listed: low for a handful of commands, medium for a dozen or so, high when the work runs to several dozen, xhigh or max beyond that. The level sets how much room the task gets, so size it from the work in front of you rather than from how the request sounded.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     planUpdateInputSchema,
		},
		Result: toolcontract.IdentityToolResult,
		Handler: func(_ context.Context, input planUpdateInput) (toolcontract.ToolResult, error) {
			document, errorValue := json.Marshal(input)
			if errorValue != nil {
				return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput,
					toolcontract.PlanUpdateToolName, errorValue.Error()), nil
			}
			return toolcontract.ToolSuccessData(string(document), json.RawMessage(document)), nil
		},
	})
}
