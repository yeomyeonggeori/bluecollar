package approval

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func (fixture fixture) recordHold(holdID string, toolName string, toolInput string) {
	fixture.record(agentcontract.TaskEventApprovalPendingCall, `{"approvalToken":"`+holdID+`","toolName":"`+toolName+`","toolInput":`+toolInput+`,"confirmation":"?"}`)
}

func (fixture fixture) recordDecided(holdID string, decision string) {
	fixture.record(agentcontract.TaskEventApprovalDecided, `{"approvalToken":"`+holdID+`","decision":"`+decision+`","source":"chat_reply"}`)
}

func TestAHoldIsSettledByItsOwnDecisionAndOnlyByIt(t *testing.T) {
	fixture := newFixture(t)
	fixture.recordHold("hold-1", "event_delete", `{"eventID":"event-1"}`)
	fixture.recordHold("hold-2", "event_delete", `{"eventID":"event-2"}`)
	fixture.recordDecided("hold-2", decisionCancel)
	fixture.recordDecided("hold-unknown", decisionConfirm)

	holds := readLedger(fixture.events()).holds

	if len(holds) != 2 || holds[0].state != holdPending || holds[1].state != holdRejected {
		t.Fatalf("a decision settles the hold it names, got %+v", holds)
	}
}

func TestAHoldThatIsAlreadySettledIgnoresALaterDecision(t *testing.T) {
	fixture := newFixture(t)
	fixture.recordHold("hold-1", "event_delete", `{}`)
	fixture.recordDecided("hold-1", decisionCancel)
	fixture.recordDecided("hold-1", decisionConfirm)

	if holds := readLedger(fixture.events()).holds; holds[0].state != holdRejected {
		t.Fatalf("a rejection is not undone by a decision that arrives after it, got %+v", holds)
	}
}

func TestAHoldWrittenWithoutAnIdentityIsKnownByItsEvent(t *testing.T) {
	fixture := newFixture(t)
	fixture.record(agentcontract.TaskEventApprovalPendingCall, `{"toolName":"shell","toolInput":{"command":"pwd"},"confirmation":"?"}`)
	pendingEvent := fixture.eventNamed(agentcontract.TaskEventApprovalPendingCall)
	fixture.recordDecided(pendingEvent.TaskEventID, decisionConfirm)

	holds := readLedger(fixture.events()).holds

	if len(holds) != 1 || holds[0].ID != pendingEvent.TaskEventID || holds[0].Call.ToolName != "bash" || holds[0].state != holdApproved {
		t.Fatalf("a hold recorded without an identity is the event that recorded it, under the tool's current name, got %+v", holds)
	}
}

func TestAHoldRecordedByTheGateReadsBackWhole(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.scopedRequest())

	hold := fixture.pendingHold(t)

	if hold.ID == "" || hold.Call.ApprovalToken != hold.ID || hold.Call.ApprovalScope != "calendar" || hold.state != holdPending {
		t.Fatalf("a hold is read back whole from the ledger, got %+v", hold)
	}
}

func TestAFreshCallAfterAReloadCancellationIsAskedAboutNotRejected(t *testing.T) {
	asker := &scriptedAsker{}
	fixture := newFixtureWith(t, nil, asker)
	fixture.await(fixture.request())
	fixture.answer(t, Rejected, "acp_permission_reload")

	outcome := fixture.await(fixture.request())

	if outcome.verdict != verdictUnanswered || asker.askedCount != 2 {
		t.Fatalf("a rejection settles the hold it answered, so the same call made fresh is a new question, got %+v after %d questions", outcome, asker.askedCount)
	}
	if holds := readLedger(fixture.events()).holds; len(holds) != 2 || holds[0].state != holdRejected || holds[1].state != holdPending {
		t.Fatalf("expected a rejected hold and a fresh pending one, got %+v", holds)
	}
}

func TestACancellationFromAReloadIsNotCarriedIntoARetryThroughTheTurnGate(t *testing.T) {
	asker := &scriptedAsker{}
	fixture := newFixtureWith(t, nil, asker)
	turnGate := fixture.gate.TurnGate(Turn{})
	turnContext := withTaskRun(fixture)
	invokeThroughGate(t, turnContext, turnGate, "file_delete")
	fixture.answer(t, Rejected, "acp_permission_reload")

	invokeThroughGate(t, turnContext, turnGate, "file_delete")

	if asker.askedCount != 2 {
		t.Fatalf("the retry is asked about afresh rather than refused for an old answer, got %d questions", asker.askedCount)
	}
}

