package intake

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func questionsFor(request agentcontract.IntakeDecisionRequest) map[string]model.DecisionQuestion {
	builder := newQuestionBuilder(request)
	questions := builder.questionsWithoutTools()
	for questionName, question := range toolQuestionsFor(request, resolveCallableToolNames(request)) {
		questions[questionName] = question
	}
	return questions
}

func criteriaText(t *testing.T, question model.DecisionQuestion) string {
	t.Helper()
	document, errorValue := json.Marshal(question.Criteria)
	if errorValue != nil {
		t.Fatalf("expected the criteria to marshal: %v", errorValue)
	}
	return question.Instructions + " " + string(document)
}

func TestTheRouteQuestionReservesGiveUpForImpossibleWork(t *testing.T) {
	questions := questionsFor(addressedDecisionRequest("이 파일로 덱 만들어줘"))

	routeCriteria := criteriaText(t, questions["m1."+agentcontract.IntakeQuestionRoute])
	if !strings.Contains(routeCriteria, "Never for a permission concern, which the operating system decides at execution") {
		t.Fatalf("expected give_up to stay out of permission decisions, got %s", routeCriteria)
	}
}

func TestClarificationQuestionDefersToolDiscoverableRequirementsToWork(t *testing.T) {
	questions := questionsFor(addressedDecisionRequest("make a report"))
	routeCriteria := criteriaText(t, questions["m1."+agentcontract.IntakeQuestionRoute])
	for _, expected := range []string{
		"requested goal, target, or outcome",
		"only the sender can resolve it",
		"operational details, approval roles",
		"a tool can inspect or resolve",
	} {
		if !strings.Contains(routeCriteria, expected) {
			t.Fatalf("expected the route question to include %q, got %s", expected, routeCriteria)
		}
	}

	shapeCriteria := criteriaText(t, questions["m1."+agentcontract.IntakeQuestionTaskShape])
	if !strings.Contains(shapeCriteria, "tool-discoverable operational requirements belong to the work itself") {
		t.Fatalf("expected approval-gated task shape to exclude tool-discoverable requirements, got %s", shapeCriteria)
	}
	independentWorkQuestion := questions["m1."+agentcontract.IntakeQuestionHasIndependentWork]
	independentWorkCriteria := criteriaText(t, independentWorkQuestion)
	if independentWorkQuestion.Type != model.DecisionQuestionTypeNoul {
		t.Fatalf("expected independent work to use a typed yes/no question, got %q", independentWorkQuestion.Type)
	}
	for _, expected := range []string{
		"independently requested part",
		"clear target and effect",
		"does not depend on the unresolved answer",
		"no actionable work was requested",
		"only a prerequisite",
		"action the requester did not authorize",
	} {
		if !strings.Contains(independentWorkCriteria, expected) {
			t.Fatalf("expected independent-work question to include %q, got %s", expected, independentWorkCriteria)
		}
	}
	if !strings.Contains(shapeCriteria, "classify the work that can proceed") {
		t.Fatalf("expected task shape to describe executable work, got %s", shapeCriteria)
	}
}

func TestExternalSendQuestionSeparatesIntentFromCurrentConversationAndToolAvailability(t *testing.T) {
	question := questionsFor(addressedDecisionRequest("deliver the report"))["m1."+agentcontract.IntakeQuestionIsExternalSendRequested]
	criteria := criteriaText(t, question)
	if question.Type != model.DecisionQuestionTypeNoul {
		t.Fatalf("expected the external-send question to use Noul, got %q", question.Type)
	}
	for _, expected := range []string{
		"content or files",
		"outside the current conversation",
		"on a schedule",
		"quoted or forwarded content",
		"intent, not whether the effect is approved or permitted",
		"attachment in this current conversation",
		"optional notification",
		"send tool available",
	} {
		if !strings.Contains(criteria, expected) {
			t.Fatalf("expected external-send intent guidance %q, got %s", expected, criteria)
		}
	}
}

