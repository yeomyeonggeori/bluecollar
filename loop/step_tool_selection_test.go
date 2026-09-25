package loop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recordingToolSelector struct {
	needs          []string
	countLimits    []int
	candidateNames [][]string
	selectedTools  []agentcontract.SelectedTool
}

func (selector *recordingToolSelector) SelectToolNames(_ context.Context, need agentcontract.ToolSelectionNeed) ([]agentcontract.SelectedTool, error) {
	selector.needs = append(selector.needs, need.Need)
	selector.countLimits = append(selector.countLimits, need.CountLimit)
	selector.candidateNames = append(selector.candidateNames, need.CallableToolNames)
	return selector.selectedTools, nil
}

func TestConsecutiveIterationsOfOneStepSendTheSameInstructionAndToolCatalog(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "move the deal forward",
		TaskLevel:       TaskLevelMedium,
		ToolSet:         testToolSet(append(testBuiltInToolNames(), "deal_update", "deal_list")),
		PinnedToolNames: []string{"deal_update"},
	}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-1")

	firstIteration := services.runner.requestForStep(context.Background(), request, &state)
	firstPrompt := buildAgentActionRequest(services.runner.actionStateForIteration(firstIteration, nil, state, false), true, false)

	state.Observations = append(state.Observations, newContentObservation("obs-001", "continue", "deal_update", "moved the deal"))
	state.IterationCount = 1
	state.ToolCallCount = 1
	secondIteration := services.runner.requestForStep(context.Background(), request, &state)
	secondPrompt := buildAgentActionRequest(services.runner.actionStateForIteration(secondIteration, nil, state, false), true, false)

	if firstPrompt.Messages[0].Content != secondPrompt.Messages[0].Content {
		t.Fatalf("expected a byte-identical system instruction within one step.\nfirst:\n%s\nsecond:\n%s", firstPrompt.Messages[0].Content, secondPrompt.Messages[0].Content)
	}
	firstCatalog := buildAgentToolDescription(modelCallableToolSet(firstIteration.ToolSet, false))
	secondCatalog := buildAgentToolDescription(modelCallableToolSet(secondIteration.ToolSet, false))
	if firstCatalog != secondCatalog {
		t.Fatalf("expected a byte-identical tool catalog within one step.\nfirst:\n%s\nsecond:\n%s", firstCatalog, secondCatalog)
	}
	if firstPrompt.Messages[1].Content != secondPrompt.Messages[1].Content {
		t.Fatalf("expected a byte-identical unchanging context within one step.\nfirst:\n%s\nsecond:\n%s", firstPrompt.Messages[1].Content, secondPrompt.Messages[1].Content)
	}
}

func TestAPlanStepChangeReselectsTheShortlist(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "deal_update"}}}
	services.runner.UseToolSelector(selector)
	request := AgentTurnRequest{ToolSet: testToolSet(append(testBuiltInToolNames(), "deal_update", "deal_list"))}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-2")

	services.runner.applyPlanObservation(context.Background(), "task-step-2", &state, planUpdateSuccessObservation("obs-001",
		`{"steps":[{"title":"read the deal","status":"in_progress"},{"title":"move the deal","status":"pending"}]}`))
	services.runner.applyPlanObservation(context.Background(), "task-step-2", &state, planUpdateSuccessObservation("obs-002",
		`{"steps":[{"title":"read the deal","status":"in_progress"},{"title":"move the deal","status":"pending"}]}`))
	services.runner.applyPlanObservation(context.Background(), "task-step-2", &state, planUpdateSuccessObservation("obs-003",
		`{"steps":[{"title":"read the deal","status":"done"},{"title":"move the deal","status":"in_progress"}]}`))

	if len(selector.needs) != 2 || selector.needs[0] != "read the deal" || selector.needs[1] != "move the deal" {
		t.Fatalf("expected one selection per active step, got %+v", selector.needs)
	}
	for _, countLimit := range selector.countLimits {
		if countLimit != toolcontract.MaxLikelyToolCountForOnePlanStep {
			t.Fatalf("expected the step-sized cap, got %d", countLimit)
		}
	}
	if len(state.PlanStepToolNames) != 1 || state.PlanStepToolNames[0] != "deal_update" {
		t.Fatalf("expected the step shortlist to replace the pinned tools, got %+v", state.PlanStepToolNames)
	}
	stepRequest := services.runner.requestForStep(context.Background(), request, &state)
	if !stepRequest.ToolSet.IsAllowed("deal_update") || stepRequest.ToolSet.IsAllowed("deal_list") {
		t.Fatalf("expected only the step shortlist exposed, got %+v", stepRequest.ToolSet.ListToolNames())
	}
}

