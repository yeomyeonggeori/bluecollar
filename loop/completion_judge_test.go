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

func TestCompletionJudgeMessagesCarryTheFinishReplyAsDelivered(t *testing.T) {
	actionDocument := turnActionDocument{Action: "finish", Message: "deploy complete: https://sites.example/launch"}
	joined := joinedMessageContent(completionJudgeMessages(AgentTurnRequest{Prompt: "publish the site and give me the link"}, nil, nil, actionDocument))
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
	if ledger[0].Tool != "earlier_operations" || !strings.Contains(ledger[0].Result, "not shown here") {
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

	messages := completionJudgeMessages(request, nil, nil, completionJudgeFinishActionDocument())
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

	joined := joinedMessageContent(completionJudgeMessages(request, nil, nil, completionJudgeFinishActionDocument()))

	if !strings.Contains(joined, "Runtime temporal context:") {
		t.Fatalf("expected temporal context in judge prompt, got %s", joined)
	}
	if !strings.Contains(joined, buildTemporalContextDescription(turnStartedAt)) {
		t.Fatalf("expected turn-start temporal description in judge prompt, got %s", joined)
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

	result := services.runner.validateCompletionGateWithJudge(context.Background(), "task-judge-6", request, nil, observations, nil, nil, completionJudgeFinishActionDocument())

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
		Message:        "Filter the contacts to those without an account before sending.",
		EvidenceKind:   evidenceKindExpectedResult,
		IsJudgeVerdict: true,
	}, nil)
	observations := []turnObservation{
		newContentObservation("obs-001", "continue", toolcontract.TerminalRunToolName, "sent to everyone"),
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
	}, nil)
	observations := []turnObservation{
		rejection,
		newContentObservation("obs-004", "continue", toolcontract.TerminalRunToolName, "re-sent to the filtered list"),
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
