package loop

import (
	"encoding/json"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type ProbedAgentAction struct {
	Action    string
	ToolName  string
	ToolInput json.RawMessage
}

func BuildActionRequestForTurn(request AgentTurnRequest) model.StructuredResponseRequest {
	return BuildAgentActionRequest(agentTaskState{Request: request})
}

func ProbeAgentActionResponse(response model.StructuredResponse) (ProbedAgentAction, error) {
	action, errorValue := ParseAgentActionResponse(response)
	if errorValue != nil {
		return ProbedAgentAction{}, errorValue
	}
	return ProbedAgentAction{Action: action.Action, ToolName: action.ToolName, ToolInput: action.ToolInput}, nil
}
