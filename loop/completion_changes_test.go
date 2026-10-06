package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestJevJudgesAChangeWithNoLookupAndNoRecordedChangeAndALowVerdictLeavesItUnmet(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}}
	expected := []expectedChange{{Change: "calendar created", Asked: "회의도 잡아줘"}}

	check, errorValue := checkExpectedChanges(context.Background(), decisionModel, deleteRequest(taskAndCalendarToolSet()), expected, []turnObservation{deletedTaskObservation()})

	if len(decisionModel.requests) != 1 {
		t.Fatalf("expected Jev asked about a change nothing recorded or looked up, got %d calls", len(decisionModel.requests))
	}
	if errorValue != nil || len(check.Unmet) != 1 || len(check.Unrecorded) != 1 {
		t.Fatalf("expected the unrecorded change to be unmet, got %+v error=%v", check, errorValue)
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

func kernelFileToolSet() *toolcontract.ToolSet {
	write := declaringPathToolDefinition("write", "Overwrite one UTF-8 text file under the Blueclaw workspace.", "file", "created")
	edit := declaringPathToolDefinition("edit", "Apply one or more exact text replacements to workspace files as one atomic edit.", "file", "updated")
	fileDelete := declaringPathToolDefinition("file_delete", "Delete one file from the Blueclaw workspace by its path.", "file", "deleted")
	fileDeliver := declaringPathToolDefinition(toolcontract.FileDeliverToolName, "Deliver one or more existing workspace files as final reply evidence.", "file", "attached")
	fileDeliver.Visibility = toolcontract.ToolVisibilityInternal
	bash := testToolDescriptor(toolcontract.BashToolName)
	bash.Description = "Run a shell command as the requester."
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{write, edit, fileDelete, fileDeliver, bash})
}

func declaringPathToolDefinition(toolName string, description string, objectType string, effect string) toolcontract.ToolDefinition {
	definition := testToolDescriptor(toolName)
	definition.Description = description
	definition.ResultContract = &toolcontract.ToolResultContract{
		Schema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		Effects: []toolcontract.ResourceEffectContract{{ObjectType: objectType, Effect: effect, ResultField: "path", EffectIdentity: "path"}},
	}
	return definition
}

func fileMadeByCommandThenDelivered(command string, filename string, contentType string) []turnObservation {
	devicePath := "/home/bc_person_sample/documents/" + filename
	commandInput, _ := json.Marshal(map[string]string{"command": command})
	deliverInput, _ := json.Marshal(map[string]string{"path": "~/documents/" + filename})
	deliverData, _ := json.Marshal(map[string]any{"deliveredPaths": []string{devicePath}, "attachmentCount": 1})
	return []turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          toolcontract.BashToolName,
			ToolInput:     commandInput,
			Output:        toolcontract.ToolOutput{Content: `{"completed":true,"exitCode":0}`, Data: json.RawMessage(`{"completed":true,"exitCode":0,"stdout":"","stderr":""}`)},
		},
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          toolcontract.FileDeliverToolName,
			ToolInput:     deliverInput,
			Output:        toolcontract.ToolOutput{Content: "files delivered", Data: deliverData},
			Effects:       []toolcontract.ResourceEffect{{ObjectType: "file", Effect: "attached", Path: devicePath}},
			Attachments:   []toolcontract.FileAttachment{{Filename: filename, ContentType: contentType, SizeBytes: 1342, DevicePath: devicePath}},
		},
	}
}

const pdfCommand = `python3 make_pdf.py 'native install rig 7d9a1a5a' ~/documents/native-install-rig.pdf`

var makeTheAskedPDF = expectedChange{Change: "file created", Asked: "Make a one-page PDF whose only line reads 'native install rig 7d9a1a5a'"}

func TestJevSeesTheCommandThatMadeAFileAndTheFileItDelivered(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}
	request := AgentTurnRequest{Prompt: makeTheAskedPDF.Asked + ", and send me the PDF file itself.", ToolSet: kernelFileToolSet()}

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{makeTheAskedPDF}, fileMadeByCommandThenDelivered(pdfCommand, "native-install-rig.pdf", "application/pdf"))

	state, _ := json.Marshal(decisionModel.requests[0].State)
	commandInput, _ := json.Marshal(map[string]string{"command": pdfCommand})
	for _, want := range []string{
		`"unrecordedWork":[{"tool":"bash","input":` + string(commandInput),
		`"file":{"filename":"native-install-rig.pdf","contentType":"application/pdf","sizeBytes":1342}`,
	} {
		if !strings.Contains(string(state), want) {
			t.Fatalf("expected %s in the change check state, got %s", want, state)
		}
	}
}

