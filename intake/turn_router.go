package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type TurnRouter struct {
	languageModel   model.LanguageModelProvider
	decisionPlanner DecisionPlanner
	options         agentcontract.IntakeOptions
}

const turnWordsSystemPrompt = "You write the words for one turn of a workplace assistant. Every decision about this turn is already made and handed to you under \"Decided for this turn\"; do not re-decide it, do not argue with it, and do not mention it. Fill only the fields the schema asks for." +
	"\n\nclarify: ask in clarificationQuestion for exactly the one thing only the requester can supply, and offer 2-5 clarificationOptions when the request itself implies a finite choice. Do not invent options the message does not imply, and never ask for approval." +
	"\n\nanswer_question and answer_meta: write the answer itself in userFacingReply, like a concise coworker. Answer jokes and casual addressed remarks in kind rather than ignoring them." +
	"\n\ngive_up: say in userFacingReply that this cannot be done, and why, without blaming the requester." +
	"\n\nbusyRoute steer: write busyInstruction as the correction to hand the task already running, in the requester's own terms." +
	"\n\nreason is one short line for the log and is never shown to anybody. Leave every field a route does not need empty." +
	"\n\nWhat this agent said earlier is its own, not the requester's. A subject it named, a title it guessed at, or a thing it reported failing to find is never what the latest message is about unless the requester's own words say so."

const expectedResultsSystemPrompt = "You write the acceptance contract for one task a workplace assistant is about to start. Every decision about this turn is already made and handed to you under \"Decided for this turn\"; do not re-decide it, and write nothing for the requester here." +
	"\n\nList in expectedResults only what the request itself asks to exist when the work is done, each with the evidence that proves it: an id, a type, one sentence of description, whether it is required, and acceptanceHints naming the tool result, file, or link a reader would check." +
	"\n\nWhen the request asks to put a choice in front of the requester, the result is the choice itself and its acceptance hint is the tool that asks it. Answer with an empty list when the whole outcome is the final reply."

var ErrTurnRouterDisabled = errors.New("turn router disabled")
var ErrTurnRouterLanguageModelUnavailable = errors.New("turn router language model unavailable")

func NewTurnRouter(languageModel model.LanguageModelProvider, decisionPlanner DecisionPlanner, options agentcontract.IntakeOptions) TurnRouter {
	return TurnRouter{
		languageModel:   languageModel,
		decisionPlanner: decisionPlanner,
		options:         agentcontract.NormalizeIntakeOptions(options),
	}
}

func (turnRouter TurnRouter) Plan(ctx context.Context, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	return turnRouter.PlanObserved(ctx, request, nil)
}

func (turnRouter TurnRouter) PlanObserved(ctx context.Context, request agentcontract.AgentRequest, callLedger *agentcontract.IntakeCallLedger) (agentcontract.TurnDecision, error) {
	if request.PrecomputedTurnDecision != nil {
		if request.IsPrecomputedDecisionExact {
			return *request.PrecomputedTurnDecision, nil
		}
		return normalizeTurnDecision(*request.PrecomputedTurnDecision, request)
	}
	if !turnRouter.options.IsEnabled {
		return agentcontract.TurnDecision{}, ErrTurnRouterDisabled
	}
	decidedFields, errorValue := turnRouter.decideTurnFields(ctx, request, callLedger)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	decidedFields, errorValue = normalizeDecidedTurnFields(decidedFields, request)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	wordsShape, needsChatCall := turnWordsShapeFor(decidedFields)
	if !needsChatCall {
		return normalizeTurnWords(decidedFields), nil
	}
	observedRouter := turnRouter
	if callLedger != nil {
		observedRouter = TurnRouter{languageModel: callLedger.LanguageModel(turnRouter.languageModel), decisionPlanner: turnRouter.decisionPlanner, options: turnRouter.options}
	}
	turnWords, errorValue := observedRouter.writeTurnWords(ctx, request, decidedFields, wordsShape)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, fmt.Errorf("turn router words: %w", errorValue)
	}
	return normalizeTurnWords(decidedFields.WithTurnWords(turnWords)), nil
}

