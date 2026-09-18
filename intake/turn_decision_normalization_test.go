package intake

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func normalizedTurnDecision(t *testing.T, decision agentcontract.TurnDecision, request agentcontract.AgentRequest) agentcontract.TurnDecision {
	t.Helper()
	normalizedDecision, errorValue := normalizeTurnDecision(decision, request)
	if errorValue != nil {
		t.Fatalf("expected the decision to normalize: %v", errorValue)
	}
	return normalizedDecision
}

func decidedTurnFields(route agentcontract.TurnRoute, classification agentcontract.IntakeClassification) agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:              route,
		Classification:     classification,
		TaskShape:          agentcontract.TaskShapeImmediateReply,
		TaskLevel:          agentcontract.TaskLevelLow,
		DeliverableKind:    agentcontract.DeliverableKindNone,
		ResponseLanguage:   "ko",
		PriorTaskReference: agentcontract.PriorTaskReferenceNone,
	}
}

func TestClassificationRepairsTheRouteItContradicts(t *testing.T) {
	repairs := []struct {
		name           string
		route          agentcontract.TurnRoute
		classification agentcontract.IntakeClassification
		expectedRoute  agentcontract.TurnRoute
		expectedShape  agentcontract.TaskShape
	}{
		{"a bounded task cannot consume", agentcontract.TurnRouteConsume, agentcontract.IntakeClassificationBoundedTask, agentcontract.TurnRouteStartTask, agentcontract.TaskShapeMaintenanceTask},
		{"a bounded task cannot clarify", agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationBoundedTask, agentcontract.TurnRouteStartTask, agentcontract.TaskShapeMaintenanceTask},
		{"a bounded task cannot give up", agentcontract.TurnRouteGiveUp, agentcontract.IntakeClassificationBoundedTask, agentcontract.TurnRouteStartTask, agentcontract.TaskShapeMaintenanceTask},
		{"a quick reply cannot clarify", agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationQuickReply, agentcontract.TurnRouteAnswerQuestion, agentcontract.TaskShapeImmediateReply},
		{"a quick reply cannot give up", agentcontract.TurnRouteGiveUp, agentcontract.IntakeClassificationQuickReply, agentcontract.TurnRouteAnswerQuestion, agentcontract.TaskShapeImmediateReply},
		{"needs_confirmation always clarifies", agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationNeedsConfirmation, agentcontract.TurnRouteClarify, agentcontract.TaskShapeApprovalGatedTask},
		{"unsupported always gives up", agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationUnsupported, agentcontract.TurnRouteGiveUp, agentcontract.TaskShapeImmediateReply},
	}

	for _, repair := range repairs {
		decision := normalizedTurnDecision(t, decidedTurnFields(repair.route, repair.classification), agentcontract.AgentRequest{})
		if decision.Route != repair.expectedRoute {
			t.Fatalf("%s: expected %q, got %q", repair.name, repair.expectedRoute, decision.Route)
		}
		if decision.TaskShape != repair.expectedShape {
			t.Fatalf("%s: expected the %q shape, got %q", repair.name, repair.expectedShape, decision.TaskShape)
		}
	}
}

func TestAnUnsupportedTurnIsGivenUpOnStructurally(t *testing.T) {
	decision := normalizedTurnDecision(t, decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationUnsupported), agentcontract.AgentRequest{})

	if decision.Route != agentcontract.TurnRouteGiveUp {
		t.Fatalf("expected the give_up route, got %q", decision.Route)
	}
	if decision.Classification != agentcontract.IntakeClassificationUnsupported {
		t.Fatalf("expected the unsupported classification to survive, got %q", decision.Classification)
	}
	if len(decision.InitialToolNames) != 0 {
		t.Fatalf("expected no tools on a turn nothing will run, got %v", decision.InitialToolNames)
	}
}

