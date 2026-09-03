package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestATurnStartsKnowingWhatTheHostAlreadyCarriedOut(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		modelTier: "xlow",
		contents:  []string{finishMessageDocument("이미 보냈습니다")},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 2, MaxToolCallCount: 5})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "회의록 보내줘",
		ToolSet:           newTestToolSet([]string{"message_send"}),
		CarriedOutCalls: []CarriedOutCall{{
			ToolName:  "message_send",
			ToolInput: json.RawMessage(`{"to":["alice"],"message":"회의록"}`),
			Result:    toolcontract.ToolSuccessData("sent to alice", json.RawMessage(`{"messageID":"m-1"}`)),
		}},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "tool.message_send.result", "sent to alice") {
		t.Fatalf("a call the host carried out has to reach the ledger as the loop's own observation, got %d events", len(taskEvents))
	}
	if !strings.Contains(strings.Join(promptsOf(languageModel), "\n"), "sent to alice") {
		t.Fatal("the model has to see what the host already did, or it will ask for it again")
	}
}

func promptsOf(languageModel *sequenceLanguageModel) []string {
	prompts := []string{}
	for _, request := range languageModel.requests {
		for _, message := range request.Messages {
			prompts = append(prompts, message.Content)
		}
	}
	return prompts
}

func carriedOutDeleteTurn(t *testing.T) (turnRunnerTestServices, string) {
	t.Helper()
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("삭제했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "그 일정 삭제해줘")
	services.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "tool.calendar_delete.result", `{"observationID":"obs-001","tool":"calendar_delete"}`)

	services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ExistingTaskRunID: taskRun.TaskRunID,
		ConversationID:    "conversation-1",
		Prompt:            "확인",
		WorkspaceRootPath: t.TempDir(),
		CarriedOutCalls: []CarriedOutCall{{
			ToolName:  "calendar_delete",
			ToolInput: json.RawMessage(`{"eventHint":"calendar-event-001"}`),
			Result:    testToolSuccess(`{"status":"deleted"}`),
		}},
	})
	return services, taskRun.TaskRunID
}

func TestACarriedOutCallDoesNotReuseAnObservationIDTheLedgerAlreadyHolds(t *testing.T) {
	services, taskRunID := carriedOutDeleteTurn(t)

	recordedResults := []string{}
	for _, taskEvent := range services.taskEventService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == "tool.calendar_delete.result" {
			recordedResults = append(recordedResults, taskEvent.Body)
		}
	}
	if len(recordedResults) != 2 {
		t.Fatalf("expected the seeded observation and the carried out one, got %+v", recordedResults)
	}
	if strings.Contains(recordedResults[1], `"observationID":"obs-001"`) {
		t.Fatalf("the carried out call took an observation ID the ledger already holds, body=%s", recordedResults[1])
	}
}

func TestACarriedOutCallIsRecordedAsACallAndNotOnlyAsAResult(t *testing.T) {
	services, taskRunID := carriedOutDeleteTurn(t)

	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRunID), "tool.calendar_delete.requested", `"eventHint":"calendar-event-001"`) {
		t.Fatal("a carried out call that records only its result reads as a result with no call behind it")
	}
}

func TestAResumedTurnKeepsWhatItLearnedBeforeThePause(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		finishMessageDocument("메모를 삭제했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"message_search", "message_delete"})
	searchCallCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "message_search"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		searchCallCount++
		return testToolSuccess(`{"messageIDs":["message-1"]}`), nil
	})
	deleteCallCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "message_delete", RequiresApproval: true}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		deleteCallCount++
		return testToolSuccess(`{"deletedMessageIDs":["message-1"]}`), nil
	})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "고객지원 월간회의 메모를 찾아서 삭제해줘")
	services.taskRunService.AppendTaskEvent(taskRun.TaskRunID, "tool.message_search.result", `{"observationID":"obs-001","action":"continue","tool":"message_search","output":{"content":"{\"messageIDs\":[\"message-1\"]}"}}`)

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:      "person-1",
		ExistingTaskRunID:      taskRun.TaskRunID,
		IsApprovalContinuation: true,
		ConversationID:         "conversation-1",
		Prompt:                 "승인",
		ResponseLanguage:       ResponseLanguageKorean,
		ToolSet:                toolRegistry,
		PinnedToolNames:        toolRegistry.ListToolNames(),
		WorkspaceRootPath:      t.TempDir(),
		CarriedOutCalls: []CarriedOutCall{{
			ToolName:  "message_delete",
			ToolInput: json.RawMessage(`{"messageIDs":["message-1"]}`),
			Result:    testToolSuccess(`{"deletedMessageIDs":["message-1"]}`),
		}},
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected the continuation to finish from restored evidence, got %+v", result)
	}
	if searchCallCount != 0 || deleteCallCount != 0 {
		t.Fatalf("a resumed turn that redoes its pre-pause work does it twice, search=%d delete=%d", searchCallCount, deleteCallCount)
	}
}
