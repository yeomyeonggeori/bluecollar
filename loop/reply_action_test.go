package loop

import (
	"context"
	"strconv"
	"strings"
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

func TestAQuestionTheRuntimeCannotAskComesBackToTheModelAsAFailure(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","expectsAnswer":true,"message":"어느 쪽으로 진행할까요?"}`,
		finishMessageDocument("직접 진행했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "둘 중 하나 골라줘",
		ToolSet:           newTestToolSet([]string{toolcontract.ShellToolName}),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to continue after an unaskable question: %v", errorValue)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, agentcontract.TaskEventAgentReplyFailed, "no_requester_to_answer") {
		t.Fatalf("expected a reply.failed receipt in the ledger, got %d events", len(events))
	}
	if !modelSawText(languageModel, "expectsAnswer is not available here") {
		t.Fatal("expected the refused question to come back to the model as a validation issue")
	}
}

func TestAQuestionCarriesItsChoicesAndItsAttachmentToTheRequester(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","expectsAnswer":true,"message":"어느 쪽으로 보낼까요?","choices":["이메일","메신저"],"attachments":[{"path":"report.pdf","filename":"report.pdf"}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{toolcontract.AskInputToolName, toolcontract.FileDeliverToolName})
	askedInput := ""
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.AskInputToolName, Visibility: toolcontract.ToolVisibilityInternal}, func(toolContext context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		askedInput = string(invocation.Input)
		taskRunID := TaskRunIDFromContext(toolContext)
		services.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingUserInput, "어느 쪽으로 보낼까요?")
		return testToolSuccess(`{"kind":"ask_input"}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName, Visibility: toolcontract.ToolVisibilityInternal}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output:      toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{DevicePath: "/tmp/report.pdf", Filename: "report.pdf"}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "보고서 보내줘",
		ToolSet:           toolRegistry,
	})
	if errorValue != nil {
		t.Fatalf("expected the question to pause the run: %v", errorValue)
	}
	if !strings.Contains(askedInput, "이메일") || !strings.Contains(askedInput, "메신저") {
		t.Fatalf("expected the choices to reach ask_input, got %s", askedInput)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "report.pdf" {
		t.Fatalf("expected the attachment to ride the paused result, got %+v", result.Attachments)
	}
}

func TestANonFinalReplyDeliversItsAttachmentOnlyOnce(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","final":false,"message":"초안을 먼저 보냅니다.","attachments":[{"path":"draft.pdf","filename":"draft.pdf"}]}`,
		finishMessageDocument("초안을 보냈습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{toolcontract.ShellToolName})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName, Visibility: toolcontract.ToolVisibilityInternal}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output:      toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{DevicePath: "/tmp/draft.pdf", Filename: "draft.pdf"}},
		}, nil
	})
	sentCheckpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "초안 보내줘",
		ToolSet:           toolRegistry,
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			sentCheckpoints = append(sentCheckpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to complete: %v", errorValue)
	}
	if len(sentCheckpoints) != 1 || len(sentCheckpoints[0].Attachments) != 1 {
		t.Fatalf("expected the mid-task reply to carry the file, got %+v", sentCheckpoints)
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected the delivered file not to ride the final result again, got %+v", result.Attachments)
	}
}

func TestRepeatedProgressRepliesStopTheTurn(t *testing.T) {
	progressReplies := []string{}
	for index := 0; index < 24; index++ {
		progressReplies = append(progressReplies, `{"action":"reply","final":false,"message":"작업 중입니다 `+strconv.Itoa(index)+`."}`)
	}
	languageModel := &sequenceLanguageModel{contents: progressReplies}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 30})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "진행 상황 알려줘",
		CheckpointSender:  func(context.Context, AgentCheckpoint) error { return nil },
	})
	if errorValue != nil {
		t.Fatalf("expected the stalled turn to return: %v", errorValue)
	}
	if result.TaskRun.Status == agentcontract.TaskStatusCompleted {
		t.Fatal("expected talking without working to stop the turn rather than complete it")
	}
	replyCount := 0
	for _, event := range services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID) {
		if event.Name == agentcontract.TaskEventAgentReplySent {
			replyCount++
		}
	}
	if replyCount >= len(progressReplies) {
		t.Fatalf("expected the stall to stop the replies before the script ran out, got %d", replyCount)
	}
}

func modelSawText(languageModel *sequenceLanguageModel, text string) bool {
	for _, request := range languageModel.requests {
		for _, message := range request.Messages {
			if strings.Contains(message.Content, text) {
				return true
			}
		}
	}
	return false
}

