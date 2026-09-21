package intake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func addressedDecisionRequest(prompt string) agentcontract.IntakeDecisionRequest {
	return agentcontract.IntakeDecisionRequest{
		Messages: []agentcontract.IntakeDecisionMessage{{
			MessageID:    "message-1",
			Prompt:       prompt,
			SenderName:   "이샘플",
			SenderHandle: "sample",
			BotMentioned: true,
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
		Addressing: agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true},
		TurnDecision: agentcontract.TurnDecision{
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

func decideOnce(t *testing.T, planner DecisionPlanner, request agentcontract.IntakeDecisionRequest) agentcontract.IntakeMessageDecision {
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

func TestDecisionPlannerDecidesAddressingAndRoutingInOneCallThatNamesNoTool(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	request := addressedDecisionRequest("다음 주 발표자료 초안 만들어줘")
	request.ToolSet = newTestToolSet([]string{"task_add", "task_list"})
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })

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
	if decision.Addressing.Target != agentcontract.AddressingTargetBot || !decision.Addressing.ShouldRespond {
		t.Fatalf("expected an addressed message that wants a reply, got %+v", decision.Addressing)
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
			planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })

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

func TestDecisionPlannerPromotesOnlyClarifyWithToolAndIndependentWork(t *testing.T) {
	testCases := []struct {
		name               string
		route              agentcontract.TurnRoute
		classification     agentcontract.IntakeClassification
		hasIndependentWork bool
		wantClassification agentcontract.IntakeClassification
		wantRoute          agentcontract.TurnRoute
	}{
		{"mixed request", agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationBoundedTask, true, agentcontract.IntakeClassificationBoundedTask, agentcontract.TurnRouteStartTask},
		{"all requested work blocked despite needing a tool", agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationBoundedTask, false, agentcontract.IntakeClassificationNeedsConfirmation, agentcontract.TurnRouteClarify},
		{"independent work needs no tool", agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationQuickReply, true, agentcontract.IntakeClassificationNeedsConfirmation, agentcontract.TurnRouteClarify},
		{"unsupported request despite needing a tool", agentcontract.TurnRouteGiveUp, agentcontract.IntakeClassificationBoundedTask, true, agentcontract.IntakeClassificationUnsupported, agentcontract.TurnRouteGiveUp},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			outcome := startTaskOutcome()
			outcome.TurnDecision.Route = testCase.route
			outcome.TurnDecision.Classification = testCase.classification
			outcome.TurnDecision.HasIndependentWork = testCase.hasIndependentWork
			decisionModel := intaketest.NewDecisionModel(outcome)
			decision := decideOnce(t, NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }), addressedDecisionRequest("finish the clear part and ask me about the unresolved part"))

			if decision.TurnFields.Classification != testCase.wantClassification {
				t.Fatalf("expected classification %q, got %q", testCase.wantClassification, decision.TurnFields.Classification)
			}
			if decision.TurnFields.HasIndependentWork != testCase.hasIndependentWork {
				t.Fatalf("expected independent work %t, got %t", testCase.hasIndependentWork, decision.TurnFields.HasIndependentWork)
			}
			if decision.TurnFields.RawDecisionRoute != testCase.route {
				t.Fatalf("expected raw route %q, got %q", testCase.route, decision.TurnFields.RawDecisionRoute)
			}
			normalizedDecision, errorValue := normalizeTurnDecision(decision.TurnFields, agentcontract.AgentRequest{})
			if errorValue != nil {
				t.Fatalf("expected the turn decision to normalize: %v", errorValue)
			}
			if normalizedDecision.Route != testCase.wantRoute {
				t.Fatalf("expected normalized route %q, got %q", testCase.wantRoute, normalizedDecision.Route)
			}
		})
	}
}

func TestDecisionPlannerLeavesAFollowUpUnaskedWithoutATask(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })

	decision := decideOnce(t, planner, addressedDecisionRequest("보고서 정리해줘"))

	if decision.HasRelatesToActiveTask {
		t.Fatal("expected no follow-up answer when no task is running")
	}
	if _, isAsked := decisionModel.Requests()[0].Questions["m1."+agentcontract.IntakeQuestionRelatesToActiveTask]; isAsked {
		t.Fatal("expected the follow-up question to be left out when no task is running")
	}
}