func TestAnApprovedHoldIsReusedOnlyByAnIdenticalCallAndOnlyOnce(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.request())
	fixture.answer(t, Approved, "chat_reply")

	differentOutcome := fixture.await(fixture.requestWithInput(`{"eventID":"event-2"}`))
	identicalOutcome := fixture.await(fixture.request())
	repeatedOutcome := fixture.await(fixture.request())

	if differentOutcome.verdict == verdictApproved {
		t.Fatalf("an approval does not cover a different call, got %+v", differentOutcome)
	}
	if identicalOutcome.verdict != verdictApproved {
		t.Fatalf("the retry of the approved call runs, got %+v", identicalOutcome)
	}
	if repeatedOutcome.verdict == verdictApproved {
		t.Fatalf("an approval is spent by the call it let run, got %+v", repeatedOutcome)
	}
}

func TestAnApprovalForOneCallSurvivesAnotherCallOfTheSameToolRunning(t *testing.T) {
	fixture := newFixture(t)
	fixture.recordHold("hold-1", "event_delete", `{"eventID":"event-1"}`)
	fixture.recordDecided("hold-1", decisionConfirm)
	fixture.recordHold("hold-2", "event_delete", `{"eventID":"event-2"}`)
	fixture.recordDecided("hold-2", decisionConfirm)

	fixture.await(fixture.requestWithInput(`{"eventID":"event-2"}`))

	if firstOutcome := fixture.await(fixture.requestWithInput(`{"eventID":"event-1"}`)); firstOutcome.verdict != verdictApproved || firstOutcome.approvedCallID != "hold-1" {
		t.Fatalf("the approval for event-1 is spent by event-1 and by nothing else, got %+v", firstOutcome)
	}
}

func TestTheSpentApprovalCarriesTheCallAndTheHoldItRanUnder(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.request())
	hold := fixture.pendingHold(t)
	fixture.answer(t, Approved, "chat_reply")

	outcome := fixture.await(fixture.request())

	spent := fixture.eventBody(t, agentcontract.TaskEventApprovalExecuted)
	if outcome.approvedCallID != hold.ID {
		t.Fatalf("the approved call runs under the hold's own identity, got %+v", outcome)
	}
	for _, expectedFragment := range []string{`"toolName":"event_delete"`, `"eventID":"event-1"`, `"approvalToken":"` + hold.ID + `"`} {
		if !strings.Contains(spent, expectedFragment) {
			t.Fatalf("expected the spent approval to carry %q, got %s", expectedFragment, spent)
		}
	}
}

func TestACallInsideAGrantedScopeRunsUnderNoHold(t *testing.T) {
	fixture := newFixture(t)
	fixture.record(agentcontract.TaskEventApprovalScopeGranted, `{"scope":"calendar"}`)

	outcome := fixture.await(fixture.scopedRequest())

	if outcome.verdict != verdictApproved || outcome.approvedCallID != "" || strings.Contains(fixture.eventBody(t, agentcontract.TaskEventApprovalExecuted), "approvalToken") {
		t.Fatalf("a call approved by its scope runs under no hold, got %+v", outcome)
	}
}

func TestAnAnswerToAHoldThatIsNoLongerWaitingChangesNothing(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.scopedRequest())
	fixture.answer(t, Rejected, "chat_reply")
	eventsBefore := len(fixture.events())
	settledHold := readLedger(fixture.events()).holds[0]

	decision := fixture.gate.settle(settledHold, Approved, "acp_permission_reload")

	if decision == verdictApproved || len(fixture.events()) != eventsBefore {
		t.Fatalf("a rejected hold cannot be approved by a late answer, got %q", decision)
	}
}

func TestAReplyThatIsNoAnswerDecidesNothing(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.scopedRequest())

	decision := fixture.answer(t, NoAnswer, "chat_reply")

	if decision != verdictUnanswered || fixture.hasEvent(agentcontract.TaskEventApprovalDecided) || fixture.hasEvent(agentcontract.TaskEventApprovalScopeGranted) {
		t.Fatalf("a reply nobody could read is not an answer, got %q with %v", decision, fixture.eventNames())
	}
}

func TestApprovingAScopedHoldGrantsItsScope(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.scopedRequest())

	decision := fixture.answer(t, Approved, "chat_reply")

	if decision != verdictApproved || !strings.Contains(fixture.eventBody(t, agentcontract.TaskEventApprovalScopeGranted), `"scope":"calendar"`) {
		t.Fatalf("approving a hold grants the scope the hold recorded, got %q with %v", decision, fixture.eventNames())
	}
}

func TestEverySurfaceRecordsTheRequesterDecisionOnTheHoldItAnswered(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		answer           Answer
		expectedDecision string
	}{
		{"approve", Approved, "confirm"},
		{"reject", Rejected, "cancel"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.await(fixture.request())
			hold := fixture.pendingHold(t)

			fixture.answer(t, testCase.answer, "chat_reply")

			decided := fixture.eventBody(t, agentcontract.TaskEventApprovalDecided)
			for _, expectedFragment := range []string{`"approvalToken":"` + hold.ID + `"`, `"decision":"` + testCase.expectedDecision + `"`, `"source":"chat_reply"`} {
				if !strings.Contains(decided, expectedFragment) {
					t.Fatalf("expected %q in %s", expectedFragment, decided)
				}
			}
		})
	}
}
