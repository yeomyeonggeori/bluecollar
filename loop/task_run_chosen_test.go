package loop

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type revisedTurn struct {
	hostTaskRunID     string
	chosenTaskRunIDs  []string
	chosenWhenToolRan int
	result            AgentTurnResult
}

func revisingTurn(t *testing.T, isHostRunOpenedForThisTurn bool) revisedTurn {
	t.Helper()
	revised := revisedTurn{}
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseIntakeLanguageModelProvider(intakeDecisionLanguageModel{decision: TurnDecision{
		Route:            TurnRouteReviseTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskLevel:        TaskLevelLow,
		InitialToolNames: []string{"task_add"},
		ResponseLanguage: "ko",
	}})
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"task_add","toolInput":{"title":"새 업무"}}`,
		finishMessageCiting("새 업무를 추가했습니다.", "obs-001"),
	}})
	taskAddDefinition := testToolDescriptor("task_add")
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{taskAddDefinition})
	registerTestTool(toolSet, taskAddDefinition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		revised.chosenWhenToolRan = len(revised.chosenTaskRunIDs)
		return testToolSuccess(`{"taskID":"new-task"}`), nil
	})
	hostTaskRun := taskRunService.CreateTaskRunWithOrigin("person-1", taskstate.TaskRunOrigin{ConversationID: "conversation-1"}, "다시 해봐")
	request := kernelTestRequest("다시 해봐")
	request.ToolSet = toolSet
	request.ExistingTaskRunID = hostTaskRun.TaskRunID
	request.IsTaskRunOpenedForThisTurn = isHostRunOpenedForThisTurn
	request.TaskRunChosen = func(taskRunID string) { revised.chosenTaskRunIDs = append(revised.chosenTaskRunIDs, taskRunID) }
	revised.hostTaskRunID = hostTaskRun.TaskRunID

	var errorValue error
	revised.result, errorValue = runRoutedRequest(t, context.Background(), agentKernel, request)
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return revised
}

func TestTheKernelSaysWhichRunItMovedTheTurnOnto(t *testing.T) {
	revised := revisingTurn(t, false)

	if revised.result.TaskRun.TaskRunID == revised.hostTaskRunID {
		t.Fatal("expected the revision to move the turn off the host's run")
	}
	if len(revised.chosenTaskRunIDs) != 1 || revised.chosenTaskRunIDs[0] != revised.result.TaskRun.TaskRunID {
		t.Fatalf("the kernel named %v, expected exactly the run the turn ran on, %q", revised.chosenTaskRunIDs, revised.result.TaskRun.TaskRunID)
	}
}

func TestTheKernelSaysWhichRunTheTurnStayedOn(t *testing.T) {
	revised := revisingTurn(t, true)

	if len(revised.chosenTaskRunIDs) != 1 || revised.chosenTaskRunIDs[0] != revised.hostTaskRunID || revised.result.TaskRun.TaskRunID != revised.hostTaskRunID {
		t.Fatalf("the kernel named %v and the turn ran on %q, expected only the host's run %q", revised.chosenTaskRunIDs, revised.result.TaskRun.TaskRunID, revised.hostTaskRunID)
	}
}

func TestTheKernelNamesTheRunBeforeTheTurnsFirstToolCall(t *testing.T) {
	revised := revisingTurn(t, false)

	if revised.chosenWhenToolRan != 1 {
		t.Fatalf("the tool ran after %d runs were named, expected the run to be named first", revised.chosenWhenToolRan)
	}
}
