package approval

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/turnclock"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func TestAnAnsweredCallRunsInsideTheTurnAndReadsTheSameOnTheLedger(t *testing.T) {
	asker := &scriptedAsker{answer: Approved}
	fixture := newFixtureWith(t, nil, asker)

	outcome := fixture.awaitOutcome(fixture.request())

	if outcome.kind != outcomeApproved {
		t.Fatalf("the answered call decided %q, expected approved", outcome.kind)
	}
	if asker.askedCount != 1 {
		t.Fatalf("the person was asked %d times, expected once", asker.askedCount)
	}
	for _, wanted := range []string{
		agentcontract.TaskEventApprovalHoldOpened,
		agentcontract.TaskEventAgentApprovalConfirmationRequested,
		agentcontract.TaskEventApprovalDecided,
		agentcontract.TaskEventApprovalHoldSpent,
	} {
		if !fixture.hasEvent(wanted) {
			t.Fatalf("the ledger carries %v and not %s, so a live-answered turn reads differently from a later-answered one", fixture.eventNames(), wanted)
		}
	}
	if fixture.taskStatus() == agentcontract.TaskStatusWaitingApproval {
		t.Fatal("the run was paused for an approval that had already been answered")
	}
}

func TestARejectedCallIsNotRecordedAsExecuted(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: Rejected})

	outcome := fixture.awaitOutcome(fixture.request())

	if outcome.kind != outcomeRejected {
		t.Fatalf("the declined call decided %q, expected rejected", outcome.kind)
	}
	if fixture.hasEvent(agentcontract.TaskEventApprovalHoldSpent) {
		t.Fatal("a declined call was recorded as executed")
	}
}

func TestAnAskerIsHandedTheHoldWithTheWordedQuestion(t *testing.T) {
	asker := &scriptedAsker{}
	fixture := newFixtureWith(t, &wordingLanguageModel{question: "일정을 지울까요?"}, asker)

	fixture.awaitOutcome(fixture.request())

	if len(asker.holds) != 1 || asker.holds[0].Call.Confirmation != "일정을 지울까요?" || asker.holds[0].Call.ToolName != "event_delete" || asker.holds[0].State != holdrecord.StatePending {
		t.Fatalf("the asker was handed %+v", asker.holds)
	}
}

func TestApprovingAScopedCallGrantsItsScopeForTheRestOfTheTask(t *testing.T) {
	asker := &scriptedAsker{answer: Approved}
	fixture := newFixtureWith(t, nil, asker)
	fixture.awaitOutcome(fixture.scopedRequest())
	nextCall := fixture.scopedRequest()
	nextCall.toolInput = []byte(`{"eventID":"event-2"}`)

	nextOutcome := fixture.awaitOutcome(nextCall)

	if !strings.Contains(fixture.eventBody(t, agentcontract.TaskEventApprovalScopeGranted), `"scope":"calendar"`) {
		t.Fatal("approving a call that declares an approval scope approves that scope")
	}
	if nextOutcome.kind != outcomeApproved || asker.askedCount != 1 {
		t.Fatalf("a call inside the granted scope runs without a second question, got %+v after %d questions", nextOutcome, asker.askedCount)
	}
}

func TestApprovingAnUnscopedCallGrantsNothing(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: Approved})

	fixture.awaitOutcome(fixture.request())

	if fixture.hasEvent(agentcontract.TaskEventApprovalScopeGranted) {
		t.Fatal("a call with no approval scope has no scope to grant")
	}
}

func TestAnAnswerThatIsNoAnswerDecidesNothing(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: NoAnswer})

	outcome := fixture.awaitOutcome(fixture.request())

	if outcome.kind != outcomeUnanswered || fixture.hasEvent(agentcontract.TaskEventApprovalDecided) {
		t.Fatalf("an unread reply is not an answer, got %+v with %v", outcome, fixture.eventNames())
	}
}

func TestWaitingOnThePersonDoesNotSpendTheTurnsTime(t *testing.T) {
	const turnBudget = 60 * time.Millisecond
	turnContext, cancelTurn := turnclock.WithActiveBudget(turnclock.With(context.Background(), turnclock.New()), time.Now(), turnBudget)
	defer cancelTurn()
	asker := &scriptedAsker{answer: Approved, beforeAnswering: func() { time.Sleep(4 * turnBudget) }}
	fixture := newFixtureWith(t, nil, asker)

	outcome := fixture.gate.awaitApproval(turnContext, fixture.request())

	if outcome.kind != outcomeApproved {
		t.Fatalf("the answered call decided %q, expected approved", outcome.kind)
	}
	if turnContext.Err() != nil {
		t.Fatalf("the turn ended (%v) while the person was deciding, so an answer given after the turn's budget found nothing left to run the call", turnContext.Err())
	}
}
