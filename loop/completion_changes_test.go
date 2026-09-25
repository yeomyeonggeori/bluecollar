package loop

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type stubStructuredLanguageModel struct {
	contents   []string
	errorValue error
	requests   []model.StructuredResponseRequest
}

func (languageModel *stubStructuredLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *stubStructuredLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	if len(languageModel.contents) == 0 {
		return model.StructuredResponse{}, languageModel.errorValue
	}
	index := min(len(languageModel.requests), len(languageModel.contents)) - 1
	return model.StructuredResponse{Content: languageModel.contents[index]}, languageModel.errorValue
}

type scriptedDecisionModel struct {
	noul       map[string]float64
	errorValue error
	cancel     context.CancelFunc
	requests   []model.DecisionRequest
}

func (decisionModel *scriptedDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.requests = append(decisionModel.requests, request)
	if decisionModel.cancel != nil {
		decisionModel.cancel()
	}
	if decisionModel.errorValue != nil {
		return model.DecisionResponse{}, decisionModel.errorValue
	}
	answers := map[string]model.DecisionAnswer{}
	for key := range request.Questions {
		answers[key] = model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Noul: decisionModel.noul[key]}
	}
	return model.DecisionResponse{Answers: answers}, nil
}

func taskToolsTestToolSet() *toolcontract.ToolSet {
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		testToolDescriptor("task_add"),
		testToolDescriptor("task_list"),
	})
}

func successfulSideEffectObservation(observationID string, toolName string, toolInput string, resultContent string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          toolName,
		ToolID:        "test:" + toolName,
		ToolInput:     json.RawMessage(toolInput),
		Output:        toolcontract.ToolOutput{Content: resultContent},
	}
}

func taskDeleteToolDefinition() toolcontract.ToolDefinition {
	definition := testToolDescriptor("task_delete")
	definition.Description = "Delete a task. Only the owner may."
	definition.SideEffectClass = toolcontract.ToolSideEffectStateChange
	definition.Completion = toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation}
	definition.OutputSchema = json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"],"additionalProperties":false}`)
	definition.ResultContract = &toolcontract.ToolResultContract{
		Schema:  definition.OutputSchema,
		Effects: []toolcontract.ResourceEffectContract{{ObjectType: "task", Effect: "deleted", ResultField: "taskID", EffectIdentity: "id"}},
	}
	return definition
}

func taskDeleteToolSet() *toolcontract.ToolSet {
	definition := taskDeleteToolDefinition()
	toolSet := toolcontract.NewToolSet([]string{definition.Name})
	toolSet.AllowTestReplacement()
	registerTestTool(toolSet, definition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		result := toolcontract.ToolSuccessData("deleted", json.RawMessage(`{"taskID":"task-1"}`))
		result.Effects = deletedTaskObservation().Effects
		return result, nil
	})
	return toolSet
}

func deletedTaskObservation() turnObservation {
	observation := successfulSideEffectObservation("obs-001", "task_delete", `{"taskID":"task-1"}`, "deleted")
	observation.Output.Data = json.RawMessage(`{"taskID":"task-1"}`)
	observation.Effects = []toolcontract.ResourceEffect{{ObjectType: "task", Effect: "deleted", ID: "task-1"}}
	return observation
}

func expectedChangesDocument(changes ...expectedChange) string {
	document, _ := json.Marshal(map[string]any{"expectedChanges": changes})
	return string(document)
}

func deleteRequest(toolSet *toolcontract.ToolSet) AgentTurnRequest {
	return AgentTurnRequest{
		Prompt:          "오래된 작업을 삭제해줘",
		ToolSet:         toolSet,
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_delete"}},
	}
}

var deleteOldTask = expectedChange{Change: "task deleted", Asked: "오래된 작업을 삭제해줘"}

func TestExpectedChangesAreNotAskedForWhenNoToolChangesAnything(t *testing.T) {
	languageModel := &stubStructuredLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	changes, isDefined := services.runner.defineExpectedChanges(context.Background(), "task-run-1", deleteRequest(taskToolsTestToolSet()))

	if !isDefined || len(changes) != 0 || len(languageModel.requests) != 0 {
		t.Fatalf("expected no definition call without a changing tool, got changes=%+v defined=%v requests=%d", changes, isDefined, len(languageModel.requests))
	}
}

