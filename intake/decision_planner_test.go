package intake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/llmcalls"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func addressedDecisionRequest(prompt string) turnclassification.IntakeDecisionRequest {
	return turnclassification.IntakeDecisionRequest{
		Messages: []turnclassification.IntakeDecisionMessage{{
			MessageID:    "message-1",
			Prompt:       prompt,
			SenderName:   "이샘플",
			SenderHandle: "sample",
			SentAt:       time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC),
		}},
		ConversationType: "channel",
		AgentIdentity:    agentcontract.AgentIdentity{Name: "인턴"},
		Company:          agentcontract.CompanyContext{Name: "여명거리", TimeZone: "UTC"},
		ResponseLanguage: "ko",
		EnvironmentNow:   time.Date(2026, 9, 18, 10, 0, 1, 0, time.UTC),
	}
}

func startTaskOutcome() intaketest.Outcome {
	return intaketest.Outcome{
		TurnDecision: turnclassification.TurnDecision{
			Route:                  agentcontract.TurnRouteStartTask,
			Classification:         agentcontract.IntakeClassificationBoundedTask,
			TaskShape:              agentcontract.TaskShapeResearchTask,
			TaskLevel:              agentcontract.TaskLevelMedium,
			DeliverableKind:        agentcontract.DeliverableKindNone,
			ResponseLanguage:       "ko",
			PriorTaskReference:     agentcontract.PriorTaskReferenceNone,
			InitialToolNames:       []string{"task_add"},
			RequestedOutputFormats: []string{"pptx"},
		},
	}
}

func decideOnce(t *testing.T, planner DecisionPlanner, request turnclassification.IntakeDecisionRequest) turnclassification.IntakeMessageDecision {
	t.Helper()
	decisions, errorValue := planner.Decide(context.Background(), request, nil)
	if errorValue != nil {
		t.Fatalf("expected the decision call to answer: %v", errorValue)
	}
	if len(decisions.Messages) != 1 {
		t.Fatalf("expected one decided message, got %d", len(decisions.Messages))
	}
	return decisions.Messages[0]
}

func TestDecisionPlannerDecidesRoutingInOneCallThatNamesNoTool(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	request := addressedDecisionRequest("다음 주 발표자료 초안 만들어줘")
	request.ToolSet = newTestToolSet([]string{"task_add", "task_list"})
	planner := NewDecisionPlanner(decisionModel, nil)

	decision := decideOnce(t, planner, request)

	intakeRequest := decisionModel.Requests()[0]
	for questionName := range intakeRequest.Questions {
		if _, isToolQuestion := toolNameOfQuestion(questionName); isToolQuestion {
			t.Fatalf("expected routing to be decided without a tool question, got %s", questionName)
		}
	}
	intakeState, isDecisionState := intakeRequest.State.(decisionState)
	if !isDecisionState {
		t.Fatalf("expected a decision state, got %T", intakeRequest.State)
	}
	if len(intakeState.AvailableTools) != 0 || intakeState.ToolGuidance != "" {
		t.Fatalf("expected no tool description in the routing call, got %+v", intakeState.AvailableTools)
	}
	if decision.MessageID != "message-1" {
		t.Fatalf("expected the decision to carry the message identifier, got %q", decision.MessageID)
	}
	if decision.TurnFields.Route != agentcontract.TurnRouteStartTask {
		t.Fatalf("expected the start_task route, got %q", decision.TurnFields.Route)
	}
	if decision.TurnFields.TaskLevel != agentcontract.TaskLevelMedium {
		t.Fatalf("expected the task level to be read by argmax, got %q", decision.TurnFields.TaskLevel)
	}
	if !containsString(decision.TurnFields.InitialToolNames, "task_add") || containsString(decision.TurnFields.InitialToolNames, "task_list") {
		t.Fatalf("expected only the yes tools, got %+v", decision.TurnFields.InitialToolNames)
	}
	if !containsString(decision.TurnFields.RequestedOutputFormats, "pptx") {
		t.Fatalf("expected the requested output format, got %+v", decision.TurnFields.RequestedOutputFormats)
	}
}

