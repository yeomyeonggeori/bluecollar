package approval

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestAHeldCallIsRecordedWithTheCallItWasHeldFor(t *testing.T) {
	fixture := newFixture(t)

	outcome := fixture.await(fixture.request())
	if outcome.verdict != verdictUnanswered {
		t.Fatalf("expected the call to be held, got %+v", outcome)
	}

	body := fixture.eventBody(t, agentcontract.TaskEventApprovalPendingCall)
	for _, expectedFragment := range []string{"event_delete", "event-1"} {
		if !strings.Contains(body, expectedFragment) {
			t.Fatalf("expected the held call to carry %q so it can be resumed, got %s", expectedFragment, body)
		}
	}
}

func TestACallWithNoTaskRunToAnswerOnIsUnanswerableRatherThanHeld(t *testing.T) {
	fixture := newFixture(t)

	outcome := fixture.await(requestFixture(""))

	if outcome.verdict != verdictUnanswerable {
		t.Fatalf("a call nobody can be asked about is not waiting for an answer, got %+v", outcome)
	}
}

func TestTheSameCallRunsOnceTheRequesterHasApprovedIt(t *testing.T) {
	fixture := newFixture(t)
	if heldOutcome := fixture.await(fixture.request()); heldOutcome.verdict != verdictUnanswered {
		t.Fatalf("expected the first call to be held, got %+v", heldOutcome)
	}

	fixture.answer(t, Approved, "chat_reply")

	if approvedOutcome := fixture.await(fixture.request()); approvedOutcome.verdict != verdictApproved {
		t.Fatalf("expected the approved call to run when the agent reissues it, got %+v", approvedOutcome)
	}
}

func TestAnApprovalIsSpentOnTheCallItAnsweredAndNotTheNextOne(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.request())
	fixture.answer(t, Approved, "chat_reply")
	fixture.await(fixture.request())

	if repeatedOutcome := fixture.await(fixture.request()); repeatedOutcome.verdict == verdictApproved {
		t.Fatal("expected one approval to authorise one call, so a second identical call is asked about again")
	}
}

func TestAnApprovalDoesNotCarryOverToACallTheRequesterNeverSaw(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.requestWithInput(`{"eventID":"event-1"}`))
	fixture.answer(t, Approved, "chat_reply")

	substitutedOutcome := fixture.await(fixture.requestWithInput(`{"eventID":"event-2"}`))
	if substitutedOutcome.verdict == verdictApproved {
		t.Fatalf("expected approving one call to authorise that call alone, so a substituted target is asked about again, got %+v", substitutedOutcome)
	}
}

func TestAnApprovedCallIsStillRecognisedWhenTheAgentReordersItsInput(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.requestWithInput(`{"eventID":"event-1","calendarID":"team"}`))
	fixture.answer(t, Approved, "chat_reply")

	reorderedOutcome := fixture.await(fixture.requestWithInput(`{"calendarID":"team","eventID":"event-1"}`))
	if reorderedOutcome.verdict != verdictApproved {
		t.Fatalf("expected the same call to be recognised through a reordered input rather than asked about twice, got %+v", reorderedOutcome)
	}
}

func TestAHeldCallTellsTheRequesterWhatTheyAreBeingAskedAbout(t *testing.T) {
	fixture := newFixture(t)
	request := fixture.request()
	request.turn.ResponseLanguage = "ko"
	request.tool.SideEffectClass = toolcontract.ToolSideEffectExternalSend

	fixture.await(request)

	confirmationBody := fixture.eventBody(t, agentcontract.TaskEventConfirmationRequested)
	for _, expectedFragment := range []string{"userFacingMessage", "responseLanguage", "external_send"} {
		if !strings.Contains(confirmationBody, expectedFragment) {
			t.Fatalf("a host reads this event to ask the requester, expected %q in %s", expectedFragment, confirmationBody)
		}
	}
}

func TestAScopedHeldCallRecordsTheScopeItWouldGrant(t *testing.T) {
	fixture := newFixture(t)

	fixture.await(fixture.scopedRequest())

	askBody := fixture.eventBody(t, agentcontract.TaskEventAskRequested)
	for _, expectedFragment := range []string{`"approvalScope":"calendar"`, `"sessionApprovable":true`} {
		if !strings.Contains(askBody, expectedFragment) {
			t.Fatalf("the scope an approval grants is read off this event, expected %q in %s", expectedFragment, askBody)
		}
	}
}

