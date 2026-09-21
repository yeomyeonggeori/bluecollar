package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestATurnThatEndsWithNoNoticeLeavesNoRunningTaskRun(t *testing.T) {
	services := newTurnRunnerTestServices(deadlineBlockingLanguageModel{}, TurnOptions{MaxElapsedSecond: 30})
	runContext, cancelRun := context.WithCancel(context.Background())
	cancelRun()

	result, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "무슨 일이 있었는지 알려줘",
		ToolSet:           newTestToolSet(nil),
	})

	if errorValue != nil {
		t.Fatalf("an abandoned turn reports through the result, not an error: %v", errorValue)
	}
	storedTaskRun, isFound := services.taskRunService.FindTaskRun(result.TaskRun.TaskRunID)
	if !isFound || storedTaskRun.Status == agentcontract.TaskStatusRunning {
		t.Fatalf("stored task run = %+v, found = %v, want a status nothing is working on", storedTaskRun, isFound)
	}
	if strings.TrimSpace(result.UserNotice) == "" {
		t.Fatal("the requester has to be told something true about the turn that stopped")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(storedTaskRun.TaskRunID), agentcontract.TaskEventAgentTurnAbandoned, "") {
		t.Fatal("the ledger has to say why the turn stopped")
	}
}