func TestDecisionPlannerReadsExternalSendIntentAsNoul(t *testing.T) {
	for _, testCase := range []struct {
		name                    string
		isExternalSendRequested bool
		initialToolNames        []string
	}{
		{name: "requested send", isExternalSendRequested: true, initialToolNames: []string{"message_send"}},
		{name: "available send tool without request", initialToolNames: []string{"message_send"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			outcome := startTaskOutcome()
			outcome.TurnDecision.IsExternalSendRequested = testCase.isExternalSendRequested
			outcome.TurnDecision.InitialToolNames = testCase.initialToolNames
			decisionModel := intaketest.NewDecisionModel(outcome)
			planner := NewDecisionPlanner(decisionModel, nil)

			decision := decideOnce(t, planner, addressedDecisionRequest("complete the requested work"))
			if decision.TurnFields.IsExternalSendRequested != testCase.isExternalSendRequested {
				t.Fatalf("expected explicit send intent %t, got %+v", testCase.isExternalSendRequested, decision.TurnFields)
			}
			requests := decisionModel.Requests()
			if len(requests) != 1 {
				t.Fatalf("expected one batched decision call, got %d", len(requests))
			}
			question, isAsked := requests[0].Questions["m1."+agentcontract.IntakeQuestionIsExternalSendRequested]
			if !isAsked || question.Type != model.DecisionQuestionTypeNoul {
				t.Fatalf("expected the external-send intent to be a batched Noul question, got %+v", question)
			}
		})
	}
}

func TestThePlannerDerivesTheTurnFromTheJudgedWork(t *testing.T) {
	testCases := []struct {
		name               string
		work               agentcontract.Work
		needsClarification bool
		wantRoute          agentcontract.TurnRoute
		wantClassification agentcontract.IntakeClassification
		wantLevel          agentcontract.TaskLevel
	}{
		{"no work is answered in words", agentcontract.WorkNone, false, agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply, agentcontract.TaskLevelLow},
		{"impossible work is declined", agentcontract.WorkImpossible, false, agentcontract.TurnRouteGiveUp, agentcontract.IntakeClassificationUnsupported, agentcontract.TaskLevelLow},
		{"easy work starts at the low level", agentcontract.WorkEasy, false, agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationBoundedTask, agentcontract.TaskLevelLow},
		{"hard work starts at the high level", agentcontract.WorkHard, false, agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationBoundedTask, agentcontract.TaskLevelHigh},
		{"work nothing of which can proceed is asked about", agentcontract.WorkNormal, true, agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationNeedsConfirmation, agentcontract.TaskLevelMedium},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			outcome := startTaskOutcome()
			outcome.WorkProbabilities = map[string]float64{string(testCase.work): 1}
			if testCase.needsClarification {
				outcome.TurnDecision.Classification = agentcontract.IntakeClassificationNeedsConfirmation
			}
			decision := decideOnce(t, NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil), addressedDecisionRequest("이번 주 회의록 정리해줘"))

			if decision.TurnFields.Route != testCase.wantRoute || decision.TurnFields.Classification != testCase.wantClassification {
				t.Fatalf("expected %q/%q, got %q/%q", testCase.wantRoute, testCase.wantClassification, decision.TurnFields.Route, decision.TurnFields.Classification)
			}
			if decision.TurnFields.TaskLevel != testCase.wantLevel {
				t.Fatalf("expected level %q, got %q", testCase.wantLevel, decision.TurnFields.TaskLevel)
			}
			if decision.TurnFields.HasIndependentWork == testCase.needsClarification && testCase.work.IsDoable() {
				t.Fatalf("expected independent work to be the opposite of needing clarification, got %t", decision.TurnFields.HasIndependentWork)
			}
		})
	}
}

func TestTheRelationToAnActiveGoalIsAskedOnlyWhenThereIsOne(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.TurnDecision.Route = agentcontract.TurnRouteReviseTask
	request := addressedDecisionRequest("아 그거 말고 3월 걸로")
	request.ActiveGoal = agentcontract.ActiveGoal{OriginalInstruction: "2월 매출 정리"}
	decision := decideOnce(t, NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil), request)
	if decision.TurnFields.Route != agentcontract.TurnRouteReviseTask {
		t.Fatalf("expected the relation to the goal to set the route, got %q", decision.TurnFields.Route)
	}

	questions := questionsFor(addressedDecisionRequest("2월 매출 정리해줘"))
	if _, isAsked := questions["m1."+agentcontract.IntakeQuestionRelation]; isAsked {
		t.Fatal("expected no relation question without an active goal")
	}
}