func TestEveryQuestionOptionIsAString(t *testing.T) {
	request := addressedDecisionRequest("보고서 정리해줘")
	request.ToolSet = newTestToolSet([]string{"task_add"})

	for questionName, question := range questionsFor(request) {
		switch question.Type {
		case model.DecisionQuestionTypeChoice:
			criteria, isOptionMap := question.Criteria.(map[string]string)
			if !isOptionMap || len(criteria) == 0 {
				t.Fatalf("expected %s to carry named string options, got %+v", questionName, question.Criteria)
			}
		case model.DecisionQuestionTypeNoul:
			criteria, isTwoSided := question.Criteria.(map[string]string)
			if !isTwoSided || criteria["true"] == "" || criteria["false"] == "" {
				t.Fatalf("expected %s to carry a two-sided criterion, got %+v", questionName, question.Criteria)
			}
		default:
			t.Fatalf("expected %s to be a choice or a noul, got %q", questionName, question.Type)
		}
		if strings.TrimSpace(question.Instructions) == "" {
			t.Fatalf("expected %s to carry instructions", questionName)
		}
	}
}

func TestThePriorTaskQuestionIsAskedOnlyForAPriorTask(t *testing.T) {
	if _, isAsked := questionsFor(addressedDecisionRequest("보고서 다시 보내줘"))["m1."+agentcontract.IntakeQuestionPriorTaskReference]; isAsked {
		t.Fatal("expected no prior task question when the state carries no prior task")
	}

	request := addressedDecisionRequest("보고서 다시 보내줘")
	request.PriorTask = agentcontract.PriorTaskContext{TaskRunID: "task-run-0", Prompt: "보고서 정리해줘"}
	if _, isAsked := questionsFor(request)["m1."+agentcontract.IntakeQuestionPriorTaskReference]; !isAsked {
		t.Fatal("expected the prior task question when the state carries a prior task")
	}
}

func firingDecisionRequest(prompt string) agentcontract.IntakeDecisionRequest {
	request := addressedDecisionRequest(prompt)
	request.ScheduledRun = agentcontract.ScheduledRunContext{
		ScheduleID:   "schedule-1",
		Name:         "주간 보고 알림",
		Kind:         "cron",
		Cadence:      "매일 22:08",
		OccurrenceAt: "2026-09-18T22:08:00Z",
	}
	return request
}

func TestEveryQuestionAboutAFiringSaysItIsTheWorkNow(t *testing.T) {
	scheduleInstruction := "매일 이 시간에 주간 보고 알림을 보내줘."

	firingRequest := firingDecisionRequest(scheduleInstruction)
	firingRequest.ToolSet = newTestToolSet([]string{"schedule_create", "message_send"})
	firingQuestions := questionsFor(firingRequest)
	if len(firingQuestions) == 0 {
		t.Fatal("expected the firing request to be asked about")
	}
	firingPreamble := newQuestionBuilder(firingRequest).about("m1")
	if !strings.Contains(firingPreamble, agentcontract.ScheduledRunReading) {
		t.Fatalf("expected the shared reading of a firing in the preamble, got %q", firingPreamble)
	}
	for questionName, question := range firingQuestions {
		if !strings.HasPrefix(question.Instructions, firingPreamble) {
			t.Fatalf("expected %s to open with the firing preamble, got %q", questionName, question.Instructions)
		}
	}

	plainRequest := addressedDecisionRequest(scheduleInstruction)
	plainRequest.ToolSet = newTestToolSet([]string{"schedule_create", "message_send"})
	plainQuestions := questionsFor(plainRequest)
	if len(plainQuestions) != len(firingQuestions) {
		t.Fatalf("expected the same questions either way, got %d without a firing and %d with one", len(plainQuestions), len(firingQuestions))
	}
	plainPreamble := newQuestionBuilder(plainRequest).about("m1")
	for questionName, question := range plainQuestions {
		if !strings.HasPrefix(question.Instructions, plainPreamble) {
			t.Fatalf("expected %s to open with the plain preamble, got %q", questionName, question.Instructions)
		}
		if strings.Contains(question.Instructions, firingMessagePreambleEnding) {
			t.Fatalf("expected %s to say nothing about a firing, got %q", questionName, question.Instructions)
		}
	}
}

