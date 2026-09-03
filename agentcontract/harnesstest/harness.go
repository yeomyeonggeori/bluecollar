package harnesstest

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

// Harness answers the agent harness port with canned decisions so host tests
// can exercise the host without the agent turn loop. It settles a real task
// run for every turn because the host reads the run the loop reports back,
// using the one the host opened when it was given one.
type Harness struct {
	taskRunService *taskstate.TaskRunService

	TurnResult           agentcontract.AgentTurnResult
	TurnStatus           agentcontract.TaskStatus
	TurnDecision         agentcontract.TurnDecision
	Reply                string
	AddressingDecision   agentcontract.AddressingDecision
	IsActiveTaskFollowUp bool

	lastTurnRequest             agentcontract.AgentTurnRequest
	runTurnCallCount            int
	classifyAddressingCallCount int
}

func New(taskRunService *taskstate.TaskRunService) *Harness {
	return &Harness{
		taskRunService: taskRunService,
		TurnStatus:     agentcontract.TaskStatusCompleted,
	}
}

func (harness *Harness) RunTurn(_ context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	harness.runTurnCallCount++
	harness.lastTurnRequest = request
	turnResult := harness.TurnResult
	settledTaskRun, errorValue := harness.settleTaskRun(request, harness.TurnStatus, turnResult.FinishMessage)
	if errorValue != nil {
		return turnResult, errorValue
	}
	turnResult.TaskRun = settledTaskRun
	return turnResult, nil
}

func (harness *Harness) RunAgentRequest(context.Context, agentcontract.AgentRequest) (agentcontract.AgentTurnResult, error) {
	return agentcontract.AgentTurnResult{}, nil
}

func (harness *Harness) CompleteLaunchFailure(_ context.Context, request agentcontract.AgentTurnRequest, phase string, stepName string, errorValue error) agentcontract.AgentTurnResult {
	failedTaskRun, transitionError := harness.settleTaskRun(request, agentcontract.TaskStatusFailed, errorValue.Error())
	if transitionError != nil {
		return agentcontract.AgentTurnResult{}
	}
	return agentcontract.AgentTurnResult{
		TaskRun: failedTaskRun,
		FailureNotice: agentcontract.FailureNotice{
			Message:           errorValue.Error(),
			Source:            "raw_error",
			DiagnosticEventID: failedTaskRun.TaskRunID + ":" + phase + ":" + stepName,
			IsSendable:        true,
		},
	}
}

func (harness *Harness) Plan(context.Context, agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	return harness.TurnDecision, nil
}

func (harness *Harness) PlanObserved(context.Context, agentcontract.AgentRequest, *agentcontract.TurnRouterCallLedger) (agentcontract.TurnDecision, error) {
	return harness.TurnDecision, nil
}

func (harness *Harness) GenerateReply(context.Context, string) (string, error) {
	return harness.Reply, nil
}

func (harness *Harness) GenerateReplyWithContext(context.Context, string, agentcontract.VisibleContext, []agentcontract.MemoryFact) (string, error) {
	return harness.Reply, nil
}

func (harness *Harness) ClassifyAddressing(context.Context, agentcontract.AddressingClassificationRequest) (agentcontract.AddressingDecision, error) {
	harness.classifyAddressingCallCount++
	return harness.AddressingDecision, nil
}

func (harness *Harness) ClassifyActiveTaskFollowUp(context.Context, agentcontract.ActiveTaskFollowUpClassificationRequest) (bool, error) {
	return harness.IsActiveTaskFollowUp, nil
}

func (harness *Harness) LastTurnRequest() agentcontract.AgentTurnRequest {
	return harness.lastTurnRequest
}

func (harness *Harness) RunTurnCallCount() int {
	return harness.runTurnCallCount
}

func (harness *Harness) ClassifyAddressingCallCount() int {
	return harness.classifyAddressingCallCount
}

func (harness *Harness) settleTaskRun(request agentcontract.AgentTurnRequest, status agentcontract.TaskStatus, message string) (agentcontract.TaskRun, error) {
	taskRun := harness.taskRunForRequest(request)
	runningTaskRun, errorValue := harness.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, request.ProfileName)
	if errorValue != nil {
		return agentcontract.TaskRun{}, errorValue
	}
	switch status {
	case agentcontract.TaskStatusRunning:
		return runningTaskRun, nil
	case agentcontract.TaskStatusCompleted:
		return harness.taskRunService.CompleteTaskRun(taskRun.TaskRunID, message)
	case agentcontract.TaskStatusFailed:
		return harness.taskRunService.FailTaskRun(taskRun.TaskRunID, message)
	default:
		return harness.taskRunService.PauseTaskRun(taskRun.TaskRunID, status, message)
	}
}

func (harness *Harness) taskRunForRequest(request agentcontract.AgentTurnRequest) agentcontract.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := harness.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	return harness.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}