func TestDecidedWorkIsTakenAsAFact(t *testing.T) {
	request := addressedDecisionRequest("포틀랜드 기준 23시 퇴근 찍어줘")
	request.DecidedWork = agentcontract.WorkEasy
	if _, isAsked := questionsFor(request)["m1."+agentcontract.IntakeQuestionWork]; isAsked {
		t.Fatal("expected the planner not to ask work the host already judged")
	}
	outcome := startTaskOutcome()
	outcome.TurnDecision.Classification = agentcontract.IntakeClassificationUnsupported
	decision := decideOnce(t, NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil), request)
	if decision.TurnFields.Route != agentcontract.TurnRouteStartTask || decision.TurnFields.TaskLevel != agentcontract.TaskLevelLow {
		t.Fatalf("expected the host's easy work to start at the low level, got %q at %q", decision.TurnFields.Route, decision.TurnFields.TaskLevel)
	}
}

func TestWorkThatIsNotDoableAsksTheModelNothing(t *testing.T) {
	for _, work := range []agentcontract.Work{agentcontract.WorkNone, agentcontract.WorkImpossible} {
		decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
		request := addressedDecisionRequest("고마워")
		request.DecidedWork = work
		decideOnce(t, NewDecisionPlanner(decisionModel, nil), request)
		if requests := decisionModel.Requests(); len(requests) != 0 {
			t.Fatalf("%s: expected no decision call, got %d", work, len(requests))
		}
	}
}

func TestDecisionPlannerFailsWithoutADecisionModel(t *testing.T) {
	_, errorValue := DecisionPlanner{}.Decide(context.Background(), addressedDecisionRequest("안녕"), nil)
	if errorValue != ErrDecisionModelUnavailable {
		t.Fatalf("expected the unavailable-model error, got %v", errorValue)
	}
}

func TestDecisionPlannerRecordsTheCallInTheIntakeLedger(t *testing.T) {
	planner := NewDecisionPlanner(intaketest.NewDecisionModel(startTaskOutcome()), nil)
	callLedger := &llmcalls.IntakeCallLedger{SchemaNames: llmcalls.IntakeSchemaNames}

	if _, errorValue := planner.Decide(context.Background(), addressedDecisionRequest("보고서 정리해줘"), callLedger); errorValue != nil {
		t.Fatalf("expected the decision call to answer: %v", errorValue)
	}

	if len(callLedger.Records) != 1 {
		t.Fatalf("expected one ledger record, got %d", len(callLedger.Records))
	}
	record := callLedger.Records[0]
	if record.Kind != agentcontract.LLMCallKindDecision {
		t.Fatalf("expected a decision record, got %q", record.Kind)
	}
	if len(record.DecidedMessageIDs) != 1 || record.QuestionCount == 0 {
		t.Fatalf("expected the decided message and question counts, got %+v", record)
	}
	if record.AttachmentsDescribed {
		t.Fatal("expected no described attachments without a describer")
	}
	if _, isRecorded := record.DecisionAnswers["m1."+agentcontract.IntakeQuestionWork]; !isRecorded {
		t.Fatalf("expected the work distribution in the record, got %+v", record.DecisionAnswers)
	}
	if !strings.Contains(string(record.Input), "보고서 정리해줘") {
		t.Fatalf("expected the record to keep the request the planner was given, got %s", record.Input)
	}
}

func TestDecisionPlannerDecidesEveryMessageOfABurstInOneCall(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	request := addressedDecisionRequest("보고서 정리해줘")
	request.Messages = append(request.Messages, turnclassification.IntakeDecisionMessage{MessageID: "message-2", Prompt: "아 그리고 표도 넣어줘", SenderName: "이샘플"})
	planner := NewDecisionPlanner(decisionModel, nil)

	decisions, errorValue := planner.Decide(context.Background(), request, nil)
	if errorValue != nil {
		t.Fatalf("expected the decision call to answer: %v", errorValue)
	}

	if len(decisionModel.Requests()) != 1 {
		t.Fatalf("expected one call for the whole burst, got %d", len(decisionModel.Requests()))
	}
	if len(decisions.Messages) != 2 {
		t.Fatalf("expected both messages decided, got %d", len(decisions.Messages))
	}
	if _, isFound := decisions.ForMessage("message-2"); !isFound {
		t.Fatal("expected the second message to be addressable by its identifier")
	}
	questions := decisionModel.Requests()[0].Questions
	if _, isAsked := questions["m2."+agentcontract.IntakeQuestionWork]; !isAsked {
		t.Fatal("expected the question set to repeat per message")
	}
}

func decisionStateOf(t *testing.T, request model.DecisionRequest) map[string]any {
	t.Helper()
	document, errorValue := json.Marshal(request.State)
	if errorValue != nil {
		t.Fatalf("expected the state to marshal: %v", errorValue)
	}
	var state map[string]any
	if errorValue := json.Unmarshal(document, &state); errorValue != nil {
		t.Fatalf("expected the state to parse: %v", errorValue)
	}
	return state
}

