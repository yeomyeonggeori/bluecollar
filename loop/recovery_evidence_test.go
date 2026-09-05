package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestRecoveryPermissionDoesNotRequireSpendingTheBudget(t *testing.T) {
	guidance := failureDebtActionContractMessage(failureReportFacts{BudgetState: "recovery_available"})
	if !strings.Contains(guidance, "Budget is a ceiling") || !strings.Contains(guidance, "no evidence-backed recovery") {
		t.Fatalf("the model must be allowed to stop a hopeless recovery immediately: %s", guidance)
	}
}

func TestNativeFailureActionExistsWhileRecoveryBudgetRemains(t *testing.T) {
	state := nativeAgentActionTestState()
	state.Options.MaxIterationCount = 100
	state.Options.MaxToolCallCount = 100
	state.Observations = []turnObservation{newFailureObservation("obs-001", "continue", "record_create", "server repair required", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "result_validation")}
	state.Observations[0].ToolInputKey = canonicalToolCallKey("record_create", json.RawMessage(`{}`))
	request := nativeAgentActionRequestFor(t, state)
	if !containsNativeAgentTool(request.Tools, "fail") {
		t.Fatal("recovery guidance permits stopping but the generated native schema has no fail action")
	}
}

func TestRecoveryGuidanceDoesNotInventAnExecutedToolCall(t *testing.T) {
	observed := newFailureObservation("obs-001", "continue", "record_create", "server repair required", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "result_validation")
	guidance := observed
	guidance.ObservationID = "obs-002"
	guidance.Action = "recovery_guidance"
	guidance.Output.Content = "Inspect the recorded failure."
	transcript := toolCallTranscript([]turnObservation{observed, guidance})
	callCount, hasGuidance := 0, false
	for _, message := range transcript {
		callCount += len(message.ToolCalls)
		hasGuidance = hasGuidance || message.Role == "system" && strings.Contains(message.Content, guidance.Output.Content)
	}
	if callCount != 1 || !hasGuidance {
		t.Fatalf("guidance must survive as runtime guidance, not an invented call: %+v", transcript)
	}
	progress := compactProgressObservations([]turnObservation{observed, guidance})
	if len(progress) != 1 || progress[0].ObservationID != observed.ObservationID {
		t.Fatalf("runtime guidance became a second progress step: %+v", progress)
	}
	if summary := buildFailureObservationSummary([]turnObservation{observed, guidance}); strings.Count(summary, "record_create failed") != 1 {
		t.Fatalf("runtime guidance became a second reported failure: %s", summary)
	}
}

func TestEarlyFailureReportDoesNotSpendRemainingRecoveryCalls(t *testing.T) {
	failureDocument := failureReportDocument("The server must be repaired by its operator.", "record_create", "{}", "operation_failed", "result_validation", "server result contract failed")
	var failureAction map[string]json.RawMessage
	if errorValue := json.Unmarshal([]byte(failureDocument), &failureAction); errorValue != nil {
		t.Fatal(errorValue)
	}
	failureAction["message"] = json.RawMessage(`"The server must be repaired by its operator."`)
	document, errorValue := json.Marshal(failureAction)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"record_create","toolInput":{}}`,
		string(document),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	tools := newTestCapabilityToolSet([]string{"record_create"})
	callCount := 0
	registerTestTool(tools, toolcontract.ToolDefinition{Name: "record_create"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		callCount++
		return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "result_validation", "server result contract failed"), nil
	})
	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{RequesterPersonID: "person-1", ConversationID: "conversation-1", Prompt: "Create a record", ToolSet: tools, PinnedToolNames: tools.ListToolNames()})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusFailed || callCount != 1 {
		t.Fatalf("early failure must stop tool calls: status=%s calls=%d", result.TaskRun.Status, callCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_budget_exhausted", "") {
		t.Fatal("the model must not need to exhaust recovery before stopping")
	}
	if len(languageModel.requests) != 2 || len(languageModel.textPrompts) != 0 {
		t.Fatalf("a validated final failure reply triggered more generation: structured=%d text=%d", len(languageModel.requests), len(languageModel.textPrompts))
	}
}

func TestPriorFailureReportIsNotPromotedToKnownFacts(t *testing.T) {
	context := priorTaskKnownContext(PriorTaskContext{TaskRunID: "previous", Status: "failed", FailureReason: "A deadline is required", Result: "The record was never saved"})
	joined := strings.Join(context, "\n")
	if strings.Contains(joined, "A deadline is required") || strings.Contains(joined, "The record was never saved") {
		t.Fatalf("unverified assistant reports became current facts: %s", joined)
	}
}

func TestNonRetryableFailureDoesNotRequestAnInputChange(t *testing.T) {
	observation := newFailureObservation("obs-001", "continue", "record_create", "response contract failed", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "result_validation")
	observation.Failure.RetryPolicy = toolcontract.RetryPolicyDoNotRetry
	packet := buildRecoveryPacket(observation)
	if packet.RetryPolicy != toolcontract.RetryPolicyDoNotRetry {
		t.Fatalf("retry policy = %s", packet.RetryPolicy)
	}
	for _, instruction := range packet.MustDoNext {
		if strings.Contains(instruction, "Change tool input") {
			t.Fatalf("an input change cannot repair a non-retryable response: %s", instruction)
		}
	}
	if !strings.Contains(strings.Join(packet.MustDoNext, " "), "independent route") {
		t.Fatalf("a failed route must still allow recovery elsewhere: %+v", packet)
	}
}

func TestRecoveryUsesTheCurrentInterpretationOfTheOutcome(t *testing.T) {
	request := AgentRequest{Prompt: "Retry with the corrected title", PriorTask: PriorTaskContext{
		TaskRunID: "previous", Prompt: "Create the original record",
		OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{{ID: "old", Type: ExpectedResultTypeMessage, Description: "Original title", Required: true}}},
	}}
	decision := IntakeDecision{PriorTaskReference: PriorTaskReferenceOutcomeRecovery, ExpectedResults: []ExpectedResult{{ID: "current", Type: ExpectedResultTypeMessage, Description: "Corrected title", Required: true}}}
	recovered, currentDecision := applyPriorTaskOutcomeRecovery(request, decision)
	contract := outcomeContractForRequest(recovered, currentDecision, InstructionBundle{}, ExecutionPlan{}, false, nil)
	if len(contract.ExpectedResults) != 1 || contract.ExpectedResults[0].ID != "current" {
		t.Fatalf("the previous interpretation overrode the current intake: %+v", contract.ExpectedResults)
	}
}
