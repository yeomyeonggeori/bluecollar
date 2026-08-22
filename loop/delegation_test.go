package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func delegateActionDocument(instruction string, expectedResult string) string {
	return `{"action":"delegate","instruction":"` + instruction + `","expectedResult":"` + expectedResult + `"}`
}

func TestDelegationCostsNothingUntilAHostAsksForIt(t *testing.T) {
	request := AgentTurnRequest{ToolSet: newTestToolSet([]string{toolcontract.TerminalRunToolName})}

	withoutDelegation := buildAgentSystemInstruction(request, TurnOptions{}).Text()
	if strings.Contains(withoutDelegation, "Delegation:") {
		t.Fatal("a host that never delegates pays for the paragraph on every step of every task forever")
	}
	schemaWithoutDelegation := buildActionSchemaFromToolDefinitions(nil, nil, false, nil, false, true, true, false)
	if strings.Contains(schemaWithoutDelegation, "delegate") {
		t.Fatalf("and pays for the schema variant too: %s", schemaWithoutDelegation)
	}

	withDelegation := buildAgentSystemInstruction(request, TurnOptions{DelegationLimit: 2}).Text()
	if !strings.Contains(withDelegation, "may delegate 2 times in total") {
		t.Fatalf("a limit the model is not told about is a limit it discovers by hitting it: %s", withDelegation)
	}
	if !strings.Contains(buildActionSchemaFromToolDefinitions(nil, nil, false, nil, false, true, true, true), `"delegate"`) {
		t.Fatal("the action has to be in the schema the model answers with")
	}
}

func TestADelegatedTurnReportsBackThroughTheParentsLedger(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		modelTier: "xlow",
		contents: []string{
			delegateActionDocument("read the release notes and say what changed", "one sentence"),
			finishMessageDocument("the release renamed two flags"),
			finishMessageDocument("두 개의 플래그 이름이 바뀌었습니다"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 4, MaxToolCallCount: 5, DelegationLimit: 2})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "무엇이 바뀌었는지 알려줘",
		ToolSet:           newTestToolSet([]string{toolcontract.TerminalRunToolName}),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "delegate.launched", "read the release notes") {
		t.Fatalf("a child the parent cannot account for is work nobody can audit: %d events", len(taskEvents))
	}
	if !taskEventsContain(taskEvents, "delegate.finished", "childTaskRunID") {
		t.Fatal("and the parent's ledger names the run that did it")
	}
	if !strings.Contains(strings.Join(promptsOf(languageModel), "\n"), "the release renamed two flags") {
		t.Fatal("the parent has to see what the child reported, or delegating it lost the work")
	}
}

func TestAChildDoesNotDelegateAgainAndSpendsTheParentsBudget(t *testing.T) {
	parentOptions := TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 10, MaxToolCallCount: 8, MaxElapsedSecond: 600, DelegationLimit: 2}
	runner := NewAgentTurnRunner(nil, nil, nil, nil, parentOptions)
	state := &agentTaskState{IterationCount: 4, ToolCallCount: 3}

	childOptions := runner.childRunner(state).options

	if childOptions.DelegationLimit != 0 {
		t.Fatal("one request must not become a tree nobody sized")
	}
	if childOptions.MaxIterationCount != 6 || childOptions.MaxToolCallCount != 5 {
		t.Fatalf("a child that gets its own full ceiling doubles what the task was allowed to spend: %+v", childOptions)
	}
}

func TestDelegationStopsAtTheLimitAndSaysWhy(t *testing.T) {
	runner := NewAgentTurnRunner(nil, nil, nil, nil, TurnOptions{DelegationLimit: 1})
	state := &agentTaskState{Observations: []turnObservation{{ObservationID: "obs-1", Action: "delegate"}}}

	observation := runner.runDelegatedTurn(context.Background(), "task-1", state, turnActionDocument{Action: "delegate", Instruction: "do the other half"})

	if !observation.Failed() || !strings.Contains(observation.ContentText(), "all 1 of the delegations") {
		t.Fatalf("a refusal the model cannot read is a refusal it will make again next step: %+v", observation)
	}
}
