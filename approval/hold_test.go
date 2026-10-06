package approval

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

func (fixture fixture) recordHold(holdID string, toolName string, toolInput string) {
	fixture.record(agentcontract.TaskEventApprovalHoldOpened, `{"holdID":"`+holdID+`","toolName":"`+toolName+`","toolInput":`+toolInput+`,"confirmation":"?"}`)
}

func (fixture fixture) recordDecided(holdID string, decision string) {
	fixture.record(agentcontract.TaskEventApprovalDecided, `{"holdID":"`+holdID+`","decision":"`+decision+`","source":"chat_reply"}`)
}

func TestAHoldRecordedByTheGateReadsBackWhole(t *testing.T) {
	fixture := newFixture(t)
	fixture.awaitOutcome(fixture.scopedRequest())

	hold := fixture.pendingHold(t)

	if hold.ID == "" || hold.Call.HoldID != hold.ID || hold.Call.ApprovalScope != "calendar" || hold.State != holdrecord.StatePending {
		t.Fatalf("a hold is read back whole from the ledger, got %+v", hold)
	}
}

func TestAFreshCallAfterAReloadCancellationIsAskedAboutNotRejected(t *testing.T) {
	asker := &scriptedAsker{}
	fixture := newFixtureWith(t, nil, asker)
	fixture.awaitOutcome(fixture.request())
	fixture.answer(t, Rejected, "acp_permission_reload")

	outcome := fixture.awaitOutcome(fixture.request())

	if outcome.kind != outcomeUnanswered || asker.askedCount != 2 {
		t.Fatalf("a rejection settles the hold it answered, so the same call made fresh is a new question, got %+v after %d questions", outcome, asker.askedCount)
	}
	if holds := holdrecord.Holds(fixture.events()); len(holds) != 2 || holds[0].State != holdrecord.StateRejected || holds[1].State != holdrecord.StatePending {
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
	fixture.awaitOutcome(fixture.request())
	fixture.answer(t, Approved, "chat_reply")

	differentOutcome := fixture.awaitOutcome(fixture.requestWithInput(`{"eventID":"event-2"}`))
	identicalOutcome := fixture.awaitOutcome(fixture.request())
	repeatedOutcome := fixture.awaitOutcome(fixture.request())

	if differentOutcome.kind == outcomeApproved {
		t.Fatalf("an approval does not cover a different call, got %+v", differentOutcome)
	}
	if identicalOutcome.kind != outcomeApproved {
		t.Fatalf("the retry of the approved call runs, got %+v", identicalOutcome)
	}
	if repeatedOutcome.kind == outcomeApproved {
		t.Fatalf("an approval is spent by the call it let run, got %+v", repeatedOutcome)
	}
}

func TestAnApprovalForOneCallSurvivesAnotherCallOfTheSameToolRunning(t *testing.T) {
	fixture := newFixture(t)
	fixture.recordHold("hold-1", "event_delete", `{"eventID":"event-1"}`)
	fixture.recordDecided("hold-1", holdrecord.DecisionApprove)
	fixture.recordHold("hold-2", "event_delete", `{"eventID":"event-2"}`)
	fixture.recordDecided("hold-2", holdrecord.DecisionApprove)

	fixture.awaitOutcome(fixture.requestWithInput(`{"eventID":"event-2"}`))

	if firstOutcome := fixture.awaitOutcome(fixture.requestWithInput(`{"eventID":"event-1"}`)); firstOutcome.kind != outcomeApproved || firstOutcome.holdID != "hold-1" {
		t.Fatalf("the approval for event-1 is spent by event-1 and by nothing else, got %+v", firstOutcome)
	}
}

func TestTheSpentApprovalCarriesTheCallAndTheHoldItRanUnder(t *testing.T) {
	fixture := newFixture(t)
	fixture.awaitOutcome(fixture.request())
	hold := fixture.pendingHold(t)
	fixture.answer(t, Approved, "chat_reply")

	outcome := fixture.awaitOutcome(fixture.request())

	spent := fixture.eventBody(t, agentcontract.TaskEventApprovalHoldSpent)
	if outcome.holdID != hold.ID {
		t.Fatalf("the approved call runs under the hold's own identity, got %+v", outcome)
	}
	for _, expectedFragment := range []string{`"toolName":"event_delete"`, `"eventID":"event-1"`, `"holdID":"` + hold.ID + `"`} {
		if !strings.Contains(spent, expectedFragment) {
			t.Fatalf("expected the spent approval to carry %q, got %s", expectedFragment, spent)
		}
	}
}

func TestACallInsideAGrantedScopeRunsUnderNoHold(t *testing.T) {
	fixture := newFixture(t)
	fixture.record(agentcontract.TaskEventApprovalScopeGranted, `{"scope":"calendar"}`)

	outcome := fixture.awaitOutcome(fixture.scopedRequest())

	if outcome.kind != outcomeApproved || outcome.holdID != "" || strings.Contains(fixture.eventBody(t, agentcontract.TaskEventApprovalHoldSpent), "holdID") {
		t.Fatalf("a call approved by its scope runs under no hold, got %+v", outcome)
	}
}

func TestApprovingAScopedHoldGrantsItsScope(t *testing.T) {
	fixture := newFixture(t)
	fixture.awaitOutcome(fixture.scopedRequest())

	decision := fixture.answer(t, Approved, "chat_reply")

	if decision != outcomeApproved || !strings.Contains(fixture.eventBody(t, agentcontract.TaskEventApprovalScopeGranted), `"scope":"calendar"`) {
		t.Fatalf("approving a hold grants the scope the hold recorded, got %q with %v", decision, fixture.eventNames())
	}
}

func TestEverySurfaceRecordsTheRequesterDecisionOnTheHoldItAnswered(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		answer           Answer
		expectedDecision string
	}{
		{"approve", Approved, "approve"},
		{"reject", Rejected, "reject"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.awaitOutcome(fixture.request())
			hold := fixture.pendingHold(t)

			fixture.answer(t, testCase.answer, "chat_reply")

			decided := fixture.eventBody(t, agentcontract.TaskEventApprovalDecided)
			for _, expectedFragment := range []string{`"holdID":"` + hold.ID + `"`, `"decision":"` + testCase.expectedDecision + `"`, `"source":"chat_reply"`} {
				if !strings.Contains(decided, expectedFragment) {
					t.Fatalf("expected %q in %s", expectedFragment, decided)
				}
			}
		})
	}
}