func TestAnInvalidClosedFieldIsAnError(t *testing.T) {
	invalidDecisions := map[string]agentcontract.TurnDecision{
		"route":          {Route: "sideways", Classification: agentcontract.IntakeClassificationQuickReply, TaskShape: agentcontract.TaskShapeImmediateReply, TaskLevel: agentcontract.TaskLevelLow},
		"classification": {Route: agentcontract.TurnRouteAnswerQuestion, Classification: "vibes", TaskShape: agentcontract.TaskShapeImmediateReply, TaskLevel: agentcontract.TaskLevelLow},
		"taskShape":      {Route: agentcontract.TurnRouteAnswerQuestion, Classification: agentcontract.IntakeClassificationQuickReply, TaskShape: "blob", TaskLevel: agentcontract.TaskLevelLow},
		"level":          {Route: agentcontract.TurnRouteAnswerQuestion, Classification: agentcontract.IntakeClassificationQuickReply, TaskShape: agentcontract.TaskShapeImmediateReply, TaskLevel: "enormous"},
	}

	for fieldName, decision := range invalidDecisions {
		if _, errorValue := normalizeTurnDecision(decision, agentcontract.AgentRequest{}); errorValue == nil {
			t.Fatalf("expected an invalid %s to be refused", fieldName)
		}
	}
}

func TestASideEffectToolTurnsAQuickReplyIntoWork(t *testing.T) {
	toolSet := newTestToolSet([]string{"task_add"})
	decidedFields := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply)
	decidedFields.InitialToolNames = []string{"task_add"}

	decision := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})

	if decision.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("expected a side-effect tool to make the turn work, got %q", decision.Classification)
	}
	if decision.Route != agentcontract.TurnRouteStartTask {
		t.Fatalf("expected the start_task route, got %q", decision.Route)
	}
	if decision.TaskShape != agentcontract.TaskShapeMaintenanceTask {
		t.Fatalf("expected a maintenance task, got %q", decision.TaskShape)
	}
}

func TestAConsumedTurnCarriesNoTools(t *testing.T) {
	toolSet := newTestToolSet([]string{"task_list"})
	decidedFields := decidedTurnFields(agentcontract.TurnRouteConsume, agentcontract.IntakeClassificationQuickReply)
	decidedFields.InitialToolNames = []string{"task_list"}

	decision := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})

	if decision.Route != agentcontract.TurnRouteConsume {
		t.Fatalf("expected the consume route to survive, got %q", decision.Route)
	}
	if len(decision.InitialToolNames) != 0 {
		t.Fatalf("expected a consumed turn to run nothing, got %v", decision.InitialToolNames)
	}
}

func TestAWebsiteDeliverableCarriesTheToolThatServesIt(t *testing.T) {
	toolSet := newTestToolSet([]string{"site_serve"})
	decidedFields := decidedTurnFields(agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationBoundedTask)
	decidedFields.DeliverableKind = agentcontract.DeliverableKindWebsite
	decidedFields.RequestedOutputFormats = []string{"html", "pdf"}

	decision := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})

	if len(decision.InitialToolNames) != 1 || decision.InitialToolNames[0] != "site_serve" {
		t.Fatalf("expected site_serve to be added, got %v", decision.InitialToolNames)
	}
	for _, format := range decision.RequestedOutputFormats {
		if format == "html" {
			t.Fatalf("expected a served site to drop the html file format, got %v", decision.RequestedOutputFormats)
		}
	}
}

func TestOnlyRegisteredToolsSurviveNormalization(t *testing.T) {
	toolSet := newTestToolSet([]string{"task_add"})
	decidedFields := decidedTurnFields(agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationBoundedTask)
	decidedFields.InitialToolNames = []string{"task_add", "task_add", "invented_tool", "  "}

	decision := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})

	if len(decision.InitialToolNames) != 1 || decision.InitialToolNames[0] != "task_add" {
		t.Fatalf("expected only the registered tool once, got %v", decision.InitialToolNames)
	}
}

func TestAFileResultNeedsAnArtifactFormat(t *testing.T) {
	toolSet := newTestToolSet([]string{toolcontract.FileDeliverToolName})
	decidedFields := decidedTurnFields(agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationBoundedTask)
	decidedFields.InitialToolNames = []string{toolcontract.FileDeliverToolName}
	decidedFields.ExpectedResults = []agentcontract.ExpectedResult{
		{ID: "deck", Type: agentcontract.ExpectedResultTypeFile, Description: "덱", Required: true},
		{ID: "reply", Type: agentcontract.ExpectedResultTypeMessage, Description: "답", Required: true},
	}

	withoutFormat := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})
	for _, expectedResult := range withoutFormat.ExpectedResults {
		if expectedResult.Type == agentcontract.ExpectedResultTypeFile {
			t.Fatalf("expected the file result to go with no artifact format, got %+v", withoutFormat.ExpectedResults)
		}
	}
	for _, toolName := range withoutFormat.InitialToolNames {
		if toolName == toolcontract.FileDeliverToolName {
			t.Fatalf("expected the delivery tool to go with no artifact format, got %v", withoutFormat.InitialToolNames)
		}
	}

	decidedFields.RequestedOutputFormats = []string{"pptx"}
	withFormat := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})
	if len(withFormat.ExpectedResults) != 2 {
		t.Fatalf("expected both results to survive an artifact format, got %+v", withFormat.ExpectedResults)
	}
}