func (turnRouter TurnRouter) decideTurnFields(ctx context.Context, request agentcontract.AgentRequest, callLedger *agentcontract.IntakeCallLedger) (agentcontract.TurnDecision, error) {
	if request.DecidedTurnFields != nil {
		return *request.DecidedTurnFields, nil
	}
	decisions, errorValue := turnRouter.decisionPlanner.Decide(ctx, TurnRequestDecisionRequest(request), callLedger)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, fmt.Errorf("turn router: %w", errorValue)
	}
	if len(decisions.Messages) == 0 {
		return agentcontract.TurnDecision{}, errors.New("turn router: the decision model answered about no message")
	}
	return decisions.Messages[0].TurnFields, nil
}

func TurnRequestDecisionRequest(request agentcontract.AgentRequest) agentcontract.IntakeDecisionRequest {
	return agentcontract.IntakeDecisionRequest{
		Messages: []agentcontract.IntakeDecisionMessage{{
			Prompt:       request.Prompt,
			SenderName:   request.RequesterCallingName,
			SenderHandle: request.RequesterHandle,
			SentAt:       request.TurnStartedAt,
			InputParts:   request.InputParts,
			Attachments:  request.IntakeAttachmentFacts,

			IsAttachmentsOnly: strings.TrimSpace(request.Prompt) == "" && len(agentcontract.ImagePartsOf(request.InputParts)) > 0,
		}},
		ConversationType:    request.ConversationType,
		VisibleContext:      request.VisibleContext,
		Company:             request.Company,
		ActiveTask:          request.ActiveTask,
		PendingConfirmation: request.PendingConfirmation,
		PendingChoice:       pendingChoiceContext(request),
		PriorTask:           request.PriorTask,
		ScheduledRun:        request.ScheduledRun,
		ActiveGoal:          request.ActiveGoal,
		ToolSet:             request.ToolSet,
		ResponseLanguage:    request.ResponseLanguage,
		AllowGiveUp:         request.AllowGiveUp,
		AllowGiveUpReason:   request.AllowGiveUpReason,
		EnvironmentNow:      request.EnvironmentNow,
	}
}

type turnWordsShape struct {
	systemPrompt   string
	schemaDocument string
}

func turnWordsShapeFor(decision agentcontract.TurnDecision) (turnWordsShape, bool) {
	if turnRouteNeedsWords(decision) {
		return turnWordsShape{systemPrompt: turnWordsSystemPrompt, schemaDocument: turnWordsSchema()}, true
	}
	if turnRouteStartsWork(decision) {
		return turnWordsShape{systemPrompt: expectedResultsSystemPrompt, schemaDocument: expectedResultsOnlySchema()}, true
	}
	return turnWordsShape{}, false
}

func turnRouteNeedsWords(decision agentcontract.TurnDecision) bool {
	if decision.BusyRoute == agentcontract.BusyRouteSteer {
		return true
	}
	switch decision.Route {
	case agentcontract.TurnRouteClarify, agentcontract.TurnRouteAnswerQuestion, agentcontract.TurnRouteAnswerMeta, agentcontract.TurnRouteGiveUp:
		return true
	default:
		return false
	}
}

func turnRouteStartsWork(decision agentcontract.TurnDecision) bool {
	switch decision.Route {
	case agentcontract.TurnRouteStartTask, agentcontract.TurnRouteContinueTask, agentcontract.TurnRouteReviseTask:
		return true
	default:
		return false
	}
}

func (turnRouter TurnRouter) writeTurnWords(ctx context.Context, request agentcontract.AgentRequest, decidedFields agentcontract.TurnDecision, wordsShape turnWordsShape) (agentcontract.TurnWords, error) {
	if turnRouter.languageModel == nil {
		return agentcontract.TurnWords{}, ErrTurnRouterLanguageModelUnavailable
	}
	messages := turnRouter.buildWordsMessages(request, decidedFields, wordsShape.systemPrompt)
	turnWords, errorValue := turnRouter.generateTurnWords(ctx, turnWordsRequest(messages, wordsShape), decidedFields)
	if errorValue == nil {
		return turnWords, nil
	}
	if errors.Is(errorValue, context.Canceled) || errors.Is(errorValue, context.DeadlineExceeded) || ctx.Err() != nil {
		return agentcontract.TurnWords{}, errorValue
	}
	correctionInstruction, isCorrectable := turnRouterCorrectionInstructionForError(errorValue)
	if !isCorrectable {
		return agentcontract.TurnWords{}, errorValue
	}
	correctionMessages := append([]model.Message{}, messages...)
	if previousWords := previousTurnRouterDecision(errorValue); previousWords != "" {
		correctionMessages = append(correctionMessages, model.Message{Role: "assistant", Content: previousWords})
	}
	correctionMessages = append(correctionMessages, model.Message{Role: "system", Content: correctionInstruction})
	return turnRouter.generateTurnWords(ctx, turnWordsRequest(correctionMessages, wordsShape), decidedFields)
}

