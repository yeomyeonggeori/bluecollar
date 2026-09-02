package loop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type completionJudgeStubLanguageModel struct {
	response   model.StructuredResponse
	errorValue error
	requests   []model.StructuredResponseRequest
}

func (languageModel *completionJudgeStubLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *completionJudgeStubLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	return languageModel.response, languageModel.errorValue
}

func completionJudgeTestToolSet() *toolcontract.ToolSet {
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

func completionJudgeFinishActionDocument() turnActionDocument {
	goalSatisfied := true
	return turnActionDocument{Action: "finish", GoalStatus: "satisfied", GoalSatisfied: &goalSatisfied}
}

func completionJudgeFinishActionDocumentCiting(observationID string, toolName string) turnActionDocument {
	actionDocument := completionJudgeFinishActionDocument()
	actionDocument.CompletionEvidence = []completionEvidenceReference{{ObservationID: observationID, ToolName: toolName}}
	return actionDocument
}

func TestCompletionJudgeMessagesCarryTheFinishReplyAsDelivered(t *testing.T) {
	actionDocument := turnActionDocument{Action: "finish", Message: "deploy complete: https://sites.example/launch"}
	joined := joinedMessageContent(completionJudgeMessages(AgentTurnRequest{Prompt: "publish the site and give me the link"}, nil, nil, actionDocument, nil))
	if !strings.Contains(joined, "https://sites.example/launch") {
		t.Fatalf("expected the finish reply text in the judge prompt, got %s", joined)
	}
	if !strings.Contains(joined, "delivers to the user") || !strings.Contains(joined, "never require a separate send or delivery operation") {
		t.Fatalf("expected the delivered-reply framing in the judge prompt, got %s", joined)
	}
}

func TestOutcomeContractHasSideEffectEvidenceForRequiredEvidenceTools(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	contract := OutcomeContract{RequiredEvidenceTools: []string{"task_add"}}
	if !outcomeContractHasSideEffectEvidence(toolSet, contract) {
		t.Fatal("expected a state-changing required evidence tool to trigger the judge")
	}
}

func TestOutcomeContractHasSideEffectEvidenceForRequiredEvidenceAnyOf(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	contract := OutcomeContract{RequiredEvidenceAnyOf: [][]string{{"task_list"}, {"task_add"}}}
	if !outcomeContractHasSideEffectEvidence(toolSet, contract) {
		t.Fatal("expected a side-effect tool inside any RequiredEvidenceAnyOf group to trigger the judge")
	}
}

func TestOutcomeContractHasSideEffectEvidenceFalseForReadOnlyTools(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	contract := OutcomeContract{RequiredEvidenceTools: []string{"task_list"}}
	if outcomeContractHasSideEffectEvidence(toolSet, contract) {
		t.Fatal("expected a read-only required evidence tool to not trigger the judge")
	}
}

func TestCompletionJudgeLedgerIncludesSuccessfulReadsAndWrites(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	observations := []turnObservation{
		successfulSideEffectObservation("obs-001", "task_add", `{"title":"a"}`, "created"),
		successfulSideEffectObservation("obs-002", "task_list", `{}`, "listed"),
		{ObservationID: "obs-003", Tool: "task_add", ToolID: "test:task_add", Failure: &toolcontract.ToolFailure{}},
	}

	ledger := completionJudgeLedger(toolSet, observations, nil)

	if len(ledger) != 2 || ledger[0].Tool != "task_add" || ledger[1].Tool != "task_list" {
		t.Fatalf("expected successful reads and writes without the failed call, got %+v", ledger)
	}
}

func TestCompletionJudgeLedgerBudgetsBytesAndNamesDroppedEntries(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	observations := []turnObservation{}
	observationCount := completionJudgeLedgerByteBudget/completionJudgeInputMaxLength + 4
	for index := 0; index < observationCount; index++ {
		bulkyInput := `{"page":` + strconv.Itoa(index) + `,"filler":"` + strings.Repeat("x", completionJudgeInputMaxLength-40) + `"}`
		observations = append(observations, successfulSideEffectObservation("obs", "task_list", bulkyInput, "listed"))
	}

	ledger := completionJudgeLedger(toolSet, observations, nil)

	if len(ledger) >= observationCount {
		t.Fatalf("expected the byte budget to drop early entries, got %d of %d", len(ledger), observationCount)
	}
	if ledger[0].Tool != "earlier_operations" || !strings.Contains(ledger[0].Result, "dropped from this display") {
		t.Fatalf("expected a marker naming the dropped entries, got %+v", ledger[0])
	}
	if !strings.Contains(ledger[len(ledger)-1].Input, `{"page":`+strconv.Itoa(observationCount-1)) {
		t.Fatalf("expected the most recent observation to survive the budget, got %+v", ledger[len(ledger)-1])
	}
}

func TestCompletionJudgeLedgerTruncatesInputAndResult(t *testing.T) {
	longInput := strings.Repeat("a", completionJudgeInputMaxLength+50)
	longResult := strings.Repeat("b", completionJudgeResultMaxLength+50)
	toolSet := completionJudgeTestToolSet()
	observation := successfulSideEffectObservation("obs-001", "task_add", `{"note":"`+longInput+`"}`, longResult)

	ledger := completionJudgeLedger(toolSet, []turnObservation{observation}, nil)

	if len(ledger) != 1 {
		t.Fatalf("expected one ledger entry, got %+v", ledger)
	}
	if !strings.HasPrefix(ledger[0].Input, `{"note":"aaa`) || !strings.Contains(ledger[0].Input, "…[display truncated; full ") {
		t.Fatalf("expected truncated input with an explicit display marker, got %q", ledger[0].Input[len(ledger[0].Input)-80:])
	}
	if !strings.Contains(ledger[0].Result, "…[display truncated; full ") {
		t.Fatalf("expected truncated result with an explicit display marker, got %q", ledger[0].Result)
	}
}

func TestCompletionJudgeMessagesIncludeOriginalInstructionAndExpectedResults(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:          "fallback prompt",
		ActiveGoal:      ActiveGoal{OriginalInstruction: "add a task to check the missing quarterly settlement"},
		OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{{Type: ExpectedResultTypeMessage, Description: "completion check", Required: true}}},
	}

	messages := completionJudgeMessages(request, nil, nil, completionJudgeFinishActionDocument(), nil)
	joined := joinedMessageContent(messages)

	if !strings.Contains(joined, "add a task to check the missing quarterly settlement") {
		t.Fatalf("expected original instruction in judge prompt, got %s", joined)
	}
	if !strings.Contains(joined, "completion check") {
		t.Fatalf("expected expected-result description in judge prompt, got %s", joined)
	}
}