func TestExpectedChangesOfferOnlyKindsTheToolsDeclare(t *testing.T) {
	languageModel := &stubStructuredLanguageModel{contents: []string{expectedChangesDocument(deleteOldTask, expectedChange{Change: "task created", Asked: "오래된 작업을"})}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	changes, _ := services.runner.defineExpectedChanges(context.Background(), "task-run-1", deleteRequest(taskDeleteToolSet()))

	schema := languageModel.requests[0].StructuredOutputSchema.Document
	if !strings.Contains(schema, `"enum":["task deleted"]`) {
		t.Fatalf("expected the kinds enum to hold only the declared effect, got %s", schema)
	}
	if !strings.Contains(languageModel.requests[0].Messages[0].Content, "task_delete: Delete a task.") {
		t.Fatalf("expected each kind to name its tools, got %s", languageModel.requests[0].Messages[0].Content)
	}
	if len(changes) != 1 || changes[0] != deleteOldTask {
		t.Fatalf("expected an undeclared kind to be dropped, got %+v", changes)
	}
}

func TestExpectedChangesAreRequotedOnceAndStillMisquotedOnesDropped(t *testing.T) {
	languageModel := &stubStructuredLanguageModel{contents: []string{
		expectedChangesDocument(expectedChange{Change: "task deleted", Asked: "오래된 작업 삭제"}),
		expectedChangesDocument(deleteOldTask, expectedChange{Change: "task deleted", Asked: "지난달 작업도 지워줘"}),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	changes, isDefined := services.runner.defineExpectedChanges(context.Background(), "task-run-1", deleteRequest(taskDeleteToolSet()))

	if len(languageModel.requests) != 2 || !strings.Contains(languageModel.requests[1].Messages[0].Content, "오래된 작업 삭제") {
		t.Fatalf("expected one retry naming the misquote, got %d requests", len(languageModel.requests))
	}
	if !isDefined || len(changes) != 1 || changes[0] != deleteOldTask {
		t.Fatalf("expected only the exactly quoted change to remain, got %+v", changes)
	}
}

func declaringToolDefinition(toolName string, objectType string, effect string) toolcontract.ToolDefinition {
	definition := testToolDescriptor(toolName)
	definition.ResultContract = &toolcontract.ToolResultContract{
		Schema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
		Effects: []toolcontract.ResourceEffectContract{{ObjectType: objectType, Effect: effect, ResultField: "id", EffectIdentity: "id"}},
	}
	return definition
}

func taskAndCalendarToolSet() *toolcontract.ToolSet {
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		taskDeleteToolDefinition(),
		declaringToolDefinition("task_add", "task", "created"),
		declaringToolDefinition("event_add", "calendar", "created"),
	})
}

func TestExpectedChangesQuoteTheLatestMessageAboutAnEarlierRequest(t *testing.T) {
	correction := expectedChange{Change: "task deleted", Asked: "아니 지우라고"}
	languageModel := &stubStructuredLanguageModel{contents: []string{expectedChangesDocument(correction)}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := deleteRequest(taskDeleteToolSet())
	request.ActiveGoal = ActiveGoal{OriginalInstruction: "오래된 작업 정리했어"}
	request.Prompt = "아니 지우라고"

	changes, _ := services.runner.defineExpectedChanges(context.Background(), "task-run-1", request)

	if !strings.Contains(languageModel.requests[0].Messages[1].Content, "Latest message about it:\n아니 지우라고") {
		t.Fatalf("expected the latest message beside the request, got %s", languageModel.requests[0].Messages[1].Content)
	}
	if len(changes) != 1 || changes[0] != correction {
		t.Fatalf("expected a quote from the latest message to stand, got %+v", changes)
	}
}

func TestExpectedChangeIsUnmetWithoutAskingJevWhenNothingOfItsRecordTypeChanged(t *testing.T) {
	decisionModel := &scriptedDecisionModel{}
	expected := []expectedChange{{Change: "calendar created", Asked: "회의도 잡아줘"}}

	check, errorValue := checkExpectedChanges(context.Background(), decisionModel, deleteRequest(taskAndCalendarToolSet()), expected, []turnObservation{deletedTaskObservation()})

	if errorValue != nil || len(check.Unmet) != 1 || len(check.Unrecorded) != 1 {
		t.Fatalf("expected the unrecorded change to be unmet, got %+v error=%v", check, errorValue)
	}
	if len(decisionModel.requests) != 0 {
		t.Fatalf("expected no Jev call when no record of that type changed, got %d", len(decisionModel.requests))
	}
}

func TestJevJudgesAChangeRecordedUnderAnotherKindOfTheSameRecordType(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.9}}
	expected := []expectedChange{{Change: "task created", Asked: "오래된 작업을 삭제해줘"}}

	check, _ := checkExpectedChanges(context.Background(), decisionModel, deleteRequest(taskAndCalendarToolSet()), expected, []turnObservation{deletedTaskObservation()})

	if len(decisionModel.requests) != 1 || len(check.Unmet) != 0 {
		t.Fatalf("expected Jev to judge a task change the definition named as another kind, got %+v", check)
	}
}

