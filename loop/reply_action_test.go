package loop

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestANonFinalReplyReachesTheRequesterAndTheTurnKeepsWorking(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","message":"초안을 먼저 보냅니다.","final":false}`,
		finishMessageDocument("초안을 정리해 보냈습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	sentCheckpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "초안 보여줘",
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			sentCheckpoints = append(sentCheckpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to continue after a non-final reply: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected the turn to complete, got %s", result.TaskRun.Status)
	}
	if len(sentCheckpoints) != 1 || sentCheckpoints[0].Message != "초안을 먼저 보냅니다." {
		t.Fatalf("expected the non-final reply to reach the requester, got %+v", sentCheckpoints)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, agentcontract.TaskEventAgentReplySent, "초안을 먼저 보냅니다.") {
		t.Fatalf("expected a reply receipt in the ledger, got %+v", events)
	}
}

func TestAReplyWithoutAChannelTellsTheModelItWasNotDelivered(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","message":"중간 보고입니다.","final":false}`,
		finishMessageDocument("정리해 보냈습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "중간 보고 해줘",
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to continue without a reply channel: %v", errorValue)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, agentcontract.TaskEventAgentReplyFailed, "missing_sender") {
		t.Fatalf("expected an undelivered reply receipt in the ledger, got %+v", events)
	}
}

func TestAReplyExpectingAnAnswerLeavesTheRunWaitingForTheUser(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","expectsAnswer":true,"message":"어느 쪽으로 진행할까요?"}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{toolcontract.AskInputToolName})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.AskInputToolName, Visibility: toolcontract.ToolVisibilityInternal}, func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		taskRunID := TaskRunIDFromContext(toolContext)
		services.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingUserInput, "어느 쪽으로 진행할까요?")
		services.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAskRequested, string(invocation.Input))
		return testToolSuccess(`{"kind":"ask_input"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "둘 중 하나 골라줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected the question to pause the run: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusWaitingUserInput {
		t.Fatalf("expected the run to wait for the user, got %s", result.TaskRun.Status)
	}
	if result.UserNotice != "어느 쪽으로 진행할까요?" {
		t.Fatalf("expected the question to reach the requester, got %q", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), agentcontract.TaskEventAskRequested, "어느 쪽으로 진행할까요?") {
		t.Fatal("expected the ask request event the intake resume path reads")
	}
}
