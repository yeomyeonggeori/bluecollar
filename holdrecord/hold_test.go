package holdrecord

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func openedHold(t *testing.T) (*taskstate.TaskRunService, string, string) {
	t.Helper()
	taskRunService := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "delete it").TaskRunID
	holdID := Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: json.RawMessage(`{"eventID":"event-1"}`)}, nil).ID
	return taskRunService, taskRunID, holdID
}

var eventDeleteInput = json.RawMessage(`{ "eventID": "event-1" }`)

func TestAnOpenHoldIsPendingAndAnswersNoCall(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	holds := Holds(ledger.ListTaskEvent(taskRunID))

	if len(holds) != 1 || holds[0].ID != holdID || holds[0].State != StatePending {
		t.Fatalf("expected one pending hold, got %+v", holds)
	}
	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); isSpent {
		t.Fatal("a hold nobody approved must not answer the call")
	}
}

func TestAnApprovedHoldIsSpentByTheExactCallOnce(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	spent, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput)
	if !isSpent || spent.ID != holdID {
		t.Fatalf("expected the approved hold to answer the call, got %+v %v", spent, isSpent)
	}
	if _, isSpentTwice := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); isSpentTwice {
		t.Fatal("a spent approval must not answer a second call")
	}
}

func TestADifferentCallIsNotCoveredAndLeavesTheApprovalUnspent(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	for _, call := range []struct{ toolName, toolInput string }{{"event_delete", `{"eventID":"event-2"}`}, {"event_update", `{"eventID":"event-1"}`}} {
		if _, isSpent := SpendApprovedCall(ledger, taskRunID, call.toolName, json.RawMessage(call.toolInput)); isSpent {
			t.Fatalf("%+v must not spend the approval", call)
		}
	}
	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); !isSpent {
		t.Fatal("a refused different call must leave the approval spendable")
	}
}

func TestARejectedHoldAnswersNoCall(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionReject, "chat_reply")

	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); isSpent {
		t.Fatal("a rejection is not an approval")
	}
}

func TestAnApprovalInAnotherTaskRunIsNotCovered(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")
	otherTaskRunID := ledger.CreateTaskRun("person-1", "conversation-2", "other").TaskRunID

	if _, isSpent := SpendApprovedCall(ledger, otherTaskRunID, "event_delete", eventDeleteInput); isSpent {
		t.Fatal("an approval belongs to the task run it was given in")
	}
}

func replayed(source *taskstate.TaskRunService, taskRunID string) (*taskstate.TaskRunService, string) {
	reopened := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	reopenedID := reopened.CreateTaskRun("person-1", "conversation-1", "delete it").TaskRunID
	for _, taskEvent := range source.ListTaskEvent(taskRunID) {
		reopened.AppendTaskEvent(reopenedID, taskEvent.Name, taskEvent.Body)
	}
	return reopened, reopenedID
}

func TestAnApprovalAndItsSpendingSurviveARestart(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	reopened, reopenedID := replayed(ledger, taskRunID)
	if _, isSpent := SpendApprovedCall(reopened, reopenedID, "event_delete", eventDeleteInput); !isSpent {
		t.Fatal("an approval read back from stored events must be spendable")
	}
	reopenedAgain, againID := replayed(reopened, reopenedID)
	if _, isSpent := SpendApprovedCall(reopenedAgain, againID, "event_delete", eventDeleteInput); isSpent {
		t.Fatal("a spend read back from stored events must stay spent")
	}
}

func TestTheRecordWritesOnlyTheEventNamesTheLedgerAlreadyHad(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")
	SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput)

	names := []string{}
	for _, taskEvent := range ledger.ListTaskEvent(taskRunID)[1:] {
		names = append(names, taskEvent.Name)
	}
	expected := []string{agentcontract.TaskEventApprovalHoldOpened, agentcontract.TaskEventApprovalDecided, agentcontract.TaskEventApprovalHoldSpent}
	if len(names) < 3 {
		t.Fatalf("got %v", names)
	}
	for index, name := range expected {
		if names[len(names)-3+index] != name {
			t.Fatalf("expected %v, got %v", expected, names)
		}
	}
}