func TestUnrecordedWorkHoldsNeitherLookupsNorRecordedChanges(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{taskDeleteToolDefinition(), testToolDescriptor("task_list")})
	lookup := successfulSideEffectObservation("obs-002", "task_list", `{}`, "[]")

	work := unrecordedWork(toolSet, []turnObservation{deletedTaskObservation(), lookup}, time.UTC)

	if len(work) != 0 {
		t.Fatalf("a lookup and a recorded change are not unrecorded work, got %+v", work)
	}
}

type recordedDelivery struct {
	Prompt       string            `json:"prompt"`
	Observations []turnObservation `json:"observations"`
	Holds        json.RawMessage   `json:"holds"`
}

func w1DeliveredWorkbook(t *testing.T) recordedDelivery {
	document, errorValue := os.ReadFile("testdata/w1-run1-delivered-workbook.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var delivery recordedDelivery
	if errorValue := json.Unmarshal(document, &delivery); errorValue != nil {
		t.Fatal(errorValue)
	}
	return delivery
}

func commandFileToolSet() *toolcontract.ToolSet {
	definitions := []toolcontract.ToolDefinition{}
	for _, name := range []string{"write", "edit", "file_delete", toolcontract.FileDeliverToolName} {
		definition, _ := kernelFileToolSet().ToolDefinition(name)
		definitions = append(definitions, definition)
	}
	bash := declaringPathToolDefinition(toolcontract.BashToolName, "Run a shell command as the requester.", "file", "changed")
	bash.SideEffectClass = toolcontract.ToolSideEffectStateChange
	return newTestToolSetWithDefinitions(append(definitions, bash))
}

func TestJevSeesWhatADeliveredWorkbookHoldsAsItsWriterRecordedIt(t *testing.T) {
	delivery := w1DeliveredWorkbook(t)
	delivered := &delivery.Observations[len(delivery.Observations)-1]
	delivered.Attachments[0].Holds = delivery.Holds
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}
	request := AgentTurnRequest{Prompt: delivery.Prompt, ToolSet: commandFileToolSet()}
	asked := expectedChange{Change: "file changed", Asked: strings.SplitN(delivery.Prompt, "\n", 2)[0]}

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{asked}, delivery.Observations)

	state, _ := json.Marshal(decisionModel.requests[0].State)
	if !strings.Contains(string(state), `"holds":`+string(compactJSON(t, delivery.Holds))) {
		t.Fatalf("expected the delivered workbook's recorded holds in the change check state, got %s", state)
	}
	instructions := decisionModel.requests[0].Questions["expected0"].Instructions
	if !strings.Contains(instructions, "holds") {
		t.Fatalf("expected the question to say how to read holds, got %s", instructions)
	}
}

func TestTheQuestionSaysNothingOfHoldsWhenNoFileRecordsThem(t *testing.T) {
	delivery := w1DeliveredWorkbook(t)
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}
	request := AgentTurnRequest{Prompt: delivery.Prompt, ToolSet: commandFileToolSet()}
	asked := expectedChange{Change: "file changed", Asked: strings.SplitN(delivery.Prompt, "\n", 2)[0]}

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{asked}, delivery.Observations)

	if instructions := decisionModel.requests[0].Questions["expected0"].Instructions; strings.Contains(instructions, "holds") {
		t.Fatalf("expected no word about holds when no file records them, got %s", instructions)
	}
}

func compactJSON(t *testing.T, document json.RawMessage) []byte {
	var buffer bytes.Buffer
	if errorValue := json.Compact(&buffer, document); errorValue != nil {
		t.Fatal(errorValue)
	}
	return buffer.Bytes()
}

func TestOneCallThatChangedSeveralRecordsShowsItsInputAndResultOnce(t *testing.T) {
	delivery := w1DeliveredWorkbook(t)
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}
	request := AgentTurnRequest{Prompt: delivery.Prompt, ToolSet: commandFileToolSet()}
	asked := expectedChange{Change: "file changed", Asked: strings.SplitN(delivery.Prompt, "\n", 2)[0]}

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{asked}, delivery.Observations)

	state, _ := json.Marshal(decisionModel.requests[0].State)
	if count := strings.Count(string(state), `office render documents/`); count != 1 {
		t.Fatalf("expected the render command shown once for the seven previews it wrote, got %d times in %s", count, state)
	}
	if !strings.Contains(string(state), `{"record":"documents/지역별_분기_매출_집계.xlsx.source.json","history":[{"change":"file changed","sameCallAs":"documents/지역별_분기_매출_집계.xlsx"}]}`) {
		t.Fatalf("expected the snapshot's step to point at the record showing the call, got %s", state)
	}
}

type deliveredOfficeFileCase struct {
	Name         string          `json:"name"`
	Prompt       string          `json:"prompt"`
	Command      string          `json:"command"`
	Output       string          `json:"output"`
	Filename     string          `json:"filename"`
	ContentType  string          `json:"contentType"`
	SizeBytes    int64           `json:"sizeBytes"`
	Holds        json.RawMessage `json:"holds"`
	IsCarriedOut bool            `json:"isCarriedOut"`
}

