package loop

import (
	"context"
	"fmt"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type batchingLanguageModel struct {
	firstBatch     []model.ChatCompletionToolCall
	actionRequests int
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
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "check the file",
		TaskLevel:         options.TaskLevel,
		ToolSet:           toolSet,
		PinnedToolNames:   []string{"probe"},
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