func TestAFinalReplyWhoseAttachmentFailsNeverClosesTheTaskPromisingTheFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","final":true,"message":"보고서를 첨부했습니다.","attachments":[{"path":"report.pdf"}],"goalStatus":"satisfied","goalSatisfied":true}`,
		finishMessageDocument("보고서를 첨부하지 못했습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{toolcontract.ShellToolName})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName, Visibility: toolcontract.ToolVisibilityInternal}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound, toolcontract.FileDeliverToolName, "report.pdf does not exist"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "보고서 보내줘",
		ToolSet:           toolRegistry,
		CheckpointSender:  func(context.Context, AgentCheckpoint) error { return nil },
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to continue after the attachment failed: %v", errorValue)
	}
	if result.FinishMessage == "보고서를 첨부했습니다." {
		t.Fatal("expected the task never to close on words promising a file it did not deliver")
	}
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no attachment on the result, got %+v", result.Attachments)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, agentcontract.TaskEventAgentReplyFailed, "report.pdf does not exist") {
		t.Fatal("expected the failed attachment to be recorded as a failed reply")
	}
}

func TestACancelledTaskPostsNothingFurther(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		directToolAction("continue", "", "notes_write", `{"path":"draft.md"}`),
		`{"action":"reply","final":false,"message":"계속 진행합니다."}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"notes_write"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "notes_write"}, func(toolContext context.Context, _ toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		services.taskRunService.CancelTaskRun(TaskRunIDFromContext(toolContext), "person-1")
		return testToolSuccess(`{"status":"written"}`), nil
	})
	sentCheckpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "초안 써줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"notes_write"},
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			sentCheckpoints = append(sentCheckpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected the cancelled turn to return: %v", errorValue)
	}
	if len(sentCheckpoints) != 0 {
		t.Fatalf("expected a cancelled task to post nothing, got %+v", sentCheckpoints)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCancelled {
		t.Fatalf("expected the cancelled run to be reported as cancelled, got %s", result.TaskRun.Status)
	}
}

func TestATaskSendsAtMostThreeProgressRepliesAndNeverTheSameOneTwice(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","final":false,"message":"첫 번째 보고입니다."}`,
		`{"action":"reply","final":false,"message":"첫 번째 보고입니다."}`,
		`{"action":"reply","final":false,"message":"두 번째 보고입니다."}`,
		`{"action":"reply","final":false,"message":"세 번째 보고입니다."}`,
		`{"action":"reply","final":false,"message":"네 번째 보고입니다."}`,
		finishMessageDocument("끝났습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 10})
	sentCheckpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "진행 상황 알려줘",
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			sentCheckpoints = append(sentCheckpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	if len(sentCheckpoints) != 3 {
		t.Fatalf("expected the duplicate and the fourth update to be refused, got %+v", sentCheckpoints)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(events, agentcontract.TaskEventAgentReplyFailed, "rate_limited_or_duplicate") {
		t.Fatal("expected the refused update to come back to the model as a receipt")
	}
	if !modelSawText(languageModel, "already sent its progress updates") {
		t.Fatal("expected the model to read why its update did not go out")
	}
	deliveredStepCount := 0
	refusedStepCount := 0
	for _, taskStep := range services.taskStepService.ListTaskStep(result.TaskRun.TaskRunID) {
		if taskStep.Instruction != "reply" {
			continue
		}
		if taskStep.Status == agentcontract.TaskStatusCompleted {
			deliveredStepCount++
			continue
		}
		refusedStepCount++
	}
	if deliveredStepCount != 3 || refusedStepCount != 2 {
		t.Fatalf("expected a step to be saved by what the receipt says, got %d delivered and %d refused", deliveredStepCount, refusedStepCount)
	}
}

func TestADelegatedChildReportsToItsCallerRatherThanToTheRequester(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		delegateActionDocument("write the release notes", "the text"),
		`{"action":"reply","final":false,"message":"중간 보고입니다."}`,
		`{"action":"reply","expectsAnswer":true,"message":"어느 쪽으로 할까요?"}`,
		finishMessageDocument("릴리스 노트를 정리했습니다."),
		finishMessageDocument("정리해 두었습니다."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8, DelegationLimit: 1})
	toolRegistry := newTestToolSet([]string{toolcontract.AskInputToolName})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.AskInputToolName, Visibility: toolcontract.ToolVisibilityInternal}, func(toolContext context.Context, _ toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		services.taskRunService.PauseTaskRun(TaskRunIDFromContext(toolContext), agentcontract.TaskStatusWaitingUserInput, "어느 쪽으로 할까요?")
		return testToolSuccess(`{"kind":"ask_input"}`), nil
	})
	sentCheckpoints := []AgentCheckpoint{}

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "릴리스 노트 정리해줘",
		ToolSet:           toolRegistry,
		CheckpointSender: func(_ context.Context, checkpoint AgentCheckpoint) error {
			sentCheckpoints = append(sentCheckpoints, checkpoint)
			return nil
		},
	})
	if errorValue != nil {
		t.Fatalf("expected the delegated turn to run: %v", errorValue)
	}
	if len(sentCheckpoints) != 0 {
		t.Fatalf("expected a child's words to reach its caller only, got %+v", sentCheckpoints)
	}
	for _, taskRun := range services.taskRunService.ListTaskRun() {
		if taskRun.Status == agentcontract.TaskStatusWaitingUserInput {
			t.Fatalf("a child that pauses for an answer strands its caller: %s", taskRun.TaskRunID)
		}
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected the parent to finish, got %s", result.TaskRun.Status)
	}
}
