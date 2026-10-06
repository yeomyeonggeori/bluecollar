package acpagent

import (
	"context"
	"errors"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func promptResponseFor(ctx context.Context, turnResult agentcontract.AgentTurnResult) acp.PromptResponse {
	return acp.PromptResponse{
		StopReason: stopReasonOfTurn(ctx, turnResult.TaskRun.Status),
		Meta:       map[string]any{TurnResultMetaKey: turnResult},
	}
}

func stopReasonOfTurn(ctx context.Context, status agentcontract.TaskStatus) acp.StopReason {
	if errors.Is(ctx.Err(), context.Canceled) {
		return acp.StopReasonCancelled
	}
	return stopReasonForStatus(status)
}