func TestAHoldWithAKnownToolNameAnswersOnlyThatTool(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_update", eventDeleteInput); isSpent {
		t.Fatal("a hold that names its tool answers no other tool")
	}
}

func namelessHold(t *testing.T, input string) (*taskstate.TaskRunService, string) {
	t.Helper()
	ledger := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRunID := ledger.CreateTaskRun("person-1", "conversation-1", "run it").TaskRunID
	holdID := Open(ledger, taskRunID, agentcontract.HeldCall{ToolInput: json.RawMessage(input)}, nil).ID
	Decide(ledger, taskRunID, holdID, DecisionApprove, "harness_permission")
	return ledger, taskRunID
}

func TestAHoldOpenedWithoutAToolNameAnswersTheCallWithTheSameInput(t *testing.T) {
	ledger, taskRunID := namelessHold(t, `{"eventID":"event-1"}`)

	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", eventDeleteInput); !isSpent {
		t.Fatal("a hold known only by its input answers the call with that input")
	}
}

func TestAHoldOpenedWithoutAToolNameAndWithoutInputAnswersNothing(t *testing.T) {
	for _, input := range []string{``, `{}`, `null`} {
		ledger, taskRunID := namelessHold(t, input)

		if _, isSpent := SpendApprovedCall(ledger, taskRunID, "event_delete", json.RawMessage(input)); isSpent {
			t.Fatalf("input %q would let one approval cover every call that takes no arguments", input)
		}
	}
}

func scopedHold(t *testing.T) (*taskstate.TaskRunService, string, string) {
	t.Helper()
	taskRunService := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRunID := taskRunService.CreateTaskRun("person-1", "conversation-1", "send it").TaskRunID
	holdID := Open(taskRunService, taskRunID, agentcontract.HeldCall{ToolName: "message_send", ApprovalScope: "message_send:team"}, nil).ID
	return taskRunService, taskRunID, holdID
}

func scopeGrantCount(taskRunService *taskstate.TaskRunService, taskRunID string) int {
	count := 0
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == agentcontract.TaskEventApprovalScopeGranted {
			count++
		}
	}
	return count
}

func TestConfirmingAHoldGrantsItsScopeOnce(t *testing.T) {
	taskRunService, taskRunID, holdID := scopedHold(t)

	Decide(taskRunService, taskRunID, holdID, DecisionApprove, "chat_reply")
	Decide(taskRunService, taskRunID, holdID, DecisionApprove, "chat_reply")

	if grants := scopeGrantCount(taskRunService, taskRunID); grants != 1 {
		t.Fatalf("expected one scope grant, got %d", grants)
	}
}

func TestRejectingOrDeferringAHoldGrantsNoScope(t *testing.T) {
	for _, decision := range []string{DecisionReject, DecisionDefer} {
		taskRunService, taskRunID, holdID := scopedHold(t)

		Decide(taskRunService, taskRunID, holdID, decision, "chat_reply")

		if grants := scopeGrantCount(taskRunService, taskRunID); grants != 0 {
			t.Fatalf("%s must not grant scope, got %d grants", decision, grants)
		}
	}
}

func TestADecisionSettlesTheHoldItNamesAndOnlyIt(t *testing.T) {
	ledger, taskRunID, firstID := openedHold(t)
	secondID := Open(ledger, taskRunID, agentcontract.HeldCall{ToolName: "event_delete", ToolInput: json.RawMessage(`{"eventID":"event-2"}`)}, nil).ID
	Decide(ledger, taskRunID, secondID, DecisionReject, "chat_reply")
	Decide(ledger, taskRunID, "hold-unknown", DecisionApprove, "chat_reply")

	holds := Holds(ledger.ListTaskEvent(taskRunID))

	if len(holds) != 2 || holds[0].ID != firstID || holds[0].State != StatePending || holds[1].State != StateRejected {
		t.Fatalf("a decision settles the hold it names, got %+v", holds)
	}
}

