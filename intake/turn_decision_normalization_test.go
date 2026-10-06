package intake

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
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

func TestNormalizationPreservesAnExplicitClarificationDecision(t *testing.T) {
	decision := normalizedTurnDecision(t, decidedTurnFields(agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationNeedsConfirmation), agentcontract.AgentRequest{})

	if decision.Route != agentcontract.TurnRouteClarify {
		t.Fatalf("expected a model-selected clarification route to remain, got %q", decision.Route)
	}
	if decision.Classification != agentcontract.IntakeClassificationNeedsConfirmation {
		t.Fatalf("expected a model-selected clarification classification to remain, got %q", decision.Classification)
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

func TestOnlyRegisteredToolsSurviveNormalization(t *testing.T) {
	toolSet := newTestToolSet([]string{"task_add"})
	decidedFields := decidedTurnFields(agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationBoundedTask)
	decidedFields.InitialToolNames = []string{"task_add", "task_add", "invented_tool", "  "}

	decision := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})

	if len(decision.InitialToolNames) != 1 || decision.InitialToolNames[0] != "task_add" {
		t.Fatalf("expected only the registered tool once, got %v", decision.InitialToolNames)
	}
}

func TestRequiredAttachmentSurvivesWithoutAnArtifactFormat(t *testing.T) {
	toolSet := newTestToolSet([]string{toolcontract.FileDeliverToolName})
	decidedFields := decidedTurnFields(agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationBoundedTask)
	decidedFields.InitialToolNames = []string{toolcontract.FileDeliverToolName}
	decidedFields.ExpectedResults = []agentcontract.ExpectedResult{
		{ID: "screenshot", Type: agentcontract.ExpectedResultTypeFile, Description: "Attach a screenshot", Required: true},
		{ID: "reply", Type: agentcontract.ExpectedResultTypeMessage, Description: "답", Required: true},
	}

	withoutFormat := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})
	if len(withoutFormat.ExpectedResults) != 2 {
		t.Fatalf("expected the attachment requirement to survive without a format, got %+v", withoutFormat.ExpectedResults)
	}
	if len(withoutFormat.InitialToolNames) != 1 || withoutFormat.InitialToolNames[0] != toolcontract.FileDeliverToolName {
		t.Fatalf("expected the delivery tool for the attachment, got %v", withoutFormat.InitialToolNames)
	}

	decidedFields.RequestedOutputFormats = []string{"pptx"}
	withFormat := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{ToolSet: toolSet})
	if len(withFormat.ExpectedResults) != 2 {
		t.Fatalf("expected both results to survive an artifact format, got %+v", withFormat.ExpectedResults)
	}
}

func TestRequiredAttachmentCannotBecomeAnImmediateReply(t *testing.T) {
	decidedFields := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply)
	decidedFields.ExpectedResults = []agentcontract.ExpectedResult{
		{ID: "existing-file", Type: agentcontract.ExpectedResultTypeFile, Description: "Attach the existing file", Required: true},
	}
	decision := normalizedTurnDecision(t, decidedFields, agentcontract.AgentRequest{})
	if decision.Classification != agentcontract.IntakeClassificationBoundedTask || decision.TaskShape == agentcontract.TaskShapeImmediateReply {
		t.Fatalf("a required attachment became an immediate reply: %+v", decision)
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
	if validateClarificationQuestion(clarifyRoute, agentcontract.TurnWords{ClarificationDisposition: agentcontract.ClarificationDispositionAsk}) == nil {
		t.Fatal("expected a clarify route with no question to be refused")
	}
	needsConfirmation := decidedTurnFields(agentcontract.TurnRouteStartTask, agentcontract.IntakeClassificationNeedsConfirmation)
	if validateClarificationQuestion(needsConfirmation, agentcontract.TurnWords{ClarificationDisposition: agentcontract.ClarificationDispositionAsk}) == nil {
		t.Fatal("expected a needs_confirmation turn with no question to be refused")
	}
	answering := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply)
	if errorValue := validateClarificationQuestion(answering, agentcontract.TurnWords{}); errorValue != nil {
		t.Fatalf("expected an answering turn to need no clarification question: %v", errorValue)
	}
}