func firstStateAttachment(t *testing.T, request model.DecisionRequest) map[string]any {
	t.Helper()
	state := decisionStateOf(t, request)
	messages, isList := state["messages"].([]any)
	if !isList || len(messages) == 0 {
		t.Fatalf("expected messages in the state, got %+v", state["messages"])
	}
	message, isObject := messages[0].(map[string]any)
	if !isObject {
		t.Fatalf("expected a message object, got %+v", messages[0])
	}
	attachments, isList := message["attachments"].([]any)
	if !isList || len(attachments) == 0 {
		t.Fatalf("expected attachment facts in the state, got %+v", message["attachments"])
	}
	attachment, isObject := attachments[0].(map[string]any)
	if !isObject {
		t.Fatalf("expected an attachment object, got %+v", attachments[0])
	}
	return attachment
}

type scriptedAttachmentDescriber struct {
	descriptions []string
	callCount    int
}

func (describer *scriptedAttachmentDescriber) DescribeAttachments(context.Context, []agentcontract.AgentPart) ([]string, error) {
	describer.callCount++
	return describer.descriptions, nil
}

func imageDecisionRequest(prompt string) turnclassification.IntakeDecisionRequest {
	request := addressedDecisionRequest(prompt)
	imagePart := agentcontract.AgentPart{
		Type:  agentcontract.AgentPartTypeImage,
		Image: &agentcontract.AgentImagePart{MimeType: "image/png", Filename: "board.png", DataBase64: "aGVsbG8="},
	}
	request.Messages[0].InputParts = []agentcontract.AgentPart{imagePart}
	request.Messages[0].Attachments = agentcontract.AttachmentFactsFromParts([]agentcontract.AgentPart{imagePart})
	request.Messages[0].IsAttachmentsOnly = strings.TrimSpace(prompt) == ""
	return request
}

func TestDecisionPlannerDescribesAnAttachmentsOnlyMessageBeforeDeciding(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	describer := &scriptedAttachmentDescriber{descriptions: []string{"화이트보드에 적힌 다음 주 배포 일정 사진."}}
	planner := NewDecisionPlanner(decisionModel, describer)
	callLedger := &llmcalls.IntakeCallLedger{SchemaNames: llmcalls.IntakeSchemaNames}

	if _, errorValue := planner.Decide(context.Background(), imageDecisionRequest(""), callLedger); errorValue != nil {
		t.Fatalf("expected the decision call to answer: %v", errorValue)
	}

	if describer.callCount != 1 {
		t.Fatalf("expected one describing call, got %d", describer.callCount)
	}
	attachment := firstStateAttachment(t, decisionModel.Requests()[0])
	if attachment["description"] != "화이트보드에 적힌 다음 주 배포 일정 사진." {
		t.Fatalf("expected the description in the state, got %+v", attachment)
	}
	if attachment["kind"] != "image" || attachment["mimeType"] != "image/png" {
		t.Fatalf("expected attachment facts, got %+v", attachment)
	}
	if _, carriesBytes := attachment["dataBase64"]; carriesBytes {
		t.Fatalf("expected facts without bytes, got %+v", attachment)
	}
	if !callLedger.Records[0].AttachmentsDescribed {
		t.Fatal("expected the ledger to say the attachment was described")
	}
	if len(callLedger.Records[0].AttachmentDescriptions) != 1 {
		t.Fatalf("expected the description in the ledger, got %+v", callLedger.Records[0].AttachmentDescriptions)
	}
}

func TestDecisionPlannerDecidesAnImageWithTextFromFactsAlone(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	describer := &scriptedAttachmentDescriber{descriptions: []string{"설명"}}
	planner := NewDecisionPlanner(decisionModel, describer)

	if _, errorValue := planner.Decide(context.Background(), imageDecisionRequest("이거 정리해줘"), nil); errorValue != nil {
		t.Fatalf("expected the decision call to answer: %v", errorValue)
	}

	if describer.callCount != 0 {
		t.Fatalf("expected no vision call for a message that carries its own text, got %d", describer.callCount)
	}
	attachment := firstStateAttachment(t, decisionModel.Requests()[0])
	if _, isDescribed := attachment["description"]; isDescribed {
		t.Fatalf("expected facts without a description, got %+v", attachment)
	}
}

