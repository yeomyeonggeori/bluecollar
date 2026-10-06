package acpagent

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type runParking struct {
	taskRuns *taskstate.TaskRunService
}

func (parking *runParking) parkOnHeldCall(ctx context.Context, toolResult toolcontract.ToolResult) {
	if parking.taskRuns == nil || toolResult.Failure == nil || !toolResult.Failure.RequiresApproval {
		return
	}
	taskRunID := strings.TrimSpace(toolcontract.TaskRunIDFromContext(ctx))
	if taskRunID == "" {
		return
	}
	parking.taskRuns.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, toolResult.Failure.UserSafeSummary)
}