func TestCompletionJudgeMessagesIncludeTemporalContext(t *testing.T) {
	turnStartedAt := time.Date(2026, 7, 23, 1, 43, 0, 0, time.UTC)
	request := AgentTurnRequest{Prompt: "move tomorrow's schedule", TurnStartedAt: turnStartedAt}

	joined := joinedMessageContent(completionJudgeMessages(request, nil, nil, completionJudgeFinishActionDocument(), nil))

	if strings.Contains(joined, "Now: ") {
		t.Fatalf("the runtime does not know the clock here, and the judge must not be handed one, got %s", joined)
	}
}

func TestEvaluateCompletionJudgeFailsOpenOnProviderError(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{errorValue: errors.New("provider unavailable")}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "task", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-1", request, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied {
		t.Fatalf("expected fail-open satisfied result on provider error, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-1"), "completion_judge.degraded", "") {
		t.Fatal("expected a completion_judge.degraded event on provider error")
	}
}

func TestEvaluateCompletionJudgeFailsOpenOnMalformedContent(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: "not json"}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "task", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-2", request, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied {
		t.Fatalf("expected fail-open satisfied result on malformed judge content, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-2"), "completion_judge.degraded", "") {
		t.Fatal("expected a completion_judge.degraded event for malformed content")
	}
}

func TestEvaluateCompletionJudgeRecordsUnsatisfiedVerdictEvent(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate is missing"],"reason":"missing due date"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "task", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-3", request, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected an unsatisfied judge result")
	}
	if !strings.Contains(result.Message, "missing due date") || !strings.Contains(result.Message, "endDate is missing") {
		t.Fatalf("expected reason and missing work in the gate message, got %q", result.Message)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-3"), "completion_judge.verdict", `"satisfied":false`) {
		t.Fatal("expected a completion_judge.verdict event recording the unsatisfied verdict")
	}
}

func TestValidateCompletionGateWithJudgeSkipsJudgeWithoutSideEffectEvidence(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":[],"reason":"should not run"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "read only task",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_list"}},
	}
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task_list", `{}`, "listed")}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-4", request, nil, observations, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied {
		t.Fatalf("expected deterministic-only satisfied result for a read-only task, got %+v", result)
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected the judge to be skipped for a read-only outcome contract, got %d calls", len(languageModel.requests))
	}
}