func TestAnApprovalOnAPendingConfirmationContinuesTheTask(t *testing.T) {
	request := agentcontract.AgentRequest{PendingConfirmation: agentcontract.PendingConfirmationContext{TaskRunID: "task-run-1", Question: "삭제할까요?"}}
	approve := agentcontract.ApprovalSignalApprove
	decidedFields := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationBoundedTask)
	decidedFields.Approval = &approve

	decision := normalizedTurnDecision(t, decidedFields, request)

	if decision.Route != agentcontract.TurnRouteContinueTask {
		t.Fatalf("expected an approval to continue the task, got %q", decision.Route)
	}
}

func TestAnApprovalSignalWithoutAPendingConfirmationIsDropped(t *testing.T) {
	approve := agentcontract.ApprovalSignalApprove
	decidedFields := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply)
	decidedFields.Approval = &approve

	decision := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{})

	if decision.Approval != nil {
		t.Fatalf("expected no approval signal without a pending confirmation, got %q", *decision.Approval)
	}
}

func TestAChoiceSelectedByItsNumberBecomesItsKey(t *testing.T) {
	request := agentcontract.AgentRequest{PendingChoice: agentcontract.PendingChoiceContext{
		TaskRunID: "task-run-1",
		Options:   []agentcontract.ChoiceReplyOption{{Key: "table", Label: "표"}, {Key: "graph", Label: "그래프"}},
	}}
	decidedFields := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply)
	decidedFields.Choices = []string{"2"}

	decision := normalizedTurnDecision(t, decidedFields, request)

	if len(decision.Choices) != 1 || decision.Choices[0] != "graph" {
		t.Fatalf("expected the option number to become its key, got %v", decision.Choices)
	}
}

func TestASingleSelectRefusesTwoSelections(t *testing.T) {
	request := agentcontract.AgentRequest{PendingChoice: agentcontract.PendingChoiceContext{
		TaskRunID: "task-run-1",
		Options:   []agentcontract.ChoiceReplyOption{{Key: "table"}, {Key: "graph"}},
	}}
	decidedFields := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply)
	decidedFields.Choices = []string{"table", "graph"}

	decision := normalizedTurnDecision(t, decidedFields, request)

	if len(decision.Choices) != 0 {
		t.Fatalf("expected a single-select to refuse two selections, got %v", decision.Choices)
	}
}

func TestClarificationOptionsAreKeyedAndKeptAboveOne(t *testing.T) {
	decidedFields := decidedTurnFields(agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationNeedsConfirmation)
	decidedFields.ClarificationQuestion = "  어떤 형식으로 드릴까요?  "
	decidedFields.ClarificationOptions = []agentcontract.ClarificationOption{
		{Label: "표"},
		{Label: "  "},
		{Label: "그래프", Value: "graph"},
	}

	decision := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{})

	if decision.ClarificationQuestion != "어떤 형식으로 드릴까요?" {
		t.Fatalf("expected the question to be trimmed, got %q", decision.ClarificationQuestion)
	}
	if len(decision.ClarificationOptions) != 2 {
		t.Fatalf("expected the blank option to be dropped, got %+v", decision.ClarificationOptions)
	}
	if decision.ClarificationOptions[0].Key != "A" || decision.ClarificationOptions[0].Value != "표" {
		t.Fatalf("expected an unkeyed option to be keyed and to answer with its label, got %+v", decision.ClarificationOptions[0])
	}

	decidedFields.ClarificationOptions = []agentcontract.ClarificationOption{{Label: "표"}}
	lonelyOption := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{})
	if len(lonelyOption.ClarificationOptions) != 0 {
		t.Fatalf("expected a single option to be no choice at all, got %+v", lonelyOption.ClarificationOptions)
	}
}