func TestAnUnscopedHeldCallDoesNotOfferAScopeItHasNot(t *testing.T) {
	fixture := newFixture(t)

	fixture.await(fixture.request())

	if askBody := fixture.eventBody(t, agentcontract.TaskEventAskRequested); strings.Contains(askBody, "sessionApprovable") {
		t.Fatalf("a call with no approval scope must not offer approving the whole task, got %s", askBody)
	}
}

func TestAGrantedScopeLetsTheNextCallInThatScopeRunUnasked(t *testing.T) {
	fixture := newFixture(t)
	fixture.await(fixture.scopedRequest())
	fixture.record(agentcontract.TaskEventApprovalScopeGranted, `{"scope":"calendar"}`)
	nextCall := fixture.scopedRequest()
	nextCall.input = []byte(`{"eventID":"event-2"}`)

	if nextCallOutcome := fixture.await(nextCall); nextCallOutcome.verdict != verdictApproved {
		t.Fatalf("approving the whole task means the requester is not asked again inside that scope, got %+v", nextCallOutcome)
	}
}

func TestAGrantedScopeDoesNotCoverAnotherScope(t *testing.T) {
	fixture := newFixture(t)
	fixture.record(agentcontract.TaskEventApprovalScopeGranted, `{"scope":"messaging"}`)

	if heldOutcome := fixture.await(fixture.scopedRequest()); heldOutcome.verdict != verdictUnanswered {
		t.Fatalf("a grant covers the scope it was given for and no other, got %+v", heldOutcome)
	}
}

func TestTheRequesterIsAskedInWordsTheModelChose(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "내일 팀 회의를 캘린더에서 지울까요?"}
	fixture := newFixtureWith(t, languageModel, &scriptedAsker{})

	fixture.await(fixture.request())

	if !strings.Contains(fixture.eventBody(t, agentcontract.TaskEventConfirmationRequested), "내일 팀 회의를 캘린더에서 지울까요?") {
		t.Fatal("the requester has to be asked in words a model wrote, not in a sentence assembled from a tool name")
	}
	if !strings.Contains(languageModel.promptSeen(), "event_delete") {
		t.Fatalf("the model needs the pending call to word the question, got %s", languageModel.promptSeen())
	}
}

func TestAnUnwordableCallStillReachesTheRequesterAsTheCallItself(t *testing.T) {
	fixture := newFixtureWith(t, &wordingLanguageModel{failure: errLanguageModelUnreachable}, &scriptedAsker{})

	heldOutcome := fixture.await(fixture.request())

	if heldOutcome.verdict != verdictUnanswered {
		t.Fatalf("a call nobody could word still has to be held rather than run, got %+v", heldOutcome)
	}
	confirmationBody := fixture.eventBody(t, agentcontract.TaskEventConfirmationRequested)
	for _, expectedFragment := range []string{"event_delete", "event-1"} {
		if !strings.Contains(confirmationBody, expectedFragment) {
			t.Fatalf("with no wording the requester gets the raw call, expected %q in %s", expectedFragment, confirmationBody)
		}
	}
}

func TestAnUnwordableCallRecordsWhyNoModelWordedIt(t *testing.T) {
	fixture := newFixtureWith(t, &wordingLanguageModel{failure: errLanguageModelUnreachable}, &scriptedAsker{})

	fixture.await(fixture.request())

	failureBody := fixture.eventBody(t, agentcontract.TaskEventApprovalWordingFailed)
	for _, expectedFragment := range []string{"event_delete", "the language model is unreachable"} {
		if !strings.Contains(failureBody, expectedFragment) {
			t.Fatalf("a requester asked in a raw call must leave the reason no model worded it, expected %q in %s", expectedFragment, failureBody)
		}
	}
}

func TestAHeldCallIsRecordedOnTheTaskRunTheCallIsRunningIn(t *testing.T) {
	fixture := newFixture(t)
	runningTaskRun := fixture.store.CreateTaskRun("person-1", "conversation-1", "다시 해봐")
	turnGate := fixture.gate.TurnGate(Turn{})

	invokeThroughGate(t, toolcontract.WithTaskRunID(context.Background(), runningTaskRun.TaskRunID), turnGate, "file_delete")

	runningEvents := namesOf(fixture.store.ListTaskEvent(runningTaskRun.TaskRunID))
	for _, expectedEventName := range []string{agentcontract.TaskEventApprovalPendingCall, agentcontract.TaskEventConfirmationRequested, agentcontract.TaskEventAskRequested} {
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