func TestValidateCompletionGateWithJudgeSkipsJudgeWhenDeterministicGateFails(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":true,"missingWork":[],"reason":"unused"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "side effect task",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}},
	}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-5", request, nil, nil, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected the deterministic gate to fail without any task_add evidence")
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected the judge to be skipped once the deterministic gate already failed, got %d calls", len(languageModel.requests))
	}
}

func TestValidateCompletionGateWithJudgeReturnsJudgeUnsatisfied(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate is missing"],"reason":"missing due date"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "add a task to check the missing quarterly settlement, due July 24",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}},
	}
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task_add", `{"title":"missing quarterly settlement check"}`, "created")}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-6", request, nil, observations, nil, nil, completionJudgeFinishActionDocumentCiting("obs-001", "task_add"))

	if result.IsSatisfied {
		t.Fatal("expected the judge to reject a semantically incomplete finish")
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected exactly one judge call, got %d", len(languageModel.requests))
	}
}

func TestCompletionGateRunsJudgeFromLedgerWhenContractIsEmpty(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":["endDate is empty"],"reason":"no due date set"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "add task", ToolSet: completionJudgeTestToolSet()}
	observations := []turnObservation{successfulSideEffectObservation("obs-001", "task_add", `{"title":"t"}`, `{"endDate":""}`)}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-ledger", request, nil, observations, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected the ledger-triggered judge to reject the finish")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-ledger"), "completion_judge.verdict", `"satisfied":false`) {
		t.Fatal("expected a completion_judge.verdict event from the ledger-triggered judge")
	}
}

func TestAFinishThatAddedNothingGetsTheVerdictItAlreadyGot(t *testing.T) {
	rejection := completionGateObservation(3, completionGateResult{
		Message:          "Filter the contacts to those without an account before sending.",
		EvidenceKind:     evidenceKindExpectedResult,
		IsJudgeVerdict:   true,
		NamesMissingWork: true,
	}, nil, nil)
	observations := []turnObservation{
		newContentObservation("obs-001", "continue", toolcontract.ShellToolName, "sent to everyone"),
		rejection,
	}

	standing, isStanding := standingJudgeRejection(observations)

	if !isStanding {
		t.Fatal("re-finishing without touching the ledger is the same finish, and asking the judge again only lets repetition pass a gate that work did not")
	}
	if !strings.Contains(standing.Message, "without an account") {
		t.Fatalf("the agent has to be told the same thing it was told, got %q", standing.Message)
	}
}

func TestAFinishThatDidMoreWorkIsJudgedAgain(t *testing.T) {
	rejection := completionGateObservation(3, completionGateResult{
		Message:      "Filter the contacts to those without an account before sending.",
		EvidenceKind: evidenceKindExpectedResult,
	}, nil, nil)
	observations := []turnObservation{
		rejection,
		newContentObservation("obs-004", "continue", toolcontract.ShellToolName, "re-sent to the filtered list"),
	}

	if _, isStanding := standingJudgeRejection(observations); isStanding {
		t.Fatal("an agent that went and did the missing work has to get a fresh verdict on it")
	}
}

func TestTheResultAFinishCitesIsNotCutToThreeHundredBytes(t *testing.T) {
	toolSet := completionJudgeTestToolSet()
	longResult := strings.Repeat("b", completionJudgeResultMaxLength*4)
	observation := successfulSideEffectObservation("obs-001", "task_add", `{"note":"x"}`, longResult)

	uncited := completionJudgeLedger(toolSet, []turnObservation{observation}, nil)
	cited := completionJudgeLedger(toolSet, []turnObservation{observation}, map[string]bool{"obs-001": true})

	if len(uncited[0].Result) >= len(cited[0].Result) {
		t.Fatalf("the judge exists to read what the finish points at, got %d bytes cited against %d uncited", len(cited[0].Result), len(uncited[0].Result))
	}
	if strings.Contains(cited[0].Result, "display truncated") {
		t.Fatal("a cited result the judge cannot see whole is a fact it is asked to certify and cannot")
	}
}

