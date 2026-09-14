package loop

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type stallingThenAnsweringLanguageModel struct {
	stalledCalls int
	requests     []model.ChatCompletionRequest
}

func (languageModel *stallingThenAnsweringLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *stallingThenAnsweringLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, nil
}

func (languageModel *stallingThenAnsweringLanguageModel) GenerateChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	if len(languageModel.requests) <= languageModel.stalledCalls {
		<-ctx.Done()
		return model.ChatCompletionResponse{}, ctx.Err()
	}
	return nativeAgentActionChatResponse(toolcontract.ShellToolName, `{"command":"ls"}`), nil
}

func TestAStalledModelCallIsCutAtTheMeasuredPatienceAndAskedAgain(t *testing.T) {
	languageModel := &stallingThenAnsweringLanguageModel{stalledCalls: 1}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	services.runner.iterationCostObserver.Record("a-model", time.Millisecond)

	action, errorValue := services.runner.decideActionPatiently(context.Background(), "run-1", nativeAgentActionTestState())

	if errorValue != nil || action.ToolName != toolcontract.ShellToolName {
		t.Fatalf("the second ask should have answered: %v %+v", errorValue, action)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected one cut and one answer, got %d asks", len(languageModel.requests))
	}
	events := services.taskEventService.ListTaskEvent("run-1")
	if !taskEventsContain(events, "agent.model_call_cut", `"patienceSeconds":0`) {
		t.Fatalf("the cut should be on the ledger: %+v", events)
	}
}

func TestAnUnmeasuredModelIsNotCut(t *testing.T) {
	languageModel := &stallingThenAnsweringLanguageModel{stalledCalls: 1}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, errorValue := services.runner.decideActionPatiently(ctx, "run-1", nativeAgentActionTestState())

	if errorValue == nil || len(languageModel.requests) != 1 {
		t.Fatalf("without a measured cost the only clock is the caller's: %v after %d asks", errorValue, len(languageModel.requests))
	}
}

func TestACallerCancellationIsNotACut(t *testing.T) {
	languageModel := &stallingThenAnsweringLanguageModel{stalledCalls: 2}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	services.runner.iterationCostObserver.Record("a-model", time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, errorValue := services.runner.decideActionPatiently(ctx, "run-1", nativeAgentActionTestState())

	if errorValue == nil || len(languageModel.requests) != 1 {
		t.Fatalf("the caller's own deadline ends the ask without a retry: %v after %d asks", errorValue, len(languageModel.requests))
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent("run-1"), "agent.model_call_cut", "") {
		t.Fatal("the caller's deadline is not the runner's cut")
	}
}

func TestAToolThatChangesSomethingRunsOnTheClosingClock(t *testing.T) {
	services := newTurnRunnerTestServices(&stallingThenAnsweringLanguageModel{}, TurnOptions{MaxElapsedSecond: 30})
	startedAt := time.Now()
	request := AgentTurnRequest{
		EffortStartedAt: startedAt,
		ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
			{Name: "peek", SideEffectClass: toolcontract.ToolSideEffectRead},
			{Name: "poke", SideEffectClass: toolcontract.ToolSideEffectStateChange},
		}),
	}
	effortContext, cancelEffort := services.runner.currentEffortContext(context.Background(), startedAt)
	defer cancelEffort()

	readContext, cancelRead := services.runner.toolInvocationContext(context.Background(), effortContext, request, "peek")
	defer cancelRead()
	writeContext, cancelWrite := services.runner.toolInvocationContext(context.Background(), effortContext, request, "poke")
	defer cancelWrite()

	readDeadline, _ := readContext.Deadline()
	writeDeadline, _ := writeContext.Deadline()
	if !readDeadline.Equal(startedAt.Add(20 * time.Second)) {
		t.Fatalf("a read keeps the work clock, got %s", readDeadline.Sub(startedAt))
	}
	if !writeDeadline.Equal(startedAt.Add(30 * time.Second)) {
		t.Fatalf("a write gets the closing clock, got %s", writeDeadline.Sub(startedAt))
	}
}
