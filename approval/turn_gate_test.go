package approval

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestAToolNeedingApprovalRunsOnlyOnceTheAskerApproves(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: Approved})

	executed, result := invokeThroughGate(t, withTaskRun(fixture), fixture.gate.TurnGate(Turn{}), "file_delete")

	if len(*executed) != 1 || (*executed)[0] != "file_delete" || result.Failed() {
		t.Fatalf("expected an approved call to run once and succeed, got executed=%+v result=%+v", *executed, result)
	}
}

func TestACallTheAskerRejectsNeverRunsAndSaysItWasRejected(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: Rejected})

	executed, result := invokeThroughGate(t, withTaskRun(fixture), fixture.gate.TurnGate(Turn{}), "file_delete")

	if len(*executed) != 0 || !result.Failed() || !strings.Contains(result.UserSafeFailureSummary(), "declined") {
		t.Fatalf("expected a declined call not to run, got executed=%+v result=%+v", *executed, result)
	}
}

func TestACallNobodyAnswersNeverRunsAndIsNotLeftWaiting(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{})

	executed, result := invokeThroughGate(t, withTaskRun(fixture), fixture.gate.TurnGate(Turn{}), "file_delete")

	if len(*executed) != 0 || !result.Failed() || result.Failure.RequiresApproval {
		t.Fatalf("an unanswered call is refused with a reason the model can act on, not held for a resume nothing performs: %+v", result)
	}
	if !strings.Contains(result.UserSafeFailureSummary(), "did not answer") {
		t.Fatalf("the model is told nobody answered, got %q", result.UserSafeFailureSummary())
	}
	if fixture.taskStatus() == agentcontract.TaskStatusWaitingApproval {
		t.Fatal("a run nothing can resume must not be parked")
	}
}

func TestAnUnansweredCallAsksAgainTheNextTimeItIsMade(t *testing.T) {
	asker := &scriptedAsker{}
	fixture := newFixtureWith(t, nil, asker)
	turnGate := fixture.gate.TurnGate(Turn{})
	invokeThroughGate(t, withTaskRun(fixture), turnGate, "file_delete")

	invokeThroughGate(t, withTaskRun(fixture), turnGate, "file_delete")

	if asker.askedCount != 2 {
		t.Fatalf("a question nobody answered is not an answer to the next one, got %d questions", asker.askedCount)
	}
}

func TestAToolNeedingApprovalWithNoAskerCannotRun(t *testing.T) {
	fixture := newFixtureWith(t, nil, nil)

	executed, result := invokeThroughGate(t, withTaskRun(fixture), fixture.gate.TurnGate(Turn{}), "file_delete")

	if len(*executed) != 0 || !result.Failed() || fixture.hasEvent(agentcontract.TaskEventApprovalHoldOpened) {
		t.Fatalf("with no one to ask the call is refused and nothing is held, got executed=%+v events=%v", *executed, fixture.eventNames())
	}
}

func TestACallWithNoTaskRunCannotRun(t *testing.T) {
	fixture := newFixtureWith(t, nil, &scriptedAsker{answer: Approved})

	executed, result := invokeThroughGate(t, context.Background(), fixture.gate.TurnGate(Turn{}), "file_delete")

	if len(*executed) != 0 || !result.Failed() || fixture.hasEvent(agentcontract.TaskEventApprovalHoldOpened) {
		t.Fatalf("a call with nowhere to record its hold never runs, got executed=%+v", *executed)
	}
}

func TestAToolThatNeedsNoApprovalNeverReachesTheAsker(t *testing.T) {
	asker := &scriptedAsker{}
	fixture := newFixtureWith(t, nil, asker)

	executed, result := invokeThroughGate(t, withTaskRun(fixture), fixture.gate.TurnGate(Turn{}), "file_read")

	if len(*executed) != 1 || result.Failed() || asker.askedCount != 0 {
		t.Fatalf("expected an ungated tool to run unasked, got executed=%+v asked=%d", *executed, asker.askedCount)
	}
}

func TestADelegatedTurnIsDeniedRatherThanAsked(t *testing.T) {
	asker := &scriptedAsker{answer: Approved}
	fixture := newFixtureWith(t, nil, asker)
	delegatedContext := toolcontract.WithDelegatedTurn(withTaskRun(fixture))

	executed, result := invokeThroughGate(t, delegatedContext, fixture.gate.TurnGate(Turn{}), "file_delete")

	if len(*executed) != 0 || !result.Failed() || asker.askedCount != 0 || fixture.hasEvent(agentcontract.TaskEventApprovalHoldOpened) {
		t.Fatalf("a delegated turn has no one to ask, got executed=%+v asked=%d", *executed, asker.askedCount)
	}
}

func TestAToolWithNoApprovalMetadataIsNotGatedEvenWhenItsSchemaMentionsApproval(t *testing.T) {
	fixture := newFixture(t)
	tool := toolcontract.ToolDefinition{Name: "shell", InputSchema: []byte(`{"type":"object","properties":{"approvalRequired":{"type":"boolean"}}}`)}

	review, errorValue := fixture.gate.TurnGate(Turn{}).ReviewToolCall(context.Background(), toolcontract.ToolInvocation{ToolName: "shell", Input: []byte(`{"approvalRequired":true}`)}, tool)

	if errorValue != nil || !review.MayProceed || fixture.hasEvent(agentcontract.TaskEventApprovalHoldOpened) {
		t.Fatalf("a host that gates its own tools has already asked, so bluecollar does not ask again: %+v %v", review, errorValue)
	}
}