func TestTheJudgeIsNotToldThatReplyingIsOneOfTheResults(t *testing.T) {
	results := []ExpectedResult{
		{ID: "send_reminders", Type: ExpectedResultTypeMessage, Description: "reminders sent to every roommate", Required: true},
		{ID: finalMessageExpectedResultID, Type: ExpectedResultTypeMessage, Description: "A final reply explaining the outcome of this task to the user", Required: true},
	}

	description := completionJudgeExpectedResultsDescription(results)

	if strings.Contains(description, finalMessageExpectedResultID) {
		t.Fatalf("a turn that did no work and explained why clearly satisfies this one, and it sits beside the results the instruction asks for: %s", description)
	}
	if !strings.Contains(description, "send_reminders") {
		t.Fatalf("the results the instruction does ask for still have to reach the judge: %s", description)
	}
}

func TestATaskWhoseOnlyResultIsTheReplyTellsTheJudgeNothing(t *testing.T) {
	results := []ExpectedResult{
		{ID: finalMessageExpectedResultID, Type: ExpectedResultTypeMessage, Description: "A final reply explaining the outcome of this task to the user", Required: true},
	}

	if description := completionJudgeExpectedResultsDescription(results); description != "" {
		t.Fatalf("with nothing else expected there is no result list worth sending, got %s", description)
	}
}

func TestATurnThatDidNothingStillGetsJudged(t *testing.T) {
	contract := OutcomeContract{ExpectedResults: []ExpectedResult{
		{ID: "send_reminders", Type: ExpectedResultTypeMessage, Description: "reminders sent", Required: true},
		{ID: finalMessageExpectedResultID, Type: ExpectedResultTypeMessage, Description: "a final reply", Required: true},
	}}

	if !contractAsksForSomethingToJudge(contract) {
		t.Fatal("asked to do something and having done nothing is the turn most worth judging, and no side effect is what it leaves behind")
	}
}

func TestATaskAskingOnlyForAReplyHasNothingToJudge(t *testing.T) {
	contract := OutcomeContract{ExpectedResults: []ExpectedResult{
		{ID: finalMessageExpectedResultID, Type: ExpectedResultTypeMessage, Description: "a final reply", Required: true},
	}}

	if contractAsksForSomethingToJudge(contract) {
		t.Fatal("delivering the reply is asked of every task and is not something to grade the work against")
	}
}

func TestTheJudgeSeesWhatDidNotWork(t *testing.T) {
	failed := turnObservation{
		ObservationID: "obs-004",
		Action:        "continue",
		Tool:          "shell",
		ToolInput:     json.RawMessage(`{"command":"cli venmo send_money 91"}`),
		Failure: &toolcontract.ToolFailure{
			Kind: toolcontract.FailureUnknown, Code: toolcontract.FailureCodes.OperationFailed.String(),
			Stage: "shell", UserSafeSummary: "the command exited 1",
		},
	}
	failed.Output.Content = "insufficient balance"

	document := completionJudgeLedgerDocument(nil, []turnObservation{failed}, nil)

	if !strings.Contains(document, "insufficient balance") {
		t.Fatalf("a run that tried and failed looks identical to one that never tried when this is filtered out: %s", document)
	}
	if !strings.Contains(document, `"failed":true`) {
		t.Fatalf("the judge has to be able to tell an attempt from an accomplishment: %s", document)
	}
}

func TestTheJudgeSeesBothEndsOfATruncatedResult(t *testing.T) {
	echoedCall := "Calling:\nprint(apis.example.search_users(**{'access_token': '" + strings.Repeat("x", 220) + "', 'query': 'Sam Example'}))"
	dataRows := `[{"first_name": "Sam", "last_name": "Example", "email": "sam@example.com", "registered_at": "2022-07-03"}]`
	observation := newContentObservation("obs-003", "continue", toolcontract.ShellToolName, echoedCall+"\n"+dataRows)

	ledger := completionJudgeLedger(nil, []turnObservation{observation}, map[string]bool{})

	if len(ledger) != 1 {
		t.Fatalf("expected one entry, got %d", len(ledger))
	}
	if !strings.Contains(ledger[0].Result, "registered_at") {
		t.Fatalf("the echoed call fills the head of every result, so a head-only cut shows the judge no data at all and it certifies from what it cannot see: %q", ledger[0].Result)
	}
}

func TestDroppedOperationsAreNotEvidence(t *testing.T) {
	ledger := []completionLedgerEntry{}
	for index := 0; index < 40; index++ {
		ledger = append(ledger, completionLedgerEntry{Tool: "shell", Input: strings.Repeat("a", 900), Result: strings.Repeat("b", 900)})
	}

	bounded := newestLedgerEntriesWithinBudget(ledger, 24000)

	marker := bounded[0]
	if marker.Tool != "earlier_operations" {
		t.Fatalf("expected the dropped-prefix marker first, got %+v", marker)
	}
	if !strings.Contains(marker.Result, "unknown in both directions") {
		t.Fatalf("a judge told only that operations were recorded certifies them as done, which is how a run that never sent the money was passed: %q", marker.Result)
	}
	if !strings.Contains(marker.Result, "shell x") {
		t.Fatalf("the tool tally is the one deterministic fact the dropped prefix can still state: %q", marker.Result)
	}
}