func TestDecisionPlannerAsksTheFollowUpQuestionForARunningTask(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.RelatesToActiveTask = true
	outcome.TurnDecision.BusyRoute = agentcontract.BusyRouteSteer
	decisionModel := intaketest.NewDecisionModel(outcome)
	request := addressedDecisionRequest("아까 그거 표 말고 그래프로 해줘")
	request.ActiveTask = agentcontract.ActiveTaskContext{TaskRunID: "task-run-1", Prompt: "보고서 정리", Status: "running"}
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })

	decision := decideOnce(t, planner, request)

	if !decision.HasRelatesToActiveTask || !decision.RelatesToActiveTask {
		t.Fatalf("expected the follow-up answer to be read, got %+v", decision)
	}
	if decision.TurnFields.BusyRoute != agentcontract.BusyRouteSteer {
		t.Fatalf("expected the busy route to be read, got %q", decision.TurnFields.BusyRoute)
	}
}

func TestDecisionPlannerReadsAPendingChoiceSelection(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.PendingChoiceKeys = []string{"1", "2"}
	outcome.TurnDecision.Choices = []string{"2"}
	decisionModel := intaketest.NewDecisionModel(outcome)
	request := addressedDecisionRequest("두 번째로 해줘")
	request.PendingChoice = agentcontract.PendingChoiceContext{
		TaskRunID: "task-run-1",
		Question:  "어떤 형식으로 드릴까요?",
		Options:   []agentcontract.ChoiceReplyOption{{Key: "1", Label: "표"}, {Key: "2", Label: "그래프"}},
	}
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })

	decision := decideOnce(t, planner, request)

	if !containsString(decision.TurnFields.Choices, "2") {
		t.Fatalf("expected the selected choice key, got %+v", decision.TurnFields.Choices)
	}
	if containsString(decision.TurnFields.Choices, "1") {
		t.Fatalf("expected only the selected option, got %+v", decision.TurnFields.Choices)
	}

	plainPlanner := NewDecisionPlanner(intaketest.NewDecisionModel(startTaskOutcome()), nil, func() float64 { return 1 })
	if plainDecision := decideOnce(t, plainPlanner, addressedDecisionRequest("두 번째로 해줘")); len(plainDecision.TurnFields.Choices) != 0 {
		t.Fatalf("expected no selection without a pending choice, got %+v", plainDecision.TurnFields.Choices)
	}
}

func reactionScript(reactProbability float64) *model.ScriptedDecisionModel {
	return &model.ScriptedDecisionModel{
		AnswerFor: func(questionName string, question model.DecisionQuestion) (model.DecisionAnswer, bool) {
			switch {
			case strings.HasSuffix(questionName, "."+agentcontract.IntakeQuestionReaction):
				return model.DecisionAnswer{
					Type:          model.DecisionQuestionTypeChoice,
					Choice:        agentcontract.IntakeReactionOptionNone,
					Probabilities: map[string]float64{agentcontract.IntakeReactionOptionNone: 1 - reactProbability, agentcontract.IntakeReactionOptionReact: reactProbability},
				}, true
			case strings.HasSuffix(questionName, "."+agentcontract.IntakeQuestionReactionEmoji):
				return model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: "white_check_mark"}, true
			}
			return intaketest.Answers(map[string]model.DecisionQuestion{questionName: question}, func(string) intaketest.Outcome {
				return startTaskOutcome()
			})[questionName], true
		},
	}
}

func TestDecisionPlannerDrawsTheReactionFromItsProbability(t *testing.T) {
	reactingPlanner := NewDecisionPlanner(reactionScript(0.1), nil, func() float64 { return 0.05 })
	reacting := decideOnce(t, reactingPlanner, addressedDecisionRequest("배포 끝났습니다"))
	if reacting.Addressing.ReactionEmoji != "white_check_mark" {
		t.Fatalf("expected a draw under the probability to react, got %q", reacting.Addressing.ReactionEmoji)
	}
	if reacting.ReactionProbability != 0.1 || reacting.ReactionDraw != 0.05 {
		t.Fatalf("expected the probability and the draw to be recorded, got %+v", reacting)
	}

	silentPlanner := NewDecisionPlanner(reactionScript(0.1), nil, func() float64 { return 0.5 })
	silent := decideOnce(t, silentPlanner, addressedDecisionRequest("배포 끝났습니다"))
	if silent.Addressing.ReactionEmoji != "" {
		t.Fatalf("expected a draw above the probability to stay silent, got %q", silent.Addressing.ReactionEmoji)
	}
}