func TestClarificationDispositionRequiresConsistentQuestionAndOptions(t *testing.T) {
	clarify := decidedTurnFields(agentcontract.TurnRouteClarify, agentcontract.IntakeClassificationNeedsConfirmation)
	validAsk := agentcontract.TurnWords{
		ClarificationDisposition: agentcontract.ClarificationDispositionAsk,
		ClarificationQuestion:    "어느 기간으로 볼까요?",
	}
	if errorValue := validateClarificationQuestion(clarify, validAsk); errorValue != nil {
		t.Fatalf("expected a nonempty ask question to be valid: %v", errorValue)
	}
	if errorValue := validateClarificationQuestion(clarify, agentcontract.TurnWords{
		ClarificationDisposition: agentcontract.ClarificationDispositionStartWork,
	}); errorValue != nil {
		t.Fatalf("expected start_work with no question or options to be valid: %v", errorValue)
	}
	if validateClarificationQuestion(clarify, agentcontract.TurnWords{
		ClarificationDisposition: agentcontract.ClarificationDispositionStartWork,
		ClarificationOptions:     []agentcontract.ClarificationOption{{Label: "선택지"}},
	}) == nil {
		t.Fatal("expected start_work with clarification options to be refused")
	}
	if validateClarificationQuestion(clarify, agentcontract.TurnWords{
		ClarificationDisposition: agentcontract.ClarificationDispositionStartWork,
		ClarificationQuestion:    "null",
	}) == nil {
		t.Fatal("expected start_work with a nonempty question to be refused")
	}
	if validateClarificationQuestion(clarify, agentcontract.TurnWords{
		ClarificationDisposition: agentcontract.ClarificationDisposition("unknown"),
		ClarificationQuestion:    "어느 기간으로 볼까요?",
	}) == nil {
		t.Fatal("expected an unknown disposition to be refused")
	}
}

func TestAnExactPrecomputedDecisionIsUsedAsItStands(t *testing.T) {
	precomputedDecision := agentcontract.TurnDecision{Route: agentcontract.TurnRouteStartTask, Classification: "vibes", TaskShape: "blob"}
	turnRouter := NewTurnRouter(nil, DecisionPlanner{}, enabledIntakeOptions())

	exactDecision, errorValue := turnRouter.PlanObserved(context.Background(), agentcontract.AgentRequest{}, agentcontract.Routing{Decision: &precomputedDecision, IsExact: true}, nil)
	if errorValue != nil {
		t.Fatalf("expected an exact decision to be used as it stands: %v", errorValue)
	}
	if exactDecision.Classification != "vibes" {
		t.Fatalf("expected the exact decision untouched, got %+v", exactDecision)
	}

	if _, errorValue := turnRouter.PlanObserved(context.Background(), agentcontract.AgentRequest{}, agentcontract.Routing{Decision: &precomputedDecision}, nil); errorValue == nil {
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

func TestTheWordsCallIsCorrectedOnceAndThenHandsTheTurnOver(t *testing.T) {
	correctedModel := &sequenceLanguageModel{contents: []string{
		`{"reason":"","userFacingReply":"","clarificationQuestion":"","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
		`{"reason":"물어본다","userFacingReply":"","clarificationDisposition":"ask","clarificationQuestion":"어떤 형식으로 드릴까요?","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
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
	fallback, errorValue := turnRouterWith(unrepentantModel, clarifyOutcome()).Plan(context.Background(), agentcontract.AgentRequest{Prompt: "정리해줘"})
	if errorValue != nil || fallback.Route != agentcontract.TurnRouteStartTask || fallback.RoutingFallbackReason == "" {
		t.Fatalf("expected a second bad answer to hand the turn to the agent loop rather than loop or fail, got %+v, %v", fallback, errorValue)
	}
	if len(unrepentantModel.requests) != 2 {
		t.Fatalf("expected the correction loop to stop after one retry, got %d calls", len(unrepentantModel.requests))
	}
}

func TestClarificationReviewCarriesAStablePrefixEndingInTheClock(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"reason":"묻는다","userFacingReply":"","clarificationDisposition":"ask","clarificationQuestion":"어떤 형식으로?","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
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
	if messages[0].Content != clarificationWordsSystemPrompt {
		t.Fatalf("expected the clarification review prompt first, got %q", messages[0].Content)
	}
	if !strings.Contains(messages[2].Content, "Proposed decision") {
		t.Fatalf("expected the proposed routing fields third, got %q", messages[2].Content)
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
	descriptions := decisionToolDescriptions(toolSet, callableToolNames).tools
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

func TestAQuickReplyWhoseDeliverableIsAFileBecomesATask(t *testing.T) {
	for _, kind := range []agentcontract.DeliverableKind{agentcontract.DeliverableKindDocument, agentcontract.DeliverableKindPresentation} {
		answered := decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply)
		answered.DeliverableKind = kind

		decision := normalizedTurnDecision(t, answered, agentcontract.AgentRequest{})

		if decision.Classification != agentcontract.IntakeClassificationBoundedTask || decision.TaskShape != agentcontract.TaskShapeMaintenanceTask || decision.Route != agentcontract.TurnRouteStartTask {
			t.Fatalf("a quick reply whose deliverable is a %s was left as %q, %q, %q", kind, decision.Classification, decision.TaskShape, decision.Route)
		}
	}
}

func TestAQuickReplyWithNoDeliverableStaysAReply(t *testing.T) {
	decision := normalizedTurnDecision(t, decidedTurnFields(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply), agentcontract.AgentRequest{})

	if decision.Classification != agentcontract.IntakeClassificationQuickReply || decision.TaskShape != agentcontract.TaskShapeImmediateReply {
		t.Fatalf("a quick reply with no deliverable became %q, %q", decision.Classification, decision.TaskShape)
	}
}