func deliveredOfficeFileCases(t *testing.T) []deliveredOfficeFileCase {
	document, errorValue := os.ReadFile("testdata/change-check-delivered-office-files.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var cases []deliveredOfficeFileCase
	if errorValue := json.Unmarshal(document, &cases); errorValue != nil || len(cases) == 0 {
		t.Fatalf("no cases in testdata/change-check-delivered-office-files.json: %v", errorValue)
	}
	return cases
}

func deliveredOfficeFileTurn(liveCase deliveredOfficeFileCase) (AgentTurnRequest, []expectedChange, []turnObservation) {
	devicePath := "/home/bc_person_sample/documents/" + liveCase.Filename
	commandInput, _ := json.Marshal(map[string]string{"command": liveCase.Command})
	commandData, _ := json.Marshal(map[string]any{"completed": true, "exitCode": 0, "output": liveCase.Output})
	deliverInput, _ := json.Marshal(map[string]string{"path": "~/documents/" + liveCase.Filename})
	deliverData, _ := json.Marshal(map[string]any{"deliveredPaths": []string{devicePath}, "attachmentCount": 1})
	observations := []turnObservation{
		{ObservationID: "obs-001", Action: "continue", Tool: toolcontract.BashToolName, ToolInput: commandInput, Output: toolcontract.ToolOutput{Content: liveCase.Output, Data: commandData}},
		{
			ObservationID: "obs-002", Action: "continue", Tool: toolcontract.FileDeliverToolName, ToolInput: deliverInput,
			Output:      toolcontract.ToolOutput{Content: "files delivered", Data: deliverData},
			Effects:     []toolcontract.ResourceEffect{{ObjectType: "file", Effect: "attached", Path: devicePath}},
			Attachments: []toolcontract.FileAttachment{{DevicePath: devicePath, Filename: liveCase.Filename, ContentType: liveCase.ContentType, SizeBytes: liveCase.SizeBytes, Holds: liveCase.Holds}},
		},
	}
	request := AgentTurnRequest{Prompt: liveCase.Prompt, ToolSet: kernelFileToolSet(), EnvironmentNow: time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)}
	expected := []expectedChange{{Change: "file attached", Asked: strings.SplitN(liveCase.Prompt, "\n", 2)[0]}}
	return request, expected, observations
}

func TestJevSeesAHeldFileByANeutralReferenceAndItsHoldsAlone(t *testing.T) {
	for _, liveCase := range deliveredOfficeFileCases(t) {
		request, expected, observations := deliveredOfficeFileTurn(liveCase)
		decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}

		checkExpectedChanges(context.Background(), decisionModel, request, expected, observations)

		state, _ := json.Marshal(decisionModel.requests[0].State)
		stem := strings.TrimSuffix(liveCase.Filename, filepath.Ext(liveCase.Filename))
		if strings.Contains(string(state), stem) || strings.Contains(string(state), "documents/") {
			t.Fatalf("%s: expected nothing naming the held file in the change check state, got %s", liveCase.Name, state)
		}
		if !strings.Contains(string(state), `{"record":"file 1","history":[{"change":"file attached","file":{"contentType":"`+liveCase.ContentType+`","sizeBytes":`) || !strings.Contains(string(state), `"holds":`+string(compactJSON(t, liveCase.Holds))) {
			t.Fatalf("%s: expected the held file as file 1 with its holds, got %s", liveCase.Name, state)
		}
	}
}

func TestARecordedWorkbookWithHoldsShowsNoneOfTheRecordsThatMadeIt(t *testing.T) {
	delivery := w1DeliveredWorkbook(t)
	delivered := &delivery.Observations[len(delivery.Observations)-1]
	delivered.Attachments[0].Holds = delivery.Holds
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}
	request := AgentTurnRequest{Prompt: delivery.Prompt, ToolSet: commandFileToolSet()}
	asked := expectedChange{Change: "file changed", Asked: strings.SplitN(delivery.Prompt, "\n", 2)[0]}

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{asked}, delivery.Observations)

	state, _ := decisionModel.requests[0].State.(map[string]any)
	records, _ := json.Marshal(state["changedRecords"])
	if strings.Contains(string(records), "지역별_분기_매출_집계") || strings.Count(string(records), `"record":`) != 1 {
		t.Fatalf("expected only the delivered workbook, by a neutral reference, got %s", records)
	}
}

func TestARecordRepeatingAnIdenticalStepShowsItOnce(t *testing.T) {
	first := deletedTaskObservation()
	second := deletedTaskObservation()
	second.ObservationID = "obs-002"

	records := changedRecords([]turnObservation{first, second}, time.UTC, map[string]bool{})

	if len(records) != 1 || len(records[0].History) != 1 {
		t.Fatalf("expected the repeated identical deletion shown once, got %+v", records)
	}
}