func TestAClarifyTurnRequiresAQuestion(t *testing.T) {
	clarifyRoute := decidedTurnFields(agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationBoundedTask)
	if validateClarificationQuestion(clarifyRoute, agentcontract.TurnWords{}) == nil {
		t.Fatal("expected a clarify route with no question to be refused")
	}
	needsConfirmation := decidedTurnFields(agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationNeedsConfirmation)
	if validateClarificationQuestion(needsConfirmation, agentcontract.TurnWords{}) == nil {
		t.Fatal("expected a needs_confirmation turn with no question to be refused")
	}
	answering := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply)
	if errorValue := validateClarificationQuestion(answering, agentcontract.TurnWords{}); errorValue != nil {
		t.Fatalf("expected an answering turn to need no clarification question: %v", errorValue)
	}
}

func TestAnExactPrecomputedDecisionIsUsedAsItStands(t *testing.T) {
	precomputedDecision := agentcontract.TurnDecision{Route: agentcontract.TurnRouteStartTask, Classification: "vibes", TaskShape: "blob"}
	turnRouter := NewTurnRouter(nil, DecisionPlanner{}, enabledIntakeOptions())

	exactDecision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{
		PrecomputedTurnDecision:    &precomputedDecision,
		IsPrecomputedDecisionExact: true,
	})
	if errorValue != nil {
		t.Fatalf("expected an exact decision to be used as it stands: %v", errorValue)
	}
	if exactDecision.Classification != "vibes" {
		t.Fatalf("expected the exact decision untouched, got %+v", exactDecision)
	}

	if _, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{PrecomputedTurnDecision: &precomputedDecision}); errorValue == nil {
		t.Fatal("expected an inexact precomputed decision to be normalized and refused")
	}
}

func TestTheWordsCallForWorkAsksOnlyForItsAcceptance(t *testing.T) {
	toolSet := newTestToolSet([]string{"task_add"})
	outcome := startTaskOutcome()
	outcome.TurnDecision.Route = agentcontract.TurnRouteStartTask
	outcome.TurnDecision.Classification = agentcontract.IntakeClassificationBoundedTask
	outcome.TurnDecision.TaskShape = agentcontract.TaskShapeMaintenanceTask
	outcome.TurnDecision.InitialToolNames = []string{"task_add"}
	outcome.TurnDecision.RequestedOutputFormats = nil
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"expectedResults":[{"id":"task","type":"message","description":"등록된 업무","required":true,"acceptanceHints":["task_add"]}]}`,
	}}
	turnRouter := turnRouterWith(languageModel, outcome)

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "업무 등록해줘", ResponseLanguage: "ko", ToolSet: toolSet})
	if errorValue != nil {
		t.Fatalf("expected a routed turn: %v", errorValue)
	}

	if decision.Route != agentcontract.TurnRouteStartTask {
		t.Fatalf("expected the work route to survive, got %q", decision.Route)
	}
	if !containsString(decision.InitialToolNames, "task_add") {
		t.Fatalf("expected the likely tool to reach the turn, got %v", decision.InitialToolNames)
	}
	if len(languageModel.requests) != 1 {
		t.Fatalf("expected exactly one chat call, got %d", len(languageModel.requests))
	}
	schemaDocument := languageModel.requests[0].StructuredOutputSchema.Document
	if strings.Contains(schemaDocument, "userFacingReply") {
		t.Fatalf("expected the acceptance-only schema for work, got %s", schemaDocument)
	}
	if len(decision.ExpectedResults) != 1 || decision.ExpectedResults[0].ID != "task" {
		t.Fatalf("expected the completion gate to be given something to grade, got %+v", decision.ExpectedResults)
	}
}

func TestTheWordsCallIsCorrectedOnceAndThenGivesUp(t *testing.T) {
	correctedModel := &sequenceLanguageModel{contents: []string{
		`{"reason":"","userFacingReply":"","clarificationQuestion":"","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
		`{"reason":"물어본다","userFacingReply":"","clarificationQuestion":"어떤 형식으로 드릴까요?","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
	}}
	turnRouter := turnRouterWith(correctedModel, clarifyOutcome())

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "정리해줘", ResponseLanguage: "ko"})
	if errorValue != nil {
		t.Fatalf("expected the correction to be accepted: %v", errorValue)
	}
	if decision.ClarificationQuestion != "어떤 형식으로 드릴까요?" {
		t.Fatalf("expected the corrected question, got %q", decision.ClarificationQuestion)
	}
	if len(correctedModel.requests) != 2 {
		t.Fatalf("expected one correction, got %d calls", len(correctedModel.requests))
	}
	correctionMessages := correctedModel.requests[1].Messages
	if !strings.Contains(correctionMessages[len(correctionMessages)-1].Content, "violates the turn contract") {
		t.Fatalf("expected the correction to name the conflict, got %+v", correctionMessages[len(correctionMessages)-1])
	}

	unrepentantModel := &sequenceLanguageModel{contents: []string{
		`{"reason":"","userFacingReply":"","clarificationQuestion":"","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
	}}
	if _, errorValue := turnRouterWith(unrepentantModel, clarifyOutcome()).Plan(context.Background(), agentcontract.AgentRequest{Prompt: "정리해줘"}); errorValue == nil {
		t.Fatal("expected a second bad answer to fail rather than loop")
	}
	if len(unrepentantModel.requests) != 2 {
		t.Fatalf("expected the correction loop to stop after one retry, got %d calls", len(unrepentantModel.requests))
	}
}

