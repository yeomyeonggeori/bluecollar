package intake

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const expectedResultsReply = `{"expectedResults":[]}`

func pendingConfirmationRequest(prompt string) agentcontract.AgentRequest {
	return agentcontract.AgentRequest{
		Prompt:              prompt,
		ResponseLanguage:    "ko",
		PendingConfirmation: agentcontract.PendingConfirmationContext{TaskRunID: "task-run-1", Question: "삭제할까요?"},
	}
}

func pendingChoiceRequest(prompt string, selectionMode string) agentcontract.AgentRequest {
	return agentcontract.AgentRequest{
		Prompt:           prompt,
		ResponseLanguage: "ko",
		PendingChoice: agentcontract.PendingChoiceContext{
			TaskRunID:     "task-run-2",
			Question:      "어떤 형식으로 드릴까요?",
			SelectionMode: selectionMode,
			Options:       []agentcontract.ChoiceReplyOption{{Key: "table", Label: "표"}, {Key: "graph", Label: "그래프"}},
		},
	}
}

func approvalOutcome(signal agentcontract.ApprovalSignal) intaketest.Outcome {
	outcome := startTaskOutcome()
	outcome.TurnDecision.Approval = &signal
	return outcome
}

func planWith(t *testing.T, decisionModel *intaketest.DecisionModel, request agentcontract.AgentRequest, callLedger *agentcontract.IntakeCallLedger) agentcontract.TurnDecision {
	t.Helper()
	languageModel := &sequenceLanguageModel{contents: []string{expectedResultsReply}}
	turnRouter := NewTurnRouter(languageModel, NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }), enabledIntakeOptions())
	decision, errorValue := turnRouter.PlanObserved(context.Background(), request, callLedger)
	if errorValue != nil {
		t.Fatalf("expected a routed turn: %v", errorValue)
	}
	return decision
}

func questionNames(request model.DecisionRequest) []string {
	names := []string{}
	for name := range request.Questions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func assertOnlyQuestions(t *testing.T, request model.DecisionRequest, expectedNames ...string) {
	t.Helper()
	sort.Strings(expectedNames)
	actualNames := questionNames(request)
	if len(actualNames) != len(expectedNames) {
		t.Fatalf("expected exactly the questions %v, got %v", expectedNames, actualNames)
	}
	for index := range expectedNames {
		if actualNames[index] != expectedNames[index] {
			t.Fatalf("expected exactly the questions %v, got %v", expectedNames, actualNames)
		}
	}
}

func assertNoPendingInteraction(t *testing.T, request model.DecisionRequest) {
	t.Helper()
	for name := range request.Questions {
		for _, pendingName := range []string{agentcontract.IntakeQuestionApproval, agentcontract.IntakeQuestionChoice, agentcontract.IntakeQuestionPendingAnswer, agentcontract.IntakeQuestionPrefixChoice} {
			if strings.HasPrefix(name, "m1."+pendingName) {
				t.Fatalf("expected the general questionnaire to hold no pending question, got %s", name)
			}
		}
	}
	state, isState := request.State.(decisionState)
	if !isState {
		t.Fatalf("expected a decision state, got %T", request.State)
	}
	if state.PendingConfirmation != nil || state.PendingChoice != nil {
		t.Fatalf("expected the general call to carry no pending interaction, got %+v", state)
	}
}

func TestAnApprovedPendingConfirmationIsReadFromOneQuestion(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(approvalOutcome(agentcontract.ApprovalSignalApprove))

	decision := planWith(t, decisionModel, pendingConfirmationRequest("ㅇ"), nil)

	if decision.Approval == nil || *decision.Approval != agentcontract.ApprovalSignalApprove {
		t.Fatalf("expected the approval, got %+v", decision.Approval)
	}
	if decision.Route != agentcontract.TurnRouteContinueTask || decision.RawDecisionRoute != agentcontract.TurnRouteContinueTask {
		t.Fatalf("expected continue_task, got %q (raw %q)", decision.Route, decision.RawDecisionRoute)
	}
	requests := decisionModel.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected one decision call, got %d", len(requests))
	}
	assertOnlyQuestions(t, requests[0], "m1."+agentcontract.IntakeQuestionApproval)
	options, _ := requests[0].Questions["m1."+agentcontract.IntakeQuestionApproval].Criteria.(map[string]string)
	if len(options) != 3 {
		t.Fatalf("expected approve, reject and other, got %+v", options)
	}
}

