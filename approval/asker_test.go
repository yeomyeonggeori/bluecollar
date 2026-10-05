package approval

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestAnAnsweredCallRunsInsideTheTurnAndReadsTheSameOnTheLedger(t *testing.T) {
	asker := &scriptedAsker{answer: Approved}
	fixture := newFixtureWith(t, nil, asker)

	outcome := fixture.await(fixture.request())

	if outcome.verdict != verdictApproved {
		t.Fatalf("the answered call decided %q, expected approved", outcome.verdict)
	}
	if asker.askedCount != 1 {
		t.Fatalf("the person was asked %d times, expected once", asker.askedCount)
	}
	for _, wanted := range []string{
		agentcontract.TaskEventApprovalPendingCall,
		agentcontract.TaskEventConfirmationRequested,
		agentcontract.TaskEventApprovalDecided,
		agentcontract.TaskEventApprovalExecuted,
	} {
		if !fixture.hasEvent(wanted) {
			t.Fatalf("the ledger carries %v and not %s, so a live-answered turn reads differently from a later-answered one", fixture.eventNames(), wanted)
		}
	}
	if fixture.status() == agentcontract.TaskStatusWaitingApproval {
		t.Fatal("the run was paused for an approval that had already been answered")
	}
}

func TestADeclinedCallIsRejectedAndNotRecordedAsExecuted(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: Rejected})

	outcome := fixture.await(fixture.request())

	if outcome.verdict != verdictRejected {
		t.Fatalf("the declined call decided %q, expected rejected", outcome.verdict)
	}
	if fixture.hasEvent(agentcontract.TaskEventApprovalExecuted) {
		t.Fatal("a declined call was recorded as executed")
	}
}

func TestAnAskerIsHandedTheHoldWithTheWordedQuestion(t *testing.T) {
	asker := &scriptedAsker{}
	fixture := newFixtureWith(t, &wordingLanguageModel{question: "일정을 지울까요?"}, asker)

	fixture.await(fixture.request())

	if len(asker.holds) != 1 || asker.holds[0].Call.Confirmation != "일정을 지울까요?" || asker.holds[0].Call.ToolName != "event_delete" || asker.holds[0].state != holdPending {
		t.Fatalf("the asker was handed %+v", asker.holds)
	}
}

func TestApprovingAScopedCallGrantsItsScopeForTheRestOfTheTask(t *testing.T) {
	asker := &scriptedAsker{answer: Approved}
	fixture := newFixtureWith(t, nil, asker)
	fixture.await(fixture.scopedRequest())
	nextCall := fixture.scopedRequest()
	nextCall.input = []byte(`{"eventID":"event-2"}`)

	nextOutcome := fixture.await(nextCall)

	if !strings.Contains(fixture.eventBody(t, agentcontract.TaskEventApprovalScopeGranted), `"scope":"calendar"`) {
		t.Fatal("approving a call that declares an approval scope approves that scope")
	}
	if nextOutcome.verdict != verdictApproved || asker.askedCount != 1 {
		t.Fatalf("a call inside the granted scope runs without a second question, got %+v after %d questions", nextOutcome, asker.askedCount)
	}
}

func TestApprovingAnUnscopedCallGrantsNothing(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: Approved})

	fixture.await(fixture.request())

	if fixture.hasEvent(agentcontract.TaskEventApprovalScopeGranted) {
		t.Fatal("a call with no approval scope has no scope to grant")
	}
}

func TestAnAnswerThatIsNoAnswerDecidesNothing(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: NoAnswer})

	outcome := fixture.await(fixture.request())

	if outcome.verdict != verdictUnanswered || fixture.hasEvent(agentcontract.TaskEventApprovalDecided) {
		t.Fatalf("an unread reply is not an answer, got %+v with %v", outcome, fixture.eventNames())
	}
}