func (turnRouter TurnRouter) generateTurnWords(ctx context.Context, request model.StructuredResponseRequest, decidedFields agentcontract.TurnDecision) (agentcontract.TurnWords, error) {
	structuredResponse, errorValue := turnRouter.languageModel.GenerateStructuredResponse(ctx, request)
	if errorValue != nil {
		return agentcontract.TurnWords{}, errorValue
	}
	var turnWords agentcontract.TurnWords
	if parseError := json.Unmarshal([]byte(structuredResponse.Content), &turnWords); parseError != nil {
		return agentcontract.TurnWords{}, turnRouterDecisionError{cause: parseError, content: structuredResponse.Content}
	}
	if validationError := validateClarificationQuestion(decidedFields, turnWords); validationError != nil {
		return agentcontract.TurnWords{}, turnRouterDecisionError{cause: validationError, content: structuredResponse.Content}
	}
	return turnWords, nil
}

func turnWordsRequest(messages []model.Message, wordsShape turnWordsShape) model.StructuredResponseRequest {
	return model.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               agentcontract.TurnRouterSchemaName,
			Document:           wordsShape.schemaDocument,
			IsStrictlyEnforced: true,
		},
	}
}

func validateClarificationQuestion(decidedFields agentcontract.TurnDecision, turnWords agentcontract.TurnWords) error {
	if decidedFields.Route != agentcontract.TurnRouteClarify && agentcontract.NormalizeIntakeClassification(decidedFields.Classification) != agentcontract.IntakeClassificationNeedsConfirmation {
		return nil
	}
	if strings.TrimSpace(turnWords.ClarificationQuestion) != "" {
		return nil
	}
	return errors.New("a clarify turn requires a clarificationQuestion naming the one thing only the requester can supply")
}

type turnRouterDecisionError struct {
	cause   error
	content string
}

func (errorValue turnRouterDecisionError) Error() string {
	return errorValue.cause.Error()
}

func (errorValue turnRouterDecisionError) Unwrap() error {
	return errorValue.cause
}

func previousTurnRouterDecision(errorValue error) string {
	var decisionError turnRouterDecisionError
	if !errors.As(errorValue, &decisionError) {
		return ""
	}
	return strings.TrimSpace(decisionError.content)
}

func turnRouterCorrectionInstructionForError(errorValue error) (string, bool) {
	var decisionError turnRouterDecisionError
	if errors.As(errorValue, &decisionError) {
		return "The previous decision violates the turn contract: " + decisionError.cause.Error() + ". Reconsider that conflict and answer with exactly one complete JSON object for the schema.", true
	}
	correction, isCorrectable := model.StructuredOutputCorrectionFromError(errorValue)
	if !isCorrectable {
		return "", false
	}
	return turnRouterCorrectionInstruction(correction), true
}

func turnRouterCorrectionInstruction(correction model.StructuredOutputCorrection) string {
	messageParts := []string{
		"The previous response did not match the required structured output.",
		"Regenerate the complete response against the same schema.",
		"Correction code: " + correction.Code + ".",
		"Diagnostic category: " + string(correction.Diagnostic.Category) + ".",
	}
	if correction.Diagnostic.FinishReason != "" {
		messageParts = append(messageParts, "Finish reason: "+string(correction.Diagnostic.FinishReason)+".")
	}
	for _, issue := range correction.Diagnostic.ValidationIssues {
		messageParts = append(messageParts, "Validation issue: "+issue.FieldPath+" ("+string(issue.Code)+").")
	}
	return strings.Join(messageParts, " ")
}

