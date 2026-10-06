package approval

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestAHoldIsRecordedWithTheCallItWasHeldFor(t *testing.T) {
	fixture := newFixture(t)

	outcome := fixture.awaitOutcome(fixture.request())
	if outcome.kind != outcomeUnanswered {
		t.Fatalf("expected the call to be held, got %+v", outcome)
	}

	body := fixture.eventBody(t, agentcontract.TaskEventApprovalHoldOpened)
	for _, expectedFragment := range []string{"event_delete", "event-1"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("expected the held call to carry %q so it can be resumed, got %s", expectedFragment, body)
		}
	}
}

func TestACallWithNoTaskRunToAnswerOnIsUnanswerableRatherThanHeld(t *testing.T) {
	fixture := newFixture(t)

	outcome := fixture.awaitOutcome(requestFixture(""))

	if outcome.kind != outcomeUnanswerable {
		t.Fatalf("a call nobody can be asked about is not waiting for an answer, got %+v", outcome)
	}
}

func TestTheSameCallRunsOnceTheRequesterHasApprovedIt(t *testing.T) {
	fixture := newFixture(t)
	if heldOutcome := fixture.awaitOutcome(fixture.request()); heldOutcome.kind != outcomeUnanswered {
		t.Fatalf("expected the first call to be held, got %+v", heldOutcome)
	}

	fixture.answer(t, Approved, "chat_reply")

	if approvedOutcome := fixture.awaitOutcome(fixture.request()); approvedOutcome.kind != outcomeApproved {
		t.Fatalf("expected the approved call to run when the agent reissues it, got %+v", approvedOutcome)
	}
}

func TestAnApprovalIsSpentOnTheCallItAnsweredAndNotTheNextOne(t *testing.T) {
	fixture := newFixture(t)
	fixture.awaitOutcome(fixture.request())
	fixture.answer(t, Approved, "chat_reply")
	fixture.awaitOutcome(fixture.request())

	if repeatedOutcome := fixture.awaitOutcome(fixture.request()); repeatedOutcome.kind == outcomeApproved {
		t.Fatal("expected one approval to authorise one call, so a second identical call is asked about again")
	}
}

func TestAnApprovalDoesNotCarryOverToACallTheRequesterNeverSaw(t *testing.T) {
	fixture := newFixture(t)
	fixture.awaitOutcome(fixture.requestWithInput(`{"eventID":"event-1"}`))
	fixture.answer(t, Approved, "chat_reply")

	substitutedOutcome := fixture.awaitOutcome(fixture.requestWithInput(`{"eventID":"event-2"}`))
	if substitutedOutcome.kind == outcomeApproved {
		t.Fatalf("expected approving one call to authorise that call alone, so a substituted target is asked about again, got %+v", substitutedOutcome)
	}
}

func TestAnApprovedCallIsStillRecognisedWhenTheAgentReordersItsInput(t *testing.T) {
	fixture := newFixture(t)
	fixture.awaitOutcome(fixture.requestWithInput(`{"eventID":"event-1","calendarID":"team"}`))
	fixture.answer(t, Approved, "chat_reply")

	reorderedOutcome := fixture.awaitOutcome(fixture.requestWithInput(`{"calendarID":"team","eventID":"event-1"}`))
	if reorderedOutcome.kind != outcomeApproved {
		t.Fatalf("expected the same call to be recognised through a reordered input rather than asked about twice, got %+v", reorderedOutcome)
	}
}

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

func TestAScopedHoldRecordsTheScopeItWouldGrant(t *testing.T) {
	fixture := newFixture(t)

	fixture.awaitOutcome(fixture.scopedRequest())

	askBody := fixture.eventBody(t, agentcontract.TaskEventAgentApprovalAskRequested)
	for _, expectedFragment := range []string{`"approvalScope":"calendar"`, `"sessionApprovable":true`} {
		if !strings.Contains(askBody, expectedFragment) {
			t.Fatalf("the scope an approval grants is read off this event, expected %q in %s", expectedFragment, askBody)
		}
	}
}

func TestAnUnscopedHoldDoesNotOfferAScopeItHasNot(t *testing.T) {
	fixture := newFixture(t)

	fixture.awaitOutcome(fixture.request())

	if askBody := fixture.eventBody(t, agentcontract.TaskEventAgentApprovalAskRequested); strings.Contains(askBody, "sessionApprovable") {
		t.Fatalf("a call with no approval scope must not offer approving the whole task, got %s", askBody)
	}
}

func TestAGrantedScopeLetsTheNextCallInThatScopeRunUnasked(t *testing.T) {
	fixture := newFixture(t)
	fixture.awaitOutcome(fixture.scopedRequest())
	fixture.record(agentcontract.TaskEventApprovalScopeGranted, `{"scope":"calendar"}`)
	nextCall := fixture.scopedRequest()
	nextCall.toolInput = []byte(`{"eventID":"event-2"}`)

	if nextCallOutcome := fixture.awaitOutcome(nextCall); nextCallOutcome.kind != outcomeApproved {
		t.Fatalf("approving the whole task means the requester is not asked again inside that scope, got %+v", nextCallOutcome)
	}
}

func TestAGrantedScopeDoesNotCoverAnotherScope(t *testing.T) {
	fixture := newFixture(t)
	fixture.record(agentcontract.TaskEventApprovalScopeGranted, `{"scope":"messaging"}`)

	if heldOutcome := fixture.awaitOutcome(fixture.scopedRequest()); heldOutcome.kind != outcomeUnanswered {
		t.Fatalf("a grant covers the scope it was given for and no other, got %+v", heldOutcome)
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

	if heldOutcome.kind != outcomeUnanswered {
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