func TestQueuedActionKeepsTheExposureOfItsOriginalModelRequest(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "deal_update"}}}
	services.runner.UseToolSelector(selector)
	toolSet := testToolSet(append(testBuiltInToolNames(), "deal_list", "deal_update"))
	request := AgentTurnRequest{
		ToolSet:         toolSet,
		PinnedToolNames: []string{"deal_list", "deal_update"},
		LikelyToolNames: []string{"deal_list", "deal_update"},
	}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-batch")

	originalRequest := services.runner.requestForStep(context.Background(), request, &state)
	if !originalRequest.ToolSet.IsAllowed("deal_list") {
		t.Fatal("expected the initial model request to expose the queued read tool")
	}
	services.runner.applyPlanObservation(context.Background(), "task-step-batch", &state, planUpdateSuccessObservation("obs-plan-1",
		`{"steps":[{"title":"move the deal","status":"in_progress"}]}`))
	rememberBatchedActions(&state, turnActionDocument{BatchedActions: []turnActionDocument{{Action: "continue", ToolName: "deal_list"}}}, originalRequest.ToolSet.ListToolNames(), originalRequest.ToolExposure)

	queuedRequest := services.runner.requestForStep(context.Background(), request, &state)
	if !queuedRequest.ToolSet.IsAllowed("deal_list") {
		t.Fatalf("expected queued call to retain its request-time tool exposure, got %+v", queuedRequest.ToolSet.ListToolNames())
	}
	if !queuedRequest.ToolSet.CanExpose("deal_list") {
		t.Fatal("expected the queued tool to pass current registration and availability checks")
	}
	if _, isBatched := takeBatchedAction(&state); !isBatched {
		t.Fatal("expected queued action to run without another model call")
	}

	nextRequest := services.runner.requestForStep(context.Background(), request, &state)
	if nextRequest.ToolSet.IsAllowed("deal_list") || !nextRequest.ToolSet.IsAllowed("deal_update") {
		t.Fatalf("expected the next model call to use the newly selected shortlist, got %+v", nextRequest.ToolSet.ListToolNames())
	}
}

func TestQueuedExposureRetainsApprovalAndDelegationGuards(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	toolSet := newTestToolSet([]string{"calendar_delete"})
	toolDefinition := testToolDescriptor("calendar_delete")
	toolDefinition.RequiresApproval = true
	handlerCallCount := 0
	registerTestTool(toolSet, toolDefinition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		handlerCallCount++
		return testToolSuccess("deleted"), nil
	})
	toolSet.UseToolCallGate(holdingToolCallGate{taskRunService: services.taskRunService, confirmation: "Confirm deletion", denialNotice: "Delegated tasks cannot delete events"})
	request := AgentTurnRequest{ToolSet: toolSet}
	state := agentTaskState{PendingBatchedActions: []turnActionDocument{{Action: "continue", ToolName: "calendar_delete"}}, PendingBatchedToolNames: []string{"calendar_delete"}}
	queuedRequest := services.runner.requestForStep(context.Background(), request, &state)

	approvalResult, errorValue := queuedRequest.ToolSet.Invoke(context.Background(), toolcontract.ToolInvocation{ToolName: "calendar_delete", Input: json.RawMessage(`{"eventHint":"event-1"}`)})
	if errorValue != nil || approvalResult.Failure == nil || !approvalResult.Failure.RequiresApproval {
		t.Fatalf("expected queued call to remain held for approval, result=%+v error=%v", approvalResult, errorValue)
	}
	delegatedResult, errorValue := queuedRequest.ToolSet.Invoke(toolcontract.WithDelegatedTurn(context.Background()), toolcontract.ToolInvocation{ToolName: "calendar_delete", Input: json.RawMessage(`{"eventHint":"event-1"}`)})
	if errorValue != nil || delegatedResult.Failure == nil || delegatedResult.Failure.Code != toolcontract.FailureCodes.PolicyBlocked.String() {
		t.Fatalf("expected queued delegated call to remain denied, result=%+v error=%v", delegatedResult, errorValue)
	}
	if handlerCallCount != 0 {
		t.Fatalf("expected approval and delegation guards to prevent the effect, handler calls=%d", handlerCallCount)
	}
}