func TestARejectedPendingConfirmationIsReadFromOneQuestion(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(approvalOutcome(agentcontract.ApprovalSignalReject))

	decision := planWith(t, decisionModel, pendingConfirmationRequest("아니"), nil)

	if decision.Approval == nil || *decision.Approval != agentcontract.ApprovalSignalReject {
		t.Fatalf("expected the rejection, got %+v", decision.Approval)
	}
	if len(decisionModel.Requests()) != 1 {
		t.Fatalf("expected one decision call, got %d", len(decisionModel.Requests()))
	}
}

func TestAMessageThatDoesNotAnswerAConfirmationGoesThroughTheGeneralQuestionnaire(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())

	decision := planWith(t, decisionModel, pendingConfirmationRequest("다음 주 일정도 정리해줘"), nil)

	requests := decisionModel.Requests()
	if len(requests) < 2 {
		t.Fatalf("expected the narrow call and then the general one, got %d calls", len(requests))
	}
	assertNoPendingInteraction(t, requests[1])
	if _, isAsked := requests[1].Questions["m1."+agentcontract.IntakeQuestionRoute]; !isAsked {
		t.Fatal("expected the second call to be the general questionnaire")
	}
	if decision.Approval != nil || len(decision.Choices) != 0 {
		t.Fatalf("expected no answer to the pending question, got %+v %+v", decision.Approval, decision.Choices)
	}
	if decision.Route != agentcontract.TurnRouteStartTask {
		t.Fatalf("expected the general decision, got %q", decision.Route)
	}
}

func TestASingleChoiceAnswerIsReadFromOneQuestion(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.PendingChoiceKeys = []string{"table", "graph"}
	outcome.TurnDecision.Choices = []string{"graph"}
	decisionModel := intaketest.NewDecisionModel(outcome)

	decision := planWith(t, decisionModel, pendingChoiceRequest("2번", ""), nil)

	if len(decision.Choices) != 1 || decision.Choices[0] != "graph" {
		t.Fatalf("expected the chosen key, got %+v", decision.Choices)
	}
	if decision.Route != agentcontract.TurnRouteContinueTask {
		t.Fatalf("expected continue_task, got %q", decision.Route)
	}
	requests := decisionModel.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected one decision call, got %d", len(requests))
	}
	assertOnlyQuestions(t, requests[0], "m1."+agentcontract.IntakeQuestionChoice)
}

func TestASingleChoiceMessageThatSelectsNothingGoesThroughTheGeneralQuestionnaire(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.PendingChoiceKeys = []string{"table", "graph"}
	decisionModel := intaketest.NewDecisionModel(outcome)

	decision := planWith(t, decisionModel, pendingChoiceRequest("다른 얘기인데요", ""), nil)

	requests := decisionModel.Requests()
	if len(requests) < 2 {
		t.Fatalf("expected the narrow call and then the general one, got %d calls", len(requests))
	}
	assertNoPendingInteraction(t, requests[1])
	if len(decision.Choices) != 0 || decision.Approval != nil {
		t.Fatalf("expected no answer to the pending question, got %+v %+v", decision.Approval, decision.Choices)
	}
}

func TestAMultipleChoiceAnswerAsksEachOptionAndWhetherItAnswers(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.PendingChoiceKeys = []string{"table", "graph"}
	outcome.TurnDecision.Choices = []string{"table", "graph"}
	decisionModel := intaketest.NewDecisionModel(outcome)

	decision := planWith(t, decisionModel, pendingChoiceRequest("둘 다", "multiple"), nil)

	if len(decision.Choices) != 2 {
		t.Fatalf("expected both options, got %+v", decision.Choices)
	}
	requests := decisionModel.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected one decision call, got %d", len(requests))
	}
	assertOnlyQuestions(t, requests[0],
		"m1."+agentcontract.IntakeQuestionPendingAnswer,
		"m1."+agentcontract.IntakeQuestionPrefixChoice+"table",
		"m1."+agentcontract.IntakeQuestionPrefixChoice+"graph",
	)
}

func TestAMultipleChoiceMessageThatDoesNotAnswerGoesThroughTheGeneralQuestionnaire(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.PendingChoiceKeys = []string{"table", "graph"}
	decisionModel := intaketest.NewDecisionModel(outcome)

	decision := planWith(t, decisionModel, pendingChoiceRequest("그건 됐고 보고서 정리해줘", "multiple"), nil)

	requests := decisionModel.Requests()
	if len(requests) < 2 {
		t.Fatalf("expected the narrow call and then the general one, got %d calls", len(requests))
	}
	assertNoPendingInteraction(t, requests[1])
	if len(decision.Choices) != 0 {
		t.Fatalf("expected no selection, got %+v", decision.Choices)
	}
}

