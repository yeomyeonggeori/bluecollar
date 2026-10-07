package approval

import (
	"context"
	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestAHoldTellsTheRequesterWhatTheyAreBeingAskedAbout(t *testing.T) {
	fixture := newFixture(t)
	request := fixture.request()
	request.turn.ResponseLanguage = "ko"
	request.toolDefinition.SideEffectClass = toolcontract.ToolSideEffectExternalSend

	fixture.awaitOutcome(request)

	confirmationBody := fixture.eventBody(t, agentcontract.TaskEventAgentApprovalConfirmationRequested)
	for _, expectedFragment := range []string{"userFacingMessage", "responseLanguage", "external_send"} {
		if !strings.Contains(confirmationBody, expectedFragment) {
			t.Fatalf("a host reads this event to ask the requester, expected %q in %s", expectedFragment, confirmationBody)
		}
	}
}

func TestTheRequesterIsAskedInWordsTheModelChose(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "내일 팀 회의를 캘린더에서 지울까요?"}
	fixture := newFixtureWith(t, languageModel, &scriptedAsker{})

	fixture.awaitOutcome(fixture.request())

	if !strings.Contains(fixture.eventBody(t, agentcontract.TaskEventAgentApprovalConfirmationRequested), "내일 팀 회의를 캘린더에서 지울까요?") {
		t.Fatal("the requester has to be asked in words a model wrote, not in a sentence assembled from a tool name")
	}
	if !strings.Contains(languageModel.promptSeen(), "event_delete") {
		t.Fatalf("the model needs the pending call to word the question, got %s", languageModel.promptSeen())
	}
}

func TestAnUnwordableCallStillReachesTheRequesterAsTheCallItself(t *testing.T) {
	fixture := newFixtureWith(t, &wordingLanguageModel{failure: errLanguageModelUnreachable}, &scriptedAsker{})

	heldOutcome := fixture.awaitOutcome(fixture.request())

	if heldOutcome.Verdict != approvalcore.Unanswered {
		t.Fatalf("a call nobody could word still has to be held rather than run, got %+v", heldOutcome)
	}
	confirmationBody := fixture.eventBody(t, agentcontract.TaskEventAgentApprovalConfirmationRequested)
	for _, expectedFragment := range []string{"event_delete", "event-1"} {
		if !strings.Contains(confirmationBody, expectedFragment) {
			t.Fatalf("with no wording the requester gets the raw call, expected %q in %s", expectedFragment, confirmationBody)
		}
	}
}

func TestAnUnwordableCallRecordsWhyNoModelWordedIt(t *testing.T) {
	fixture := newFixtureWith(t, &wordingLanguageModel{failure: errLanguageModelUnreachable}, &scriptedAsker{})

	fixture.awaitOutcome(fixture.request())

	failureBody := fixture.eventBody(t, agentcontract.TaskEventAgentApprovalWordingFailed)
	for _, expectedFragment := range []string{"event_delete", "the language model is unreachable"} {
		if !strings.Contains(failureBody, expectedFragment) {
			t.Fatalf("a requester asked in a raw call must leave the reason no model worded it, expected %q in %s", expectedFragment, failureBody)
		}
	}
}

func TestAHoldIsRecordedOnTheTaskRunTheCallIsRunningIn(t *testing.T) {
	fixture := newFixture(t)
	runningTaskRun := fixture.store.CreateTaskRun("person-1", "conversation-1", "다시 해봐")
	turnGate := fixture.gate.TurnGate(Turn{})

	invokeThroughGate(t, toolcontract.WithTaskRunID(context.Background(), runningTaskRun.TaskRunID), turnGate, "file_delete")

	runningEvents := namesOf(fixture.store.ListTaskEvent(runningTaskRun.TaskRunID))
	for _, expectedEventName := range []string{agentcontract.TaskEventApprovalHoldOpened, agentcontract.TaskEventAgentApprovalConfirmationRequested, agentcontract.TaskEventAgentApprovalAskRequested} {
		if !contains(runningEvents, expectedEventName) {
			t.Fatalf("expected %q on the run the call is executing in, got %v", expectedEventName, runningEvents)
		}
		if fixture.hasEvent(expectedEventName) {
			t.Fatalf("expected %q never to reach the run the turn left behind, got %v", expectedEventName, fixture.eventNames())
		}
	}
}

func namesOf(taskEvents []agentcontract.TaskEvent) []string {
	names := []string{}
	for _, taskEvent := range taskEvents {
		names = append(names, taskEvent.Name)
	}
	return names
}

func contains(names []string, wanted string) bool {
	for _, name := range names {
		if name == wanted {
			return true
		}
	}
	return false
}