func TestQueuedExposureFailsClosedWhenEveryCapturedToolIsDenied(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	toolSet := toolcontract.NewToolSet([]string{"deal_list", "deal_update"})
	registerTestTool(toolSet, testToolDescriptor("deal_list"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("read"), nil
	})
	if errorValue := toolSet.RegisterBoundTool(toolcontract.BoundTool{
		Definition:   testToolDescriptor("deal_update"),
		Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityDenied},
		Handler: func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("updated"), nil
		},
	}); errorValue != nil {
		t.Fatalf("expected denied tool to register: %v", errorValue)
	}
	request := AgentTurnRequest{ToolSet: toolSet}
	state := agentTaskState{
		PendingBatchedActions:   []turnActionDocument{{Action: "continue", ToolName: "deal_update"}},
		PendingBatchedToolNames: []string{"deal_update"},
	}
	state.PendingBatchedToolExposure.ExposedToolIDs = []string{"deal_update"}

	queuedRequest := services.runner.requestForStep(context.Background(), request, &state)
	if len(queuedRequest.ToolExposure.ExposedToolIDs) != 0 || len(queuedRequest.ToolSet.ListToolNames()) != 0 {
		t.Fatalf("expected denied captured tool to leave no exposed tool, got exposure=%+v allowed=%+v", queuedRequest.ToolExposure, queuedRequest.ToolSet.ListToolNames())
	}
	if queuedRequest.ToolSet.IsRegistered("deal_update") || queuedRequest.ToolSet.IsRegistered("deal_list") {
		t.Fatalf("expected no captured or unrelated tool to remain invocable, got deal_update=%v deal_list=%v", queuedRequest.ToolSet.IsRegistered("deal_update"), queuedRequest.ToolSet.IsRegistered("deal_list"))
	}
}