func (turnRouter TurnRouter) buildWordsMessages(request agentcontract.AgentRequest, decidedFields agentcontract.TurnDecision, systemPrompt string) []model.Message {
	messages := []model.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "system", Content: agentcontract.ResponseLanguageInstruction(firstNonEmptyAddressingText(decidedFields.ResponseLanguage, request.ResponseLanguage))},
		{Role: "system", Content: decidedTurnFactsDescription(request, decidedFields)},
	}
	if contextDescription := agentcontract.BuildVisibleContextDescription(request.VisibleContext, request.Company.TimeZone); contextDescription != "" {
		messages = append(messages, model.Message{Role: "system", Content: contextDescription})
	}
	if goalDescription := agentcontract.ActiveGoalDescriptionForPrompt(request.ActiveGoal, request.Prompt); goalDescription != "" {
		messages = append(messages, model.Message{Role: "system", Content: goalDescription})
	}
	if priorTaskDescription := agentcontract.PriorTaskContextDescription(request.PriorTask); priorTaskDescription != "" {
		messages = append(messages, model.Message{Role: "system", Content: priorTaskDescription})
	}
	if pendingDescription := turnWordsPendingDescription(request); pendingDescription != "" {
		messages = append(messages, model.Message{Role: "system", Content: pendingDescription})
	}
	if temporalContext := agentcontract.BuildTemporalContextDescription(request.EnvironmentNow, request.Company.TimeZone); temporalContext != "" {
		messages = append(messages, model.Message{Role: "system", Content: temporalContext})
	}
	return append(messages, turnWordsUserMessage(request))
}

func turnWordsUserMessage(request agentcontract.AgentRequest) model.Message {
	message := model.Message{Role: "user", Content: request.Prompt}
	imageParts := agentcontract.ImageMessageParts(request.InputParts)
	if len(imageParts) == 0 {
		return message
	}
	message.Parts = append([]model.MessagePart{{Type: "text", Text: request.Prompt}}, imageParts...)
	return message
}

func decidedTurnFactsDescription(request agentcontract.AgentRequest, decidedFields agentcontract.TurnDecision) string {
	lines := []string{
		"Decided for this turn:",
		"- route: " + string(decidedFields.Route),
		"- classification: " + string(decidedFields.Classification),
		"- taskShape: " + string(decidedFields.TaskShape),
		"- level: " + string(decidedFields.TaskLevel),
		"- deliverableKind: " + string(decidedFields.DeliverableKind),
	}
	if decidedFields.BusyRoute != "" {
		lines = append(lines, "- busyRoute: "+string(decidedFields.BusyRoute))
	}
	if len(decidedFields.InitialToolNames) > 0 {
		lines = append(lines, "- likely tools: "+strings.Join(decidedFields.InitialToolNames, ", "))
	}
	for _, attachment := range request.IntakeAttachmentFacts {
		if description := strings.TrimSpace(attachment.Description); description != "" {
			lines = append(lines, "- attachment "+strings.TrimSpace(attachment.FileName)+": "+description)
		}
	}
	return strings.Join(lines, "\n")
}

func turnWordsPendingDescription(request agentcontract.AgentRequest) string {
	lines := []string{}
	if strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" {
		lines = append(lines, "Pending confirmation:", "- Task: "+strings.TrimSpace(request.PendingConfirmation.Prompt), "- Question: "+strings.TrimSpace(request.PendingConfirmation.Question))
	}
	if pendingChoice := pendingChoiceContext(request); strings.TrimSpace(pendingChoice.TaskRunID) != "" {
		optionLines := []string{}
		for index, option := range pendingChoice.Options {
			optionLines = append(optionLines, strconv.Itoa(index+1)+". "+strings.TrimSpace(option.Label))
		}
		lines = append(lines, "Pending question: "+strings.TrimSpace(pendingChoice.Question), "Options: "+strings.Join(optionLines, "; "))
	}
	if strings.TrimSpace(request.ActiveTask.TaskRunID) != "" {
		lines = append(lines, "Task already running:", "- Original instruction: "+strings.TrimSpace(request.ActiveTask.Prompt), "- Current progress: "+strings.TrimSpace(request.ActiveTask.Summary))
	}
	return strings.Join(lines, "\n")
}

