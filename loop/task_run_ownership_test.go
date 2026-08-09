package loop

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func quickReplyKernel(t *testing.T) (*AgentKernel, *taskstate.TaskRunService) {
	t.Helper()
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteConsume,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelXLow,
		ResponseLanguage: "ko",
		Reason:           "lightweight acknowledgement",
	}})
	return agentKernel, taskRunService
}

func TestAHostSuppliedTaskRunIsUsedRatherThanASecondOne(t *testing.T) {
	agentKernel, taskRunService := quickReplyKernel(t)
	hostTaskRun := taskRunService.CreateTaskRunWithOrigin("person-kernel-test", taskstate.TaskRunOrigin{ConversationID: "conversation-kernel-test"}, "고마워!")

	request := kernelTestRequest("고마워!")
	request.ExistingTaskRunID = hostTaskRun.TaskRunID
	result, errorValue := agentKernel.RunAgentRequest(context.Background(), routedRequest(t, context.Background(), agentKernel, request))
	if errorValue != nil {
		t.Fatalf("expected the consumed request to complete: %v", errorValue)
	}

	if result.TaskRun.TaskRunID != hostTaskRun.TaskRunID {
		t.Fatalf("a host that already opened a task run must not end up with a second one it never hears about, got %q against %q", result.TaskRun.TaskRunID, hostTaskRun.TaskRunID)
	}
	if taskRunCount := len(taskRunService.ListTaskRun()); taskRunCount != 1 {
		t.Fatalf("expected one task run for one turn, got %d", taskRunCount)
	}
}