func TestDecisionPlannerFailsWithoutADecisionModel(t *testing.T) {
	_, errorValue := DecisionPlanner{}.Decide(context.Background(), addressedDecisionRequest("안녕"), nil)
	if errorValue != ErrDecisionModelUnavailable {
		t.Fatalf("expected the unavailable-model error, got %v", errorValue)
	}
}

func TestDecisionPlannerRecordsTheCallInTheIntakeLedger(t *testing.T) {
	planner := NewDecisionPlanner(intaketest.NewDecisionModel(startTaskOutcome()), nil, func() float64 { return 1 })
	callLedger := &agentcontract.IntakeCallLedger{}

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
	if record.DecidedMessageCount != 1 || record.QuestionCount == 0 {
		t.Fatalf("expected the decided message and question counts, got %+v", record)
	}
	if record.AttachmentsDescribed {
		t.Fatal("expected no described attachments without a describer")
	}
	if _, isRecorded := record.DecisionAnswers["m1."+agentcontract.IntakeQuestionRoute]; !isRecorded {
		t.Fatalf("expected the route distribution in the record, got %+v", record.DecisionAnswers)
	}
}

func TestDecisionPlannerDecidesEveryMessageOfABurstInOneCall(t *testing.T) {
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	request := addressedDecisionRequest("보고서 정리해줘")
	request.Messages = append(request.Messages, agentcontract.IntakeDecisionMessage{MessageID: "message-2", Prompt: "아 그리고 표도 넣어줘", SenderName: "이샘플"})
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })

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
	if _, isAsked := questions["m2."+agentcontract.IntakeQuestionRoute]; !isAsked {
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

func imageDecisionRequest(prompt string) agentcontract.IntakeDecisionRequest {
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
	planner := NewDecisionPlanner(decisionModel, describer, func() float64 { return 1 })
	callLedger := &agentcontract.IntakeCallLedger{}

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
	planner := NewDecisionPlanner(decisionModel, describer, func() float64 { return 1 })

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
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })
	callLedger := &agentcontract.IntakeCallLedger{}

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
	droppedQuestionKey := "m1." + agentcontract.IntakeQuestionRoute
	planner := NewDecisionPlanner(answerDroppingDecisionModel{outcome: startTaskOutcome(), droppedQuestionKey: droppedQuestionKey}, nil, func() float64 { return 1 })

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

func TestDecisionPlannerReadsNoneOfTheseAsNoSelection(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.PendingChoiceKeys = []string{"1", "2"}
	decisionModel := intaketest.NewDecisionModel(outcome)
	request := addressedDecisionRequest("둘 다 아니에요")
	request.PendingChoice = agentcontract.PendingChoiceContext{
		TaskRunID: "task-run-1",
		Question:  "어떤 형식으로 드릴까요?",
		Options:   []agentcontract.ChoiceReplyOption{{Key: "1", Label: "표"}, {Key: "2", Label: "그래프"}},
	}
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })

	decision := decideOnce(t, planner, request)

	if len(decision.TurnFields.Choices) != 0 {
		t.Fatalf("expected none_of_these to select nothing, got %+v", decision.TurnFields.Choices)
	}
}

func TestDecisionPlannerReadsAMultipleChoiceReplyWithoutAYesAsNoSelection(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.PendingChoiceKeys = []string{"1", "2"}
	decisionModel := intaketest.NewDecisionModel(outcome)
	request := addressedDecisionRequest("둘 다 아니에요")
	request.PendingChoice = agentcontract.PendingChoiceContext{
		TaskRunID:     "task-run-1",
		Question:      "어떤 형식으로 드릴까요?",
		SelectionMode: "multiple",
		Options:       []agentcontract.ChoiceReplyOption{{Key: "1", Label: "표"}, {Key: "2", Label: "그래프"}},
	}
	planner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })

	decision := decideOnce(t, planner, request)

	if len(decision.TurnFields.Choices) != 0 {
		t.Fatalf("expected no selection when every option is answered no, got %+v", decision.TurnFields.Choices)
	}
}