func TestRunTurnExecutesBatchedReadAfterPlanChangesTheShortlist(t *testing.T) {
	languageModel := &batchedPlanExposureLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, TaskLevel: TaskLevelMedium})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "deal_update"}}}
	services.runner.UseToolSelector(selector)
	toolSet := newTestToolSet([]string{toolcontract.PlanToolName, "deal_list", "deal_update"})
	registerTestTool(toolSet, toolcontract.ToolDefinition{Name: toolcontract.PlanToolName, SideEffectClass: toolcontract.ToolSideEffectNone, ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)}}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var input planDocument
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		input.Goal, input.Steps = NormalizePlan(input.Goal, input.Steps)
		document := marshalEventBody(input)
		return toolcontract.ToolSuccessData(document, json.RawMessage(document)), nil
	})
	dealListCallCount := 0
	registerTestTool(toolSet, testToolDescriptor("deal_list"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		dealListCallCount++
		return testToolSuccess("deal list"), nil
	})
	registerTestTool(toolSet, testToolDescriptor("deal_update"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("deal updated"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "move the deal forward",
		TaskLevel:         TaskLevelMedium,
		ToolSet:           toolSet,
		PinnedToolNames:   []string{toolcontract.PlanToolName, "deal_list", "deal_update"},
		LikelyToolNames:   []string{"deal_list", "deal_update"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to complete: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted || dealListCallCount != 1 {
		t.Fatalf("expected the queued read and final reply to complete, status=%s deal_list calls=%d", result.TaskRun.Status, dealListCallCount)
	}
	if len(languageModel.actionRequests) != 2 {
		t.Fatalf("expected one batched model response and one follow-up request, got %d calls", len(languageModel.actionRequests))
	}
	if !chatRequestIncludesTool(languageModel.actionRequests[0], "deal_list") {
		t.Fatal("expected original model request to expose the queued read")
	}
	if chatRequestIncludesTool(languageModel.actionRequests[1], "deal_list") || !chatRequestIncludesTool(languageModel.actionRequests[1], "deal_update") {
		t.Fatalf("expected follow-up model request to use the newly selected shortlist, tools=%+v", chatRequestToolNames(languageModel.actionRequests[1]))
	}
}

type batchedPlanExposureLanguageModel struct {
	actionRequests []model.ChatCompletionRequest
}

func (languageModel *batchedPlanExposureLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *batchedPlanExposureLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == expectedChangesSchemaName {
		return model.StructuredResponse{Content: expectedChangesDocument()}, nil
	}
	return model.StructuredResponse{Content: finishMessageDocument("done")}, nil
}

func (languageModel *batchedPlanExposureLanguageModel) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	if request.SchemaName != agentActionSchemaName {
		return model.ChatCompletionResponse{FinishReason: "stop", Message: model.ChatCompletionMessage{Role: "assistant", Content: "done"}}, nil
	}
	languageModel.actionRequests = append(languageModel.actionRequests, request)
	if len(languageModel.actionRequests) == 1 {
		return model.ChatCompletionResponse{FinishReason: "tool_calls", Message: model.ChatCompletionMessage{Role: "assistant", ToolCalls: []model.ChatCompletionToolCall{
			nativeAgentActionToolCall(toolcontract.PlanToolName, `{"goal":"move the deal","steps":[{"title":"move the deal","status":"in_progress"}]}`),
			nativeAgentActionToolCall("deal_list", `{}`),
		}}}, nil
	}
	return nativeAgentActionChatResponse("reply", `{"final":true,"message":"done","goalStatus":"satisfied","goalSatisfied":true,"hasRemainingWork":false,"completionEvidenceIDs":[],"qualityReview":[]}`), nil
}

func chatRequestIncludesTool(request model.ChatCompletionRequest, toolName string) bool {
	return stringSliceContains(chatRequestToolNames(request), toolName)
}

func chatRequestToolNames(request model.ChatCompletionRequest) []string {
	toolNames := []string{}
	for _, tool := range request.Tools {
		toolNames = append(toolNames, tool.Function.Name)
	}
	return toolNames
}

func TestAClosingPlanKeepsTheCurrentShortlist(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "deal_update"}}}
	services.runner.UseToolSelector(selector)
	request := AgentTurnRequest{ToolSet: testToolSet(append(testBuiltInToolNames(), "deal_update", "deal_list"))}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-3")

	services.runner.applyPlanObservation(context.Background(), "task-step-3", &state, planUpdateSuccessObservation("obs-001",
		`{"steps":[{"title":"move the deal","status":"in_progress"}]}`))
	services.runner.applyPlanObservation(context.Background(), "task-step-3", &state, planUpdateSuccessObservation("obs-002",
		`{"steps":[{"title":"move the deal","status":"done"}]}`))

	if len(selector.needs) != 1 || selector.needs[0] != "move the deal" {
		t.Fatalf("expected the closing plan to spend no selection call, got %+v", selector.needs)
	}
	if len(state.PlanStepToolNames) != 1 || state.PlanStepToolNames[0] != "deal_update" {
		t.Fatalf("expected the shortlist to survive the closing plan, got %+v", state.PlanStepToolNames)
	}
}

type failingToolSelector struct {
	needs []string
}

func (selector *failingToolSelector) SelectToolNames(_ context.Context, need agentcontract.ToolSelectionNeed) ([]agentcontract.SelectedTool, error) {
	selector.needs = append(selector.needs, need.Need)
	return nil, errors.New("the selector was unreachable")
}

func TestAFailedSelectionNeverLeavesTheLastStepsToolsOnTheNewStep(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "deal_list"}}}
	services.runner.UseToolSelector(selector)
	request := AgentTurnRequest{ToolSet: testToolSet(append(testBuiltInToolNames(), "deal_update", "deal_list"))}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-4")

	services.runner.applyPlanObservation(context.Background(), "task-step-4", &state, planUpdateSuccessObservation("obs-001",
		`{"steps":[{"title":"read the deal","status":"in_progress"},{"title":"move the deal","status":"pending"}]}`))
	failing := &failingToolSelector{}
	services.runner.UseToolSelector(failing)
	services.runner.applyPlanObservation(context.Background(), "task-step-4", &state, planUpdateSuccessObservation("obs-002",
		`{"steps":[{"title":"read the deal","status":"done"},{"title":"move the deal","status":"in_progress"}]}`))

	if len(state.PlanStepToolNames) != 0 {
		t.Fatalf("expected a failed selection to drop the previous step's shortlist, got %+v", state.PlanStepToolNames)
	}
	if state.ActivePlanStepTitle != "read the deal" {
		t.Fatalf("expected the unselected step to stay open for another try, got %q", state.ActivePlanStepTitle)
	}
	services.runner.applyPlanObservation(context.Background(), "task-step-4", &state, planUpdateSuccessObservation("obs-003",
		`{"steps":[{"title":"read the deal","status":"done"},{"title":"move the deal","status":"in_progress"}]}`))
	if len(failing.needs) != 2 {
		t.Fatalf("expected the next plan observation to try the same step again, got %+v", failing.needs)
	}
}