type askingJudgeLanguageModel struct {
	responses []model.StructuredResponse
	requests  []model.StructuredResponseRequest
}

func (languageModel *askingJudgeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *askingJudgeLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	return languageModel.responses[len(languageModel.requests)-1], nil
}

func TestTheJudgeAsksForWhatItCannotSeeAndDecidesFromTheFullEntry(t *testing.T) {
	languageModel := &askingJudgeLanguageModel{responses: []model.StructuredResponse{
		{Content: `{"satisfied":true,"missingWork":[],"reason":"cannot see the search rows","needObservationIDs":["obs-002"]}`},
		{Content: `{"satisfied":false,"missingWork":["the account holder was messaged despite being registered"],"reason":"the full entry shows a registered account","needObservationIDs":[]}`},
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	echoedCall := "Calling: search_users(access_token=" + strings.Repeat("x", 260) + ")"
	observation := newContentObservation("obs-002", "continue", toolcontract.ShellToolName, echoedCall+`[{"name":"Sam Example","registered_at":"2022-07-03"}]`)
	request := AgentTurnRequest{Prompt: "message only the relatives without an account", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-ask", request, []turnObservation{observation}, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("the first verdict was a guess over invisible rows, and the runtime handed the judge the rows it asked for exactly so that the guess would not stand")
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected one expansion pass, got %d calls", len(languageModel.requests))
	}
	secondLedger := languageModel.requests[1].Messages[len(languageModel.requests[1].Messages)-1].Content
	if !strings.Contains(secondLedger, "registered_at") {
		t.Fatalf("the requested entry must reach the second pass in full: %s", secondLedger[:200])
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-judge-ask"), "completion_judge.expanded", "obs-002") {
		t.Fatal("the expansion is a routing decision and belongs in the ledger")
	}
}

func TestAJudgeAskingForUnknownIDsGetsNoSecondPass(t *testing.T) {
	languageModel := &askingJudgeLanguageModel{responses: []model.StructuredResponse{
		{Content: `{"satisfied":true,"missingWork":[],"reason":"done","needObservationIDs":["obs-404"]}`},
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "task", OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}}}

	result := services.runner.evaluateCompletionJudge(context.Background(), "task-judge-unknown", request, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied || len(languageModel.requests) != 1 {
		t.Fatalf("an ID that matches no recorded observation resolves to nothing, and a second pass over the same display would be the same guess: calls=%d result=%+v", len(languageModel.requests), result)
	}
}

func TestTheJudgeSeesItsOwnPriorRejections(t *testing.T) {
	rejection := completionGateObservation(3, completionGateResult{
		Message:      "the venmo filter was never applied to the recipients",
		EvidenceKind: evidenceKindExpectedResult,
	}, nil, nil)
	messages := completionJudgeMessages(AgentTurnRequest{Prompt: "message the relatives without venmo"}, []turnObservation{rejection}, nil, completionJudgeFinishActionDocument(), nil)

	joined := joinedMessageContent(messages)
	if !strings.Contains(joined, "venmo filter was never applied") {
		t.Fatalf("each judge call was memoryless, so an agent re-finishing the same state farmed fresh samples until one passed — verdict runs of seven rejections then one approval: %s", joined[:300])
	}
	if !strings.Contains(joined, "not permanent vetoes") {
		t.Fatalf("a lone must-check makes a weak judge treat old reasons as forever: %s", joined[:200])
	}
}

func TestAFirstFinishCarriesNoRejectionContext(t *testing.T) {
	messages := completionJudgeMessages(AgentTurnRequest{Prompt: "task"}, nil, nil, completionJudgeFinishActionDocument(), nil)

	if strings.Contains(joinedMessageContent(messages), "Earlier finishes of this task were rejected") {
		t.Fatal("nothing was rejected and nothing should be claimed")
	}
}

func TestARejectionNamingNoMissingWorkDoesNotStand(t *testing.T) {
	listlessRejection := completionGateObservation(0, completionGateResult{
		Message:        "the ledger does not show the requested result",
		EvidenceKind:   evidenceKindExpectedResult,
		IsJudgeVerdict: true,
	}, nil, nil)

	if _, isStanding := standingJudgeRejection([]turnObservation{listlessRejection}); isStanding {
		t.Fatal("a verdict that rejects while naming nothing to do is one bad sample, and remembering it turns that sample into a permanent veto")
	}

	listedRejection := completionGateObservation(0, completionGateResult{
		Message:          "the summary file was never written",
		EvidenceKind:     evidenceKindExpectedResult,
		IsJudgeVerdict:   true,
		NamesMissingWork: true,
	}, nil, nil)

	if _, isStanding := standingJudgeRejection([]turnObservation{listedRejection}); !isStanding {
		t.Fatal("a rejection that names the missing work stands until the ledger changes, or the judge can be farmed by re-finishing")
	}
}

// The gate cannot refuse on a hint, so the reading that catches a false claim on
// a hinted-write task is the judge's. Without this trigger the claim ships.
func TestCompletionJudgeRunsWhenTheContractOnlyHintsAStateChangeTool(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":["no task_add operation is recorded"],"reason":"the reply says the task was added and the ledger carries no such operation"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "월요일까지 신규 가입 플로우 점검 작업 추가해줘",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{SelectedEvidenceHints: []string{"task_list", "task_add"}},
	}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-hinted-write", request, nil, nil, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("expected the judge to reject a claim the empty ledger does not carry")
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected exactly one judge call, got %d", len(languageModel.requests))
	}
}

