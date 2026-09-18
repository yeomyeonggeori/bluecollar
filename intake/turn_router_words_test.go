package intake

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func enabledIntakeOptions() agentcontract.IntakeOptions {
	return agentcontract.IntakeOptions{IsEnabled: true}
}

func turnRouterWith(languageModel *sequenceLanguageModel, outcome intaketest.Outcome) TurnRouter {
	decisionPlanner := NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil, func() float64 { return 1 })
	return NewTurnRouter(languageModel, decisionPlanner, enabledIntakeOptions())
}

func clarifyOutcome() intaketest.Outcome {
	return intaketest.Outcome{
		Addressing: agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true},
		TurnDecision: agentcontract.TurnDecision{
			Route:              agentcontract.TurnRouteClarify,
			Classification:     agentcontract.IntakeClassificationNeedsConfirmation,
			TaskShape:          agentcontract.TaskShapeApprovalGatedTask,
			TaskLevel:          agentcontract.TaskLevelLow,
			DeliverableKind:    agentcontract.DeliverableKindNone,
			ResponseLanguage:   "ko",
			PriorTaskReference: agentcontract.PriorTaskReferenceNone,
		},
	}
}

func TestTurnRouterAsksAWorkRouteOnlyForItsAcceptanceContract(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"expectedResults":[{"id":"deck","type":"file","description":"초안 덱 파일 하나","required":true,"acceptanceHints":[".pptx"]}]}`,
	}}
	turnRouter := turnRouterWith(languageModel, startTaskOutcome())

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "다음 주 발표자료 초안 만들어줘", ResponseLanguage: "ko"})
	if errorValue != nil {
		t.Fatalf("expected a routed turn: %v", errorValue)
	}

	if decision.Route != agentcontract.TurnRouteStartTask {
		t.Fatalf("expected the start_task route, got %q", decision.Route)
	}
	if len(decision.ExpectedResults) == 0 || decision.ExpectedResults[0].ID != "deck" {
		t.Fatalf("expected the acceptance contract to be written, got %+v", decision.ExpectedResults)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected exactly one chat call for a work route, got %d", len(languageModel.requests))
	}
	schemaDocument := languageModel.requests[0].StructuredOutputSchema.Document
	for _, absentField := range []string{"userFacingReply", "clarificationQuestion", "busyInstruction", "\"route\"", "\"level\""} {
		if strings.Contains(schemaDocument, absentField) {
			t.Fatalf("expected %s to be gone from the acceptance schema, got %s", absentField, schemaDocument)
		}
	}
}

func TestTurnRouterWritesNothingForAConsumedTurn(t *testing.T) {
	languageModel := &sequenceLanguageModel{}
	outcome := startTaskOutcome()
	outcome.TurnDecision.Route = agentcontract.TurnRouteConsume
	outcome.TurnDecision.Classification = agentcontract.IntakeClassificationQuickReply
	outcome.TurnDecision.TaskShape = agentcontract.TaskShapeImmediateReply
	outcome.TurnDecision.InitialToolNames = nil
	outcome.TurnDecision.RequestedOutputFormats = nil
	turnRouter := turnRouterWith(languageModel, outcome)

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "고마워!", ResponseLanguage: "ko"})
	if errorValue != nil {
		t.Fatalf("expected a routed turn: %v", errorValue)
	}

	if decision.Route != agentcontract.TurnRouteConsume {
		t.Fatalf("expected the consume route, got %q", decision.Route)
	}
	if len(languageModel.requests) != 0 {
		t.Fatalf("expected no chat call for a consumed turn, got %d", len(languageModel.requests))
	}
}

func TestTurnRouterAsksTheChatModelOnlyForTheWords(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"reason":"missing the deadline","userFacingReply":"","clarificationQuestion":"언제까지 필요하세요?","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
	}}
	turnRouter := turnRouterWith(languageModel, clarifyOutcome())

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "보고서 하나 만들어줘", ResponseLanguage: "ko"})
	if errorValue != nil {
		t.Fatalf("expected a clarify turn: %v", errorValue)
	}

	if decision.ClarificationQuestion != "언제까지 필요하세요?" {
		t.Fatalf("expected the written clarification question, got %q", decision.ClarificationQuestion)
	}
	if decision.Route != agentcontract.TurnRouteClarify {
		t.Fatalf("expected the decided route to survive the words call, got %q", decision.Route)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected exactly one chat call, got %d", len(languageModel.requests))
	}
	schemaDocument := languageModel.requests[0].StructuredOutputSchema.Document
	for _, closedField := range []string{"\"route\"", "\"classification\"", "\"level\"", "\"initialToolNames\""} {
		if strings.Contains(schemaDocument, closedField) {
			t.Fatalf("expected %s to be gone from the words schema", closedField)
		}
	}
	if !strings.Contains(schemaDocument, "clarificationQuestion") {
		t.Fatalf("expected the words schema to ask for the clarification question, got %s", schemaDocument)
	}
}

func TestTurnRouterHandsTheDecidedFieldsToTheWordsCallAsFacts(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"reason":"","userFacingReply":"","clarificationQuestion":"어느 팀 기준으로 볼까요?","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
	}}
	turnRouter := turnRouterWith(languageModel, clarifyOutcome())

	if _, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "정리해줘", ResponseLanguage: "ko"}); errorValue != nil {
		t.Fatalf("expected a clarify turn: %v", errorValue)
	}

	systemContent := ""
	for _, message := range languageModel.requests[0].Messages {
		if message.Role == "system" {
			systemContent += message.Content + "\n"
		}
	}
	if !strings.Contains(systemContent, string(agentcontract.TurnRouteClarify)) {
		t.Fatalf("expected the decided route to be handed over as a fact, got %s", systemContent)
	}
	if !strings.Contains(systemContent, string(agentcontract.TaskLevelLow)) {
		t.Fatalf("expected the decided level to be handed over as a fact, got %s", systemContent)
	}
}

func TestTurnRouterCarriesTheImageIntoTheWordsCall(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"reason":"","userFacingReply":"화이트보드에 적힌 일정이 보이네요.","clarificationQuestion":"","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
	}}
	outcome := clarifyOutcome()
	outcome.TurnDecision.Route = agentcontract.TurnRouteAnswerQuestion
	outcome.TurnDecision.Classification = agentcontract.IntakeClassificationQuickReply
	outcome.TurnDecision.TaskShape = agentcontract.TaskShapeImmediateReply
	turnRouter := turnRouterWith(languageModel, outcome)

	request := agentcontract.AgentRequest{
		Prompt:           "이거 뭐라고 적혀 있어?",
		ResponseLanguage: "ko",
		InputParts: []agentcontract.AgentPart{{
			Type:  agentcontract.AgentPartTypeImage,
			Image: &agentcontract.AgentImagePart{MimeType: "image/png", Filename: "board.png", DataBase64: "aGVsbG8="},
		}},
	}
	if _, errorValue := turnRouter.Plan(context.Background(), request); errorValue != nil {
		t.Fatalf("expected an answered turn: %v", errorValue)
	}

	userMessage := languageModel.requests[0].Messages[len(languageModel.requests[0].Messages)-1]
	hasImagePart := false
	for _, part := range userMessage.Parts {
		if part.Type == "image" && part.DataBase64 == "aGVsbG8=" {
			hasImagePart = true
		}
	}
	if !hasImagePart {
		t.Fatalf("expected the image itself in the words call, got %+v", userMessage.Parts)
	}
}

func TestTurnRouterUsesDecidedFieldsCarriedOnTheRequest(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{`{"expectedResults":[]}`}}
	decisionModel := intaketest.NewDecisionModel(startTaskOutcome())
	turnRouter := NewTurnRouter(languageModel, NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }), enabledIntakeOptions())
	decidedFields := startTaskOutcome().TurnDecision

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{
		Prompt:            "다음 주 발표자료 초안 만들어줘",
		ResponseLanguage:  "ko",
		DecidedTurnFields: &decidedFields,
	})
	if errorValue != nil {
		t.Fatalf("expected a routed turn: %v", errorValue)
	}

	if decision.Route != agentcontract.TurnRouteStartTask {
		t.Fatalf("expected the carried route, got %q", decision.Route)
	}
	if len(decisionModel.Requests()) != 0 {
		t.Fatalf("expected no second decision call when the burst already decided, got %d", len(decisionModel.Requests()))
	}
}

func systemMessagesAboutAFiring(messages []model.Message) []string {
	firingMessages := []string{}
	for _, message := range messages {
		if message.Role == "system" && strings.Contains(message.Content, agentcontract.ScheduledRunReading) {
			firingMessages = append(firingMessages, message.Content)
		}
	}
	return firingMessages
}

func TestTheWordsCallIsToldTheMessageIsAFiring(t *testing.T) {
	request := agentcontract.AgentRequest{
		Prompt:           "매일 이 시간에 주간 보고 알림을 보내줘.",
		ResponseLanguage: "ko",
		ScheduledRun: agentcontract.ScheduledRunContext{
			ScheduleID:   "schedule-1",
			Name:         "주간 보고 알림",
			Kind:         "cron",
			Cadence:      "매일 22:08",
			OccurrenceAt: "2026-09-18T22:08:00Z",
		},
	}

	firingMessages := systemMessagesAboutAFiring(TurnRouter{}.buildWordsMessages(request, startTaskOutcome().TurnDecision, "system prompt"))
	if len(firingMessages) != 1 {
		t.Fatalf("expected exactly one system message about the firing, got %d", len(firingMessages))
	}
	if !strings.Contains(firingMessages[0], "schedule-1") {
		t.Fatalf("expected the firing message to carry the schedule, got %q", firingMessages[0])
	}
}

func TestTheWordsCallSaysNothingAboutAFiringWithoutOne(t *testing.T) {
	request := agentcontract.AgentRequest{Prompt: "주간 보고 알림 보내줘", ResponseLanguage: "ko"}

	firingMessages := systemMessagesAboutAFiring(TurnRouter{}.buildWordsMessages(request, startTaskOutcome().TurnDecision, "system prompt"))
	if len(firingMessages) != 0 {
		t.Fatalf("expected no system message about a firing, got %d", len(firingMessages))
	}
}

func TestTurnRouterFailsWhenTheDecisionCallFails(t *testing.T) {
	turnRouter := NewTurnRouter(&sequenceLanguageModel{}, DecisionPlanner{}, enabledIntakeOptions())

	_, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "안녕"})
	if errorValue == nil {
		t.Fatal("expected the turn to fail when nothing can decide it")
	}
	if !strings.Contains(errorValue.Error(), ErrDecisionModelUnavailable.Error()) {
		t.Fatalf("expected the decision failure to survive, got %v", errorValue)
	}
}