func TestAStepShortlistReplacesTheLikelyToolsAndKeepsTheHostsPins(t *testing.T) {
	request := AgentTurnRequest{
		ToolSet:         testToolSet(append(testBuiltInToolNames(), "deal_update", "deal_list", "memory_search")),
		PinnedToolNames: []string{"memory_search", "deal_list"},
		LikelyToolNames: []string{"deal_list"},
	}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-5")
	state.PlanStepToolNames = []string{"deal_update"}

	stepToolNames := planStepPinnedToolNames(request, state)

	if !stringSliceContains(stepToolNames, "memory_search") {
		t.Fatalf("expected a tool the host pinned deliberately to survive the step change, got %+v", stepToolNames)
	}
	if stringSliceContains(stepToolNames, "deal_list") {
		t.Fatalf("expected intake's likely tool to give way to the step shortlist, got %+v", stepToolNames)
	}
	if !stringSliceContains(stepToolNames, "deal_update") {
		t.Fatalf("expected the step shortlist to be exposed, got %+v", stepToolNames)
	}
}

func TestAPlanStepRanksOnlyWithinTheTaskShortlist(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "deal_update"}}}
	services.runner.UseToolSelector(selector)
	request := AgentTurnRequest{
		ToolSet:         testToolSet(append(testBuiltInToolNames(), "deal_update", "deal_list", "invoice_send")),
		LikelyToolNames: []string{"deal_update", "deal_list"},
	}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-narrow")

	services.runner.applyPlanObservation(context.Background(), "task-step-narrow", &state, planUpdateSuccessObservation("obs-001",
		`{"steps":[{"title":"move the deal","status":"in_progress"}]}`))

	if len(selector.candidateNames) != 1 {
		t.Fatalf("expected one selection for the active step, got %+v", selector.candidateNames)
	}
	candidates := selector.candidateNames[0]
	if !sameStringSet(candidates, []string{"deal_update", "deal_list"}) {
		t.Fatalf("expected the step to rank only what the task already shortlisted, got %+v", candidates)
	}
}

func TestAPlanStepWithoutAShortlistRanksTheWholeToolSet(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "deal_update"}}}
	services.runner.UseToolSelector(selector)
	request := AgentTurnRequest{ToolSet: testToolSet(append(testBuiltInToolNames(), "deal_update", "deal_list"))}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-wide")

	services.runner.applyPlanObservation(context.Background(), "task-step-wide", &state, planUpdateSuccessObservation("obs-001",
		`{"steps":[{"title":"move the deal","status":"in_progress"}]}`))

	if len(selector.candidateNames) != 1 || len(selector.candidateNames[0]) != 0 {
		t.Fatalf("expected an empty candidate list so the selector falls back to the whole tool set, got %+v", selector.candidateNames)
	}
}

func TestAPlanStepRanksWhatEquipFoundEvenWhenTheTaskMissedIt(t *testing.T) {
	services := newTurnRunnerTestServices(&stubStructuredLanguageModel{}, TurnOptions{})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "invoice_send"}}}
	services.runner.UseToolSelector(selector)
	request := AgentTurnRequest{
		ToolSet:         testToolSet(append(testBuiltInToolNames(), "deal_update", "invoice_send")),
		LikelyToolNames: []string{"deal_update"},
	}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-equip")
	state.Observations = []turnObservation{{
		ObservationID: "obs-equip",
		Action:        "continue",
		Tool:          toolcontract.EquipToolName,
		Output: toolcontract.ToolOutput{
			Content: "found",
			Data:    json.RawMessage(`{"selectedTools":[{"name":"invoice_send","description":"send an invoice"}]}`),
		},
	}}

	services.runner.applyPlanObservation(context.Background(), "task-step-equip", &state, planUpdateSuccessObservation("obs-001",
		`{"steps":[{"title":"send the invoice","status":"in_progress"}]}`))

	if len(selector.candidateNames) != 1 {
		t.Fatalf("expected one selection for the active step, got %+v", selector.candidateNames)
	}
	if !sameStringSet(selector.candidateNames[0], []string{"deal_update", "invoice_send"}) {
		t.Fatalf("expected a tool the agent equipped itself to stay a candidate, got %+v", selector.candidateNames[0])
	}
}