func TestTheRouteQuestionDoesNotRepeatTheFiringFact(t *testing.T) {
	scheduleInstruction := "매일 이 시간에 주간 보고 알림을 보내줘."

	firingRoute := criteriaText(t, questionsFor(firingDecisionRequest(scheduleInstruction))["m1."+agentcontract.IntakeQuestionRoute])
	if occurrences := strings.Count(firingRoute, firingMessagePreambleEnding); occurrences != 1 {
		t.Fatalf("expected the firing fact exactly once, got %d in %q", occurrences, firingRoute)
	}
	if !strings.Contains(firingRoute, "activeGoal") {
		t.Fatalf("expected the route question to keep the activeGoal clause, got %q", firingRoute)
	}

	plainRoute := criteriaText(t, questionsFor(addressedDecisionRequest(scheduleInstruction))["m1."+agentcontract.IntakeQuestionRoute])
	if strings.Contains(plainRoute, "scheduledRun") {
		t.Fatalf("expected no mention of scheduledRun without a scheduled run, got %q", plainRoute)
	}
}

func TestTheStateNamesEachMessageTheQuestionsAskAbout(t *testing.T) {
	request := addressedDecisionRequest("보고서 정리해줘")
	request.Messages = append(request.Messages, agentcontract.IntakeDecisionMessage{MessageID: "message-2", Prompt: "표도 넣어줘"})

	state := buildDecisionState(request, nil)

	if len(state.Messages) != 2 || state.Messages[0].ID != "m1" || state.Messages[1].ID != "m2" {
		t.Fatalf("expected the messages to be named m1 and m2, got %+v", state.Messages)
	}
	if !strings.Contains(questionsFor(request)["m2."+agentcontract.IntakeQuestionRoute].Instructions, "message m2") {
		t.Fatal("expected each question to name the message it asks about")
	}
}

func TestTheJointCallAsksOnlyWhatPlanningATurnNeeds(t *testing.T) {
	request := firingDecisionRequest("보고서 다시 보내줘")
	request.ResponseLanguage = ""
	request.PriorTask = agentcontract.PriorTaskContext{TaskRunID: "task-run-0", Prompt: "보고서 정리해줘"}
	request.ToolSet = newTestToolSet([]string{"task_add"})
	planning := map[string]bool{
		agentcontract.IntakeQuestionRoute:                   true,
		agentcontract.IntakeQuestionExpectedToolCount:       true,
		agentcontract.IntakeQuestionHasIndependentWork:      true,
		agentcontract.IntakeQuestionIsExternalSendRequested: true,
		agentcontract.IntakeQuestionTaskShape:               true,
		agentcontract.IntakeQuestionLevel:                   true,
		agentcontract.IntakeQuestionDeliverableKind:         true,
		agentcontract.IntakeQuestionPriorTaskReference:      true,
		agentcontract.IntakeQuestionResponseLanguage:        true,
	}

	for questionName := range questionsFor(request) {
		_, shortName, _ := strings.Cut(questionName, ".")
		isFormat := strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixFormat)
		isTool := strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixTool)
		if !planning[shortName] && !isFormat && !isTool {
			t.Fatalf("%s is not a question about planning the turn; whether to answer, react or interrupt a running task is the host's to ask", questionName)
		}
	}
	document, errorValue := json.Marshal(buildDecisionState(request, nil))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, hostFact := range []string{"standingDuties", "activeTask", "recentlyFinishedTask", "pendingConfirmation", "pendingChoice", "botMentioned"} {
		if strings.Contains(string(document), `"`+hostFact+`"`) {
			t.Fatalf("the planning state carries %q, a fact only the host's gateway questions read", hostFact)
		}
	}
}
