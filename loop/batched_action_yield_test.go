package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type batchingLanguageModel struct {
	firstBatch     []model.ChatCompletionToolCall
	actionRequests int
	lastRequest    model.ChatCompletionRequest
}

func (languageModel *batchingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *batchingLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == expectedChangesSchemaName {
		return model.StructuredResponse{Content: expectedChangesDocument()}, nil
	}
	return model.StructuredResponse{Content: finishMessageDocument("done")}, nil
}

func (languageModel *batchingLanguageModel) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	if request.SchemaName != agentActionSchemaName {
		return model.ChatCompletionResponse{FinishReason: "stop", Message: model.ChatCompletionMessage{Role: "assistant", Content: "done"}}, nil
	}
	languageModel.actionRequests++
	languageModel.lastRequest = request
	if languageModel.actionRequests == 1 {
		return model.ChatCompletionResponse{FinishReason: "tool_calls", Message: model.ChatCompletionMessage{Role: "assistant", ToolCalls: languageModel.firstBatch}}, nil
	}
	return nativeAgentActionChatResponse("reply", `{"final":true,"message":"done","goalStatus":"satisfied","goalSatisfied":true,"hasRemainingWork":false,"completionEvidenceIDs":[],"qualityReview":[]}`), nil
}

func probeCalls(count int) []model.ChatCompletionToolCall {
	calls := []model.ChatCompletionToolCall{}
	for index := 0; index < count; index++ {
		calls = append(calls, nativeAgentActionToolCall("probe", fmt.Sprintf(`{"attempt":%d}`, index)))
	}
	return calls
}

func runProbeBatch(t *testing.T, languageModel *batchingLanguageModel, options TurnOptions, probeOutput func(int) string) (AgentTurnResult, int) {
	t.Helper()
	services := newTurnRunnerTestServices(languageModel, options)
	toolSet := newTestToolSet([]string{"probe"})
	probeCallCount := 0
	registerTestTool(toolSet, testToolDescriptor("probe"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		probeCallCount++
		return testToolSuccess(probeOutput(probeCallCount)), nil
	})
	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterIdentity: RequesterIdentity{
			RequesterPersonID: "person-1",
		},
		ConversationID:  "conversation-1",
		Prompt:          "check the file",
		TaskLevel:       options.TaskLevel,
		ToolSet:         toolSet,
		PinnedToolNames: []string{"probe"},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	return result, probeCallCount
}

func TestABatchedCallThatRepeatsAnEarlierOutputHandsTheTurnBackToTheModel(t *testing.T) {
	languageModel := &batchingLanguageModel{firstBatch: probeCalls(6)}
	_, probeCallCount := runProbeBatch(t, languageModel, TurnOptions{MaxIterationCount: 20, TaskLevel: TaskLevelMedium}, func(int) string { return "the file is there" })

	if probeCallCount != 2 {
		t.Fatalf("expected the batch to stop at the first repeated output, ran %d of 6 calls", probeCallCount)
	}
	if languageModel.actionRequests != 2 {
		t.Fatalf("expected the model to be asked again after the repeat, got %d action requests", languageModel.actionRequests)
	}
	note := notRunNote(languageModel.lastRequest, batchStoppedByRepeat)
	for attempt := 2; attempt < 6; attempt++ {
		if !strings.Contains(note, fmt.Sprintf(`probe {"attempt":%d}`, attempt)) {
			t.Fatalf("expected dropped call %d to reach the model as not run, got %q", attempt, note)
		}
	}
}

func TestEveryCallDroppedAfterAFailureReachesTheModelAsNotRun(t *testing.T) {
	languageModel := &batchingLanguageModel{firstBatch: probeCalls(3)}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 20, TaskLevel: TaskLevelMedium})
	toolSet := newTestToolSet([]string{"probe"})
	probeCallCount := 0
	registerTestTool(toolSet, testToolDescriptor("probe"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		probeCallCount++
		return toolcontract.ToolResult{}, errors.New("probe failed")
	})
	_, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterIdentity: RequesterIdentity{
			RequesterPersonID: "person-1",
		},
		ConversationID:  "conversation-1",
		Prompt:          "check the file",
		TaskLevel:       TaskLevelMedium,
		ToolSet:         toolSet,
		PinnedToolNames: []string{"probe"},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	if probeCallCount != 1 {
		t.Fatalf("expected the batch to stop at the failure, ran %d calls", probeCallCount)
	}
	note := notRunNote(languageModel.lastRequest, batchStoppedByFailure)
	if !strings.Contains(note, `probe {"attempt":1}`) || !strings.Contains(note, `probe {"attempt":2}`) {
		t.Fatalf("expected both calls after the failure to reach the model as not run, got %q", note)
	}
}