func TestCompletionJudgeStaysSkippedWhenTheContractOnlyHintsAReadTool(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":["unused"],"reason":"unused"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "월요일 일정 뭐 있어?",
		ToolSet:         completionJudgeTestToolSet(),
		OutcomeContract: OutcomeContract{SelectedEvidenceHints: []string{"task_list"}},
	}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-hinted-read", request, nil, nil, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied {
		t.Fatalf("expected an answer with nothing to change to finish, got %+v", result)
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected no judge call when nothing in the contract can have changed, got %d", len(languageModel.requests))
	}
}

func inputImageAgentPart() AgentPart {
	return AgentPart{
		Type: AgentPartTypeImage,
		Image: &AgentImagePart{
			MimeType:   "image/png",
			Filename:   "sent.png",
			DataBase64: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
		},
	}
}

func TestTheJudgeIsShownThePictureTheMessageCameWith(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:     "이 그림에 적힌 글자를 그대로 알려줘",
		ToolSet:    completionJudgeTestToolSet(),
		InputParts: []AgentPart{TextAgentPart("이 그림에 적힌 글자를 그대로 알려줘"), inputImageAgentPart()},
	}

	messages := completionJudgeMessages(request, nil, nil, completionJudgeFinishActionDocument(), nil)

	imagePartCount := 0
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Type == "image" && strings.TrimSpace(part.DataBase64) != "" {
				imagePartCount++
			}
		}
	}
	if imagePartCount != 1 {
		t.Fatalf("the judge grades a claim about a picture it was never shown: %d image parts", imagePartCount)
	}
}

func TestAReplyDescribingAPictureNobodyOpenedIsJudged(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":["the picture holds no letters"],"reason":"the reply describes a black rectangle the image does not show"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:     "이 그림에 적힌 글자를 그대로 알려줘",
		ToolSet:    completionJudgeTestToolSet(),
		InputParts: []AgentPart{inputImageAgentPart()},
	}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-input-image", request, nil, nil, nil, nil, completionJudgeFinishActionDocument())

	if result.IsSatisfied {
		t.Fatal("a finish describing what an unopened picture shows was accepted on its own say-so")
	}
	if result.EvidenceKind != evidenceKindExpectedResult {
		t.Fatalf("expected the judge's expected-result refusal, got %q", result.EvidenceKind)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected exactly one judge call, got %d", len(languageModel.requests))
	}
}

func TestATurnWithNoPictureAndNothingToJudgeStillSkipsTheJudge(t *testing.T) {
	languageModel := &completionJudgeStubLanguageModel{response: model.StructuredResponse{Content: `{"satisfied":false,"missingWork":[],"reason":"should not run"}`}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := AgentTurnRequest{Prompt: "안녕하세요", ToolSet: completionJudgeTestToolSet()}

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-no-image", request, nil, nil, nil, nil, completionJudgeFinishActionDocument())

	if !result.IsSatisfied {
		t.Fatalf("a greeting with nothing to grade was refused: %+v", result)
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected no judge call, got %d", len(languageModel.requests))
	}
}
