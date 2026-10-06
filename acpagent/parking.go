package acpagent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type runParking struct {
	taskRuns *taskstate.TaskRunService
}

func (parking *runParking) parkOnHostPause(ctx context.Context, toolName string, toolResult toolcontract.ToolResult) {
	status, reason, isPause := hostPauseOf(toolName, toolResult)
	taskRunID := strings.TrimSpace(toolcontract.TaskRunIDFromContext(ctx))
	if parking.taskRuns == nil || !isPause || taskRunID == "" {
		return
	}
	parking.taskRuns.PauseTaskRun(taskRunID, status, reason)
}

func hostPauseOf(toolName string, toolResult toolcontract.ToolResult) (agentcontract.TaskStatus, string, bool) {
	if toolResult.Failure != nil && toolResult.Failure.RequiresApproval {
		return agentcontract.TaskStatusWaitingApproval, toolResult.Failure.UserSafeSummary, true
	}
	if toolName == toolcontract.AskInputToolName && !toolResult.Failed() && isWaitingForAnswer(toolResult) {
		return agentcontract.TaskStatusWaitingUserInput, toolResult.ContentText(), true
	}
	return "", "", false
}

func isWaitingForAnswer(toolResult toolcontract.ToolResult) bool {
	result := struct {
		Status string `json:"status"`
	}{}
	return json.Unmarshal(toolResult.Output.Data, &result) == nil && result.Status == string(agentcontract.TaskStatusWaitingUserInput)
}