func TestRecordedExpectedChangeIsCarriedOutFromTheThresholdUp(t *testing.T) {
	for _, testCase := range []struct {
		noul      float64
		unmetSize int
	}{{changeCarriedOutThreshold, 0}, {changeCarriedOutThreshold - 0.01, 1}} {
		decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": testCase.noul}}

		check, _ := checkExpectedChanges(context.Background(), decisionModel, deleteRequest(taskDeleteToolSet()), []expectedChange{deleteOldTask}, []turnObservation{deletedTaskObservation()})

		if len(check.Unmet) != testCase.unmetSize {
			t.Fatalf("noul %v: expected %d unmet, got %+v", testCase.noul, testCase.unmetSize, check)
		}
	}
}

func TestJevSeesEachChangedRecordWithItsInputAndResult(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}

	checkExpectedChanges(context.Background(), decisionModel, deleteRequest(taskDeleteToolSet()), []expectedChange{deleteOldTask}, []turnObservation{deletedTaskObservation()})

	state, _ := json.Marshal(decisionModel.requests[0].State)
	if !strings.Contains(string(state), `"changedRecords":[{"record":"task-1","history":[{"change":"task deleted","input":{"taskID":"task-1"},"result":{"taskID":"task-1"}}]}]`) {
		t.Fatalf("expected the deleted task in changedRecords, got %s", state)
	}
}

func TestUnmetChangesMessageNamesWhatWasAskedAndWhy(t *testing.T) {
	created := expectedChange{Change: "task created", Asked: "새 작업도 만들어줘"}
	message := unmetChangesMessage(changeCheck{Unrecorded: []expectedChange{created}, Unmet: []expectedChange{created, deleteOldTask}})

	for _, want := range []string{`"새 작업도 만들어줘" (task created): nothing recorded changed this kind of record`, `"오래된 작업을 삭제해줘" (task deleted): the recorded changes do not carry it out`} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected %q in %q", want, message)
		}
	}
}

func TestExpectedChangesPassWhenNoDecisionModelIsConfigured(t *testing.T) {
	languageModel := &stubStructuredLanguageModel{contents: []string{expectedChangesDocument(deleteOldTask)}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "오래된 작업을 삭제해줘")

	result := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, deleteRequest(taskDeleteToolSet()), nil)

	if !result.IsSatisfied {
		t.Fatalf("expected an unconfigured check to leave the deterministic gate in charge, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), "completion.check_degraded", "decision model is not configured") {
		t.Fatal("expected the missing decision model to be recorded")
	}
}

func TestExpectedChangesCanAskForAFileTheReplyDelivers(t *testing.T) {
	fileDeliver := declaringToolDefinition(toolcontract.FileDeliverToolName, "file", "attached")
	fileDeliver.Visibility = toolcontract.ToolVisibilityInternal
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{fileDeliver, declaringToolDefinition("write", "file", "created")})

	vocabulary := changeVocabularyOf(toolSet)

	if !slices.Contains(vocabulary.Kinds, "file attached") || !slices.Contains(vocabulary.Kinds, "file created") {
		t.Fatalf("expected the reply's file delivery beside the model's own tools, got %v", vocabulary.Kinds)
	}
}