func TestAResponseRepeatingACallThousandsOfTimesLeavesOneBoundedNote(t *testing.T) {
	calls := probeCalls(1)
	for index := 0; index < 5000; index++ {
		calls = append(calls, nativeAgentActionToolCall("probe", `{"attempt":"again"}`))
	}
	for index := 0; index < 5000; index++ {
		calls = append(calls, nativeAgentActionToolCall("probe", fmt.Sprintf(`{"attempt":"invented-%d"}`, index)))
	}
	languageModel := &batchingLanguageModel{firstBatch: calls}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 20, TaskLevel: TaskLevelMedium})
	toolSet := newTestToolSet([]string{"probe"})
	registerTestTool(toolSet, testToolDescriptor("probe"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{}, errors.New("probe failed")
	})
	if _, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterIdentity: RequesterIdentity{RequesterPersonID: "person-1"},
		ConversationID:    "conversation-1",
		Prompt:            "check the file",
		TaskLevel:         TaskLevelMedium,
		ToolSet:           toolSet,
		PinnedToolNames:   []string{"probe"},
	}); errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	note := notRunNote(languageModel.lastRequest, batchStoppedByFailure)
	if !strings.Contains(note, "10000 calls") || !strings.Contains(note, `probe {"attempt":"again"} (repeated 5000 times)`) {
		t.Fatalf("expected one note counting every dropped call, got %q", note[:min(len(note), 400)])
	}
	if len(note) > services.runner.toolResultLimit()+100 {
		t.Fatalf("expected the note to stay within one tool result's share, got %d characters", len(note))
	}
	if !strings.Contains(note, "more distinct calls") {
		t.Fatal("expected the distinct calls past the share to be counted, not listed")
	}
}

func notRunNote(request model.ChatCompletionRequest, reason string) string {
	notes := []string{}
	for _, message := range request.Messages {
		if strings.HasPrefix(message.Content, "Runtime observation (not_run): Not run: "+reason) {
			notes = append(notes, message.Content)
		}
	}
	if len(notes) != 1 {
		return fmt.Sprintf("%d notes", len(notes))
	}
	return notes[0]
}

func TestABatchStillRunsWhileEachCallReturnsSomethingNew(t *testing.T) {
	languageModel := &batchingLanguageModel{firstBatch: probeCalls(4)}
	_, probeCallCount := runProbeBatch(t, languageModel, TurnOptions{MaxIterationCount: 20, TaskLevel: TaskLevelMedium}, func(call int) string { return fmt.Sprintf("result %d", call) })

	if probeCallCount != 4 || languageModel.actionRequests != 2 {
		t.Fatalf("expected all four new results without a model call between them, ran %d with %d action requests", probeCallCount, languageModel.actionRequests)
	}
}

func TestLimitPressureHandsAPendingBatchBackToTheModelBeforeTheLimit(t *testing.T) {
	languageModel := &batchingLanguageModel{firstBatch: probeCalls(12)}
	result, probeCallCount := runProbeBatch(t, languageModel, TurnOptions{MaxIterationCount: 40, MaxToolCallCount: 10, TaskLevel: TaskLevelMedium}, func(call int) string { return fmt.Sprintf("result %d", call) })

	if languageModel.actionRequests != 2 {
		t.Fatalf("expected the model to be asked again once the budget came under pressure, got %d action requests", languageModel.actionRequests)
	}
	if probeCallCount >= 10 {
		t.Fatalf("expected the batch to yield before the tool-call limit, ran %d calls", probeCallCount)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected the model's reply to finish the turn, got %s", result.TaskRun.Status)
	}
}

func TestACallRepeatedInOneResponseRunsOnce(t *testing.T) {
	languageModel := &batchingLanguageModel{firstBatch: []model.ChatCompletionToolCall{
		nativeAgentActionToolCall("probe", `{"message":"the pdf"}`),
		nativeAgentActionToolCall("probe", `{"message":"the pdf"}`),
		nativeAgentActionToolCall("probe", `{ "message" : "the pdf" }`),
		nativeAgentActionToolCall("probe", `{"message":"a second note"}`),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 20, TaskLevel: TaskLevelMedium})
	toolSet := newTestToolSet([]string{"probe"})
	callsByInput := map[string]int{}
	registerTestTool(toolSet, testToolDescriptor("probe"), func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var input struct {
			Message string `json:"message"`
		}
		json.Unmarshal(invocation.Input, &input)
		callsByInput[input.Message]++
		return testToolSuccess("sent " + input.Message), nil
	})
	if _, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterIdentity: RequesterIdentity{RequesterPersonID: "person-1"},
		ConversationID:    "conversation-1",
		Prompt:            "send the pdf",
		TaskLevel:         TaskLevelMedium,
		ToolSet:           toolSet,
		PinnedToolNames:   []string{"probe"},
	}); errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	if callsByInput["the pdf"] != 1 || callsByInput["a second note"] != 1 {
		t.Fatalf("expected each distinct call to run once, got %v", callsByInput)
	}
	note := notRunNote(languageModel.lastRequest, batchSkippedRepeat)
	if !strings.Contains(note, "2 calls") || !strings.Contains(note, "(repeated 2 times)") {
		t.Fatalf("expected the model to be told its repeats did not run, got %q", note)
	}
}