func turnRouterCallableToolNames(request agentcontract.AgentRequest) []string {
	callableToolNames := []string{}
	if request.ToolSet != nil {
		for _, toolName := range request.ToolSet.ListToolNames() {
			if toolcontract.ToolIsModelCallable(toolName) {
				callableToolNames = append(callableToolNames, toolName)
			}
		}
		for _, toolDefinition := range request.ToolSet.ListRegisteredToolDefinitions() {
			toolName := strings.TrimSpace(toolDefinition.Name)
			if toolName != "" && agentcontract.RequiredEvidenceToolCanBeSatisfied(request.ToolSet, toolName) {
				callableToolNames = toolcontract.AppendUniqueStrings(callableToolNames, toolName)
			}
		}
	}
	return callableToolNames
}

func turnWordsSchema() string {
	document, errorValue := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason":          map[string]any{"type": "string", "maxLength": 512},
			"userFacingReply": map[string]any{"type": "string", "maxLength": 512},
			"clarificationQuestion": map[string]any{"anyOf": []any{
				map[string]any{"type": "string", "maxLength": 256},
				map[string]any{"type": "null"},
			}},
			"clarificationOptions": clarificationOptionsSchema(),
			"busyInstruction":      map[string]any{"type": "string", "maxLength": 512},
			"expectedResults":      expectedResultsSchema(),
		},
		"required":             []string{"reason", "userFacingReply", "clarificationQuestion", "clarificationOptions", "busyInstruction", "expectedResults"},
		"additionalProperties": false,
	})
	if errorValue != nil {
		return `{"type":"object","properties":{"reason":{"type":"string"},"userFacingReply":{"type":"string"}},"required":["reason","userFacingReply"],"additionalProperties":false}`
	}
	return string(document)
}

func expectedResultsOnlySchema() string {
	document, errorValue := json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"expectedResults": expectedResultsSchema()},
		"required":             []string{"expectedResults"},
		"additionalProperties": false,
	})
	if errorValue != nil {
		return `{"type":"object","properties":{"expectedResults":{"type":"array","items":{"type":"object"}}},"required":["expectedResults"],"additionalProperties":false}`
	}
	return string(document)
}

func expectedResultsSchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"maxItems": 8,
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "maxLength": 128},
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{agentcontract.ExpectedResultTypeMessage, agentcontract.ExpectedResultTypeFile, agentcontract.ExpectedResultTypeLink},
					"description": "message means an answer the final reply itself delivers — never add a second message result for the reply, or the agent sends the same answer twice. A separate message result is only for a message that must exist apart from the reply: a standalone channel post, or a direct message to somebody else.",
				},
				"description":     map[string]any{"type": "string", "maxLength": 256},
				"required":        map[string]any{"type": "boolean"},
				"acceptanceHints": map[string]any{"type": "array", "maxItems": 4, "items": map[string]any{"type": "string", "maxLength": 128}},
			},
			"required":             []string{"id", "type", "description", "required", "acceptanceHints"},
			"additionalProperties": false,
		},
	}
}

func pendingChoiceContext(request agentcontract.AgentRequest) agentcontract.PendingChoiceContext {
	if strings.TrimSpace(request.PendingChoice.TaskRunID) != "" {
		return request.PendingChoice
	}
	if strings.TrimSpace(request.PendingInput.TaskRunID) == "" || len(request.PendingInput.Options) == 0 {
		return agentcontract.PendingChoiceContext{}
	}
	return agentcontract.PendingChoiceContext{
		TaskRunID:     request.PendingInput.TaskRunID,
		Question:      request.PendingInput.Question,
		SelectionMode: request.PendingInput.SelectionMode,
		Options:       request.PendingInput.Options,
	}
}

func clarificationOptionsSchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"minItems": 0,
		"maxItems": maximumClarificationOptionCount,
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string", "maxLength": 64},
				"label": map[string]any{"type": "string", "maxLength": 128},
				"value": map[string]any{"type": "string", "maxLength": 256},
			},
			"required":             []string{"key", "label", "value"},
			"additionalProperties": false,
		},
	}
}
