package intake

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
)

func narrowStateKeys(t *testing.T, request agentcontract.IntakeDecisionRequest) []string {
	t.Helper()
	state := buildPendingAnswerRequest(request, decisionMessageKey(len(request.Messages)-1)).State
	encoded, errorValue := json.Marshal(state)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	fields := map[string]json.RawMessage{}
	if errorValue := json.Unmarshal(encoded, &fields); errorValue != nil {
		t.Fatal(errorValue)
	}
	keys := []string{}
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func busyConfirmationRequest() agentcontract.IntakeDecisionRequest {
	return agentcontract.IntakeDecisionRequest{
		Messages: []agentcontract.IntakeDecisionMessage{
			{MessageID: "earlier", Prompt: "an earlier message", SenderName: "이샘플", SentAt: time.Now()},
			{MessageID: "latest", Prompt: "응", SenderName: "이샘플", SentAt: time.Now()},
		},
		ConversationType:    "direct",
		AgentIdentity:       agentcontract.AgentIdentity{Name: "김인턴"},
		Company:             agentcontract.CompanyContext{Name: "샘플 주식회사", TimeZone: "Asia/Seoul"},
		VisibleContext:      agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{{Speaker: "이샘플", Text: "older context"}}},
		ActiveTask:          agentcontract.ActiveTaskContext{TaskRunID: "task-run-active", Prompt: "something running"},
		PriorTask:           agentcontract.PriorTaskContext{Prompt: "a finished task"},
		PendingConfirmation: agentcontract.PendingConfirmationContext{TaskRunID: "task-run-1", Prompt: "delete the event", Question: "삭제할까요?", AskedAt: time.Now(), ExchangesSince: 2},
		ResponseLanguage:    "ko",
		EnvironmentNow:      time.Now(),
	}
}

func TestTheNarrowConfirmationStateCarriesOnlyWhatTheQuestionNeeds(t *testing.T) {
	keys := narrowStateKeys(t, busyConfirmationRequest())
	expectedKeys := []string{"messages", "pendingConfirmation", "runtimeResponseLanguage"}
	if len(keys) != len(expectedKeys) {
		t.Fatalf("expected the state keys %v, got %v", expectedKeys, keys)
	}
	for index := range expectedKeys {
		if keys[index] != expectedKeys[index] {
			t.Fatalf("expected the state keys %v, got %v", expectedKeys, keys)
		}
	}
}

func TestTheNarrowConfirmationStateHoldsTheLatestMessageTheQuestionAndTheAction(t *testing.T) {
	state := buildPendingAnswerState(busyConfirmationRequest())
	if len(state.Messages) != 1 || state.Messages[0].ID != "m2" || state.Messages[0].Text != "응" {
		t.Fatalf("expected only the latest message as m2, got %+v", state.Messages)
	}
	if state.PendingConfirmation == nil || state.PendingConfirmation.Question != "삭제할까요?" || state.PendingConfirmation.Prompt != "delete the event" {
		t.Fatalf("expected the confirmation question and the action it authorises, got %+v", state.PendingConfirmation)
	}
	if state.PendingChoice != nil || state.ResponseLanguage != "ko" {
		t.Fatalf("expected no pending choice and the response language, got %+v", state)
	}
}

func TestTheNarrowChoiceStateCarriesTheOptionsAndNothingStanding(t *testing.T) {
	request := busyConfirmationRequest()
	request.PendingConfirmation = agentcontract.PendingConfirmationContext{}
	request.PendingChoice = agentcontract.PendingChoiceContext{
		TaskRunID: "task-run-2", Question: "어떤 형식으로 드릴까요?", SelectionMode: "single",
		Options: []agentcontract.ChoiceReplyOption{{Key: "table", Label: "표"}, {Key: "graph", Label: "그래프"}},
	}
	keys := narrowStateKeys(t, request)
	expectedKeys := []string{"messages", "pendingChoice", "runtimeResponseLanguage"}
	for index := range expectedKeys {
		if index >= len(keys) || keys[index] != expectedKeys[index] {
			t.Fatalf("expected the state keys %v, got %v", expectedKeys, keys)
		}
	}
	if state := buildPendingAnswerState(request); len(state.PendingChoice.Options) != 2 {
		t.Fatalf("expected both options in the state, got %+v", state.PendingChoice)
	}
}

func TestTheNarrowCallSendsTheSlimStateToTheDecisionModel(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(approvalOutcome(agentcontract.ApprovalSignalApprove))
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })
	if _, errorValue := planner.DecidePendingAnswer(context.Background(), busyConfirmationRequest(), &agentcontract.IntakeCallLedger{}); errorValue != nil {
		t.Fatal(errorValue)
	}
	requests := decisionModel.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected one call, got %d", len(requests))
	}
	if _, isSlim := requests[0].State.(pendingAnswerState); !isSlim {
		t.Fatalf("expected the slim state, got %T", requests[0].State)
	}
}