func TestDecisionPlannerSendsFactsAloneWithoutADescriber(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	planner := NewDecisionPlanner(decisionModel, nil)
	callLedger := &llmcalls.IntakeCallLedger{SchemaNames: llmcalls.IntakeSchemaNames}

	if _, errorValue := planner.Decide(context.Background(), imageDecisionRequest(""), callLedger); errorValue != nil {
		t.Fatalf("expected the decision call to answer: %v", errorValue)
	}

	attachment := firstStateAttachment(t, decisionModel.Requests()[0])
	if _, isDescribed := attachment["description"]; isDescribed {
		t.Fatalf("expected facts without a description, got %+v", attachment)
	}
	if callLedger.Records[0].AttachmentsDescribed {
		t.Fatal("expected the ledger to say nothing was described")
	}
}

func TestDecisionPlannerFailsWhenAnAskedQuestionIsUnanswered(t *testing.T) {
	request := addressedDecisionRequest("발표자료 초안 만들어줘")
	request.ToolSet = newTestToolSet([]string{"task_add", "task_list"})
	droppedQuestionKey := "m1." + agentcontract.IntakeQuestionWork
	planner := NewDecisionPlanner(answerDroppingDecisionModel{outcome: startTaskOutcome(), droppedQuestionKey: droppedQuestionKey}, nil)

	_, errorValue := planner.Decide(context.Background(), request, nil)

	if errorValue == nil || !strings.Contains(errorValue.Error(), droppedQuestionKey) {
		t.Fatalf("expected the unanswered question to be named, got %v", errorValue)
	}
}

type answerDroppingDecisionModel struct {
	outcome            intaketest.Outcome
	droppedQuestionKey string
}

func (decisionModel answerDroppingDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	answers := intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return decisionModel.outcome })
	delete(answers, decisionModel.droppedQuestionKey)
	return model.DecisionResponse{Answers: answers, ModelName: "answer-dropping"}, nil
}

func TestDecisionPlannerAsksTheLanguageOnlyWhenTheHostNamesNone(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.TurnDecision.ResponseLanguage = "ja"
	for _, hostLanguage := range []string{"", "en"} {
		decisionModel := intaketest.NewDecisionModel(outcome)
		request := addressedDecisionRequest("今パリは何時ですか")
		request.ResponseLanguage = hostLanguage
		planner := NewDecisionPlanner(decisionModel, nil)

		decision := decideOnce(t, planner, request)

		wasAsked := false
		for _, decisionRequest := range decisionModel.Requests() {
			for questionKey := range decisionRequest.Questions {
				wasAsked = wasAsked || strings.HasSuffix(questionKey, agentcontract.IntakeQuestionResponseLanguage)
			}
		}
		expectedLanguage := map[string]string{"": "ja", "en": ""}[hostLanguage]
		if wasAsked != (hostLanguage == "") || decision.TurnFields.ResponseLanguage != expectedLanguage {
			t.Fatalf("host language %q: asked %v, reply language %q, want asked %v and %q", hostLanguage, wasAsked, decision.TurnFields.ResponseLanguage, hostLanguage == "", expectedLanguage)
		}
	}
}

func TestAScheduledFiringIsWorkByTheFactThatItFired(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.WorkProbabilities = map[string]float64{"none": 0.8, "easy": 0.15, "normal": 0.05}
	decision := decideOnce(t, NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil), firingDecisionRequest("매주 월요일 주간 보고 알림 보내줘"))

	if decision.TurnFields.Route != agentcontract.TurnRouteStartTask || decision.TurnFields.TaskLevel != agentcontract.TaskLevelLow {
		t.Fatalf("expected the firing to start work at its likeliest level, got %q at %q", decision.TurnFields.Route, decision.TurnFields.TaskLevel)
	}
	if _, isAsked := questionsFor(firingDecisionRequest("매주 월요일 주간 보고 알림 보내줘"))["m1."+agentcontract.IntakeQuestionClarify]; isAsked {
		t.Fatal("expected a firing, which has nobody to ask, not to be asked whether to clarify")
	}
}

func TestAMessageThatCarriesAnActiveGoalOnIsWork(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.TurnDecision.Route = agentcontract.TurnRouteContinueTask
	outcome.WorkProbabilities = map[string]float64{"none": 0.9, "easy": 0.1}
	request := addressedDecisionRequest("응 그렇게 해줘")
	request.ActiveGoal = agentcontract.ActiveGoal{OriginalInstruction: "다음 주 회의 잡아줘", Status: agentcontract.ActiveGoalStatusBlocked}
	decision := decideOnce(t, NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil), request)

	if decision.TurnFields.Route != agentcontract.TurnRouteContinueTask {
		t.Fatalf("expected a yes to a waiting goal to continue it, got %q", decision.TurnFields.Route)
	}
}