func TestTheWordsCallCarriesAStablePrefixEndingInTheClock(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"reason":"묻는다","userFacingReply":"","clarificationQuestion":"어떤 형식으로?","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
	}}
	turnRouter := turnRouterWith(languageModel, clarifyOutcome())

	request := agentcontract.AgentRequest{
		Prompt:           "정리해줘",
		ResponseLanguage: "ko",
		Company:          agentcontract.CompanyContext{Name: "여명거리", TimeZone: "Asia/Seoul"},
		EnvironmentNow:   agentcontract.AgentRequest{}.TurnStartedAt,
		VisibleContext:   agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{{Speaker: "이샘플", Text: "어제 자료 봤어?"}}},
	}
	if _, errorValue := turnRouter.Plan(context.Background(), request); errorValue != nil {
		t.Fatalf("expected a routed turn: %v", errorValue)
	}

	messages := languageModel.requests[0].Messages
	if messages[0].Content != turnWordsSystemPrompt {
		t.Fatalf("expected the words prompt first, got %q", messages[0].Content)
	}
	if !strings.Contains(messages[2].Content, "Decided for this turn:") {
		t.Fatalf("expected the decided facts third, got %q", messages[2].Content)
	}
	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != "user" || lastMessage.Content != request.Prompt {
		t.Fatalf("expected the message itself last, got %+v", lastMessage)
	}
	for _, message := range messages[:len(messages)-1] {
		if message.Role != "system" {
			t.Fatalf("expected every message before the user message to be a system message, got %+v", message)
		}
	}
}

func TestEveryReachableToolIsOfferedToTheDecision(t *testing.T) {
	toolSet := newTestToolSet([]string{"task_add", "task_list"})
	request := agentcontract.IntakeDecisionRequest{ToolSet: toolSet}

	callableToolNames := resolveCallableToolNames(request)
	for _, toolName := range toolSet.ListToolNames() {
		if !containsToolName(callableToolNames, toolName) {
			t.Fatalf("expected %s to be callable, got %v", toolName, callableToolNames)
		}
	}
	descriptions := decisionToolDescriptions(toolSet, callableToolNames)
	if len(descriptions) != len(callableToolNames) {
		t.Fatalf("expected one description per callable tool, got %+v", descriptions)
	}
	for _, description := range descriptions {
		if strings.TrimSpace(description.Name) == "" {
			t.Fatalf("expected every offered tool to be named, got %+v", descriptions)
		}
	}
}

func containsToolName(toolNames []string, toolName string) bool {
	for _, candidate := range toolNames {
		if candidate == toolName {
			return true
		}
	}
	return false
}

func TestAMessageAimedAtAPersonIsNeverAnsweredInWords(t *testing.T) {
	planner := NewDecisionPlanner(intaketest.NewDecisionModel(intaketest.Outcome{
		Addressing: agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetHuman, ShouldRespond: true},
	}), nil, func() float64 { return 1 })

	decision := decideOnce(t, planner, addressedDecisionRequest("샘플님 이거 확인 부탁해요"))

	if decision.Addressing.ShouldRespond {
		t.Fatal("expected a message aimed at a person to go unanswered")
	}
}

func TestAnEmojiOutsideTheAcceptedSetIsNoReaction(t *testing.T) {
	if normalizeAddressingReactionEmoji("shrug") != "" {
		t.Fatal("expected an emoji the runtime does not accept to be dropped")
	}
	if normalizeAddressingReactionEmoji("  EYES  ") != "eyes" {
		t.Fatalf("expected an accepted emoji to be normalized, got %q", normalizeAddressingReactionEmoji("  EYES  "))
	}
}