func TestAMultipleChoiceReplyThatAnswersWithoutSelectingAnOptionGoesThroughTheGeneralQuestionnaire(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.PendingChoiceKeys = []string{"table", "graph"}
	outcome.AnswersPendingChoiceWithoutSelecting = true
	decisionModel := intaketest.NewDecisionModel(outcome)

	decision := planWith(t, decisionModel, pendingChoiceRequest("둘 다 싫어요", "multiple"), nil)

	requests := decisionModel.Requests()
	if len(requests) < 2 {
		t.Fatalf("expected the narrow call and then the general one, got %d calls", len(requests))
	}
	assertNoPendingInteraction(t, requests[1])
	if len(decision.Choices) != 0 {
		t.Fatalf("expected no selection, got %+v", decision.Choices)
	}
}

func TestAPendingFreeTextInputContinuesTheTaskWithoutAskingTheDecisionModel(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	request := agentcontract.AgentRequest{
		Prompt:           "다음 주 화요일 오후 3시",
		ResponseLanguage: "ko",
		PendingInput:     agentcontract.PendingInputContext{TaskRunID: "task-run-3", Question: "언제로 잡을까요?"},
	}

	decision := planWith(t, decisionModel, request, nil)

	if decision.Route != agentcontract.TurnRouteContinueTask {
		t.Fatalf("expected continue_task, got %q", decision.Route)
	}
	if decision.Approval != nil || len(decision.Choices) != 0 {
		t.Fatalf("expected no approval or choice, got %+v %+v", decision.Approval, decision.Choices)
	}
	if requests := decisionModel.Requests(); len(requests) != 0 {
		t.Fatalf("expected no decision call, got %d", len(requests))
	}
}

func TestTheNarrowCallIsRecordedInTheIntakeLedger(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(approvalOutcome(agentcontract.ApprovalSignalApprove))
	callLedger := &agentcontract.IntakeCallLedger{}

	planWith(t, decisionModel, pendingConfirmationRequest("ㅇ"), callLedger)

	decisionRecords := 0
	for _, record := range callLedger.Records {
		if record.Kind != agentcontract.LLMCallKindDecision {
			continue
		}
		decisionRecords++
		if record.QuestionCount != 1 || len(record.DecidedMessageIDs) != 1 {
			t.Fatalf("expected the narrow call's question and message, got %+v", record)
		}
	}
	if decisionRecords != 1 {
		t.Fatalf("expected one decision record, got %d", decisionRecords)
	}
}

func TestANormalizedPendingAnswerKeepsTheWorkFieldsEmpty(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(approvalOutcome(agentcontract.ApprovalSignalApprove))

	decision := planWith(t, decisionModel, pendingConfirmationRequest("ㅇ"), nil)

	if decision.Classification != "" || decision.TaskShape != "" || decision.TaskLevel != "" || decision.DeliverableKind != "" || decision.BusyRoute != "" {
		t.Fatalf("expected an answer to carry no work decision, got %+v", decision)
	}
}

func TestAnApprovedAnswerWithAnActiveTaskNeverBecomesAnUnrelatedBusyRoute(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(approvalOutcome(agentcontract.ApprovalSignalApprove))
	request := pendingConfirmationRequest("ㅇ")
	request.ActiveTask = agentcontract.ActiveTaskContext{TaskRunID: "task-run-1", Prompt: "일정 삭제"}

	decision := planWith(t, decisionModel, request, nil)

	if decision.BusyRoute != "" {
		t.Fatalf("expected no busy route for an answer, got %q", decision.BusyRoute)
	}
	if decision.Approval == nil || *decision.Approval != agentcontract.ApprovalSignalApprove {
		t.Fatalf("expected the approval, got %+v", decision.Approval)
	}
}

func TestADecidedContinuationWithAnActiveTaskStillNeedsABusyRoute(t *testing.T) {
	decidedFields := decidedTurnFields(agentcontract.TurnRouteContinueTask, agentcontract.IntakeClassificationBoundedTask)
	request := agentcontract.AgentRequest{ActiveTask: agentcontract.ActiveTaskContext{TaskRunID: "task-run-1"}}

	if _, errorValue := normalizeDecidedTurnFields(decidedFields, request); errorValue == nil {
		t.Fatal("expected a decided continuation without a busy route to be refused")
	}
}