func TestASettledHoldIgnoresALaterDecision(t *testing.T) {
	ledger, taskRunID, holdID := openedHold(t)
	Decide(ledger, taskRunID, holdID, DecisionReject, "chat_reply")
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	if holds := Holds(ledger.ListTaskEvent(taskRunID)); holds[0].State != StateRejected {
		t.Fatalf("a rejection is not undone by a decision that arrives after it, got %+v", holds)
	}
}

func TestAHoldIsReadUnderTheToolsCurrentName(t *testing.T) {
	ledger := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRunID := ledger.CreateTaskRun("person-1", "conversation-1", "run it").TaskRunID
	holdID := Open(ledger, taskRunID, agentcontract.HeldCall{ToolName: "shell", ToolInput: json.RawMessage(`{"command":"pwd"}`)}, nil).ID
	Decide(ledger, taskRunID, holdID, DecisionApprove, "chat_reply")

	holds := Holds(ledger.ListTaskEvent(taskRunID))

	if len(holds) != 1 || holds[0].Call.ToolName != "bash" || holds[0].State != StateApproved {
		t.Fatalf("a hold recorded under a former tool name is read under the current one, got %+v", holds)
	}
	if _, isSpent := SpendApprovedCall(ledger, taskRunID, "bash", json.RawMessage(`{"command":"pwd"}`)); !isSpent {
		t.Fatal("the current name answers a hold recorded under the former one")
	}
}

func TestAGrantedScopeIsReadBackFromTheLedger(t *testing.T) {
	taskRunService, taskRunID, holdID := scopedHold(t)
	Decide(taskRunService, taskRunID, holdID, DecisionApprove, "chat_reply")

	ledger := LedgerOf(taskRunService.ListTaskEvent(taskRunID))

	if !ledger.GrantsScope("message_send:team") || ledger.GrantsScope("message_send:other") || ledger.GrantsScope("") {
		t.Fatalf("a scope is granted by an approved hold that carries it, got %+v", ledger)
	}
}

func TestAHoldCarriesTheChoicesItWasOpenedWith(t *testing.T) {
	ledger := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRunID := ledger.CreateTaskRun("person-1", "conversation-1", "update").TaskRunID
	offered := []Choice{{Key: "offHours", StartsAt: "2099-10-03T03:00:00+09:00"}, {Key: "now"}}
	Open(ledger, taskRunID, agentcontract.HeldCall{ToolName: "host_update"}, offered)

	holds := Holds(ledger.ListTaskEvent(taskRunID))

	if len(holds) != 1 || len(holds[0].Choices) != 2 || holds[0].Choices[0] != offered[0] || !holds[0].Choices[0].DefersTheCall() || holds[0].Choices[1].DefersTheCall() {
		t.Fatalf("expected the offered choices in their order, got %+v", holds)
	}
}

func TestAnAnswerAHoldOffersKeepsItsLabelInTheLedger(t *testing.T) {
	ledger := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRunID := ledger.CreateTaskRun("person-1", "conversation-1", "book a room").TaskRunID
	Open(ledger, taskRunID, agentcontract.HeldCall{ToolName: "ask_input"}, []Choice{{Key: "1", Label: "회의실 A"}, {Key: "2", Label: "회의실 B"}})

	choices := Holds(ledger.ListTaskEvent(taskRunID))[0].Choices

	if len(choices) != 2 || choices[1].Label != "회의실 B" || !choices[1].IsAnAnswer() || choices[1].DefersTheCall() {
		t.Fatalf("a choice that carries a label is an answer to a question, and one that starts later is not, got %+v", choices)
	}
}
