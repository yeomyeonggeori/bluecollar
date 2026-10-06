package acpagent

import (
	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func promptResponseFor(turnResult agentcontract.AgentTurnResult) acp.PromptResponse {
	return acp.PromptResponse{
		StopReason: stopReasonForStatus(turnResult.TaskRun.Status),
		Meta:       map[string]any{TurnResultMetaKey: turnResult},
	}
}
