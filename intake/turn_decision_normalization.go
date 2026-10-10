package intake

import (
	"errors"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const maximumClarificationOptionCount = 5

func normalizeTurnDecision(decision turnclassification.TurnDecision, request agentcontract.AgentRequest) (turnclassification.TurnDecision, error) {
	decidedFields, errorValue := normalizeDecidedTurnFields(decision, request)
	if errorValue != nil {
		return turnclassification.TurnDecision{}, errorValue
	}
	return normalizeTurnWords(decidedFields), nil
}

func normalizeDecidedTurnFields(decision turnclassification.TurnDecision, request agentcontract.AgentRequest) (turnclassification.TurnDecision, error) {
	decision, errorValue := normalizeDecidedRoute(decision)
	if errorValue != nil {
		return turnclassification.TurnDecision{}, errorValue
	}
	decision, errorValue = normalizeDecidedWork(decision)
	if errorValue != nil {
		return turnclassification.TurnDecision{}, errorValue
	}
	return normalizeDecidedExecution(decision, request)
}

func normalizeDecidedRoute(decision turnclassification.TurnDecision) (turnclassification.TurnDecision, error) {
	decision.Route = normalizeTurnRoute(decision.Route)
	if decision.Route == "" {
		return turnclassification.TurnDecision{}, errors.New("turn router returned an invalid route")
	}
	return decision, nil
}

func isBareContinuation(decision turnclassification.TurnDecision) bool {
	if decision.Route != agentcontract.TurnRouteContinueTask {
		return false
	}
	return decision.Classification == "" && decision.TaskShape == "" && decision.TaskLevel == ""
}

func normalizeDecidedWork(decision turnclassification.TurnDecision) (turnclassification.TurnDecision, error) {
	if isBareContinuation(decision) {
		return decision, nil
	}
	decision.Classification = agentcontract.NormalizeIntakeClassification(decision.Classification)
	if decision.Classification == "" {
		return turnclassification.TurnDecision{}, errors.New("turn router returned an invalid classification")
	}
	decision.TaskShape = normalizeTaskShape(decision.TaskShape)
	if decision.TaskShape == "" {
		return turnclassification.TurnDecision{}, errors.New("turn router returned an invalid task shape")
	}
	decision.RequestedOutputFormats = turnclassification.NormalizeRequestedOutputFormats(decision.RequestedOutputFormats)
	decision = normalizeDeliverableTools(decision)
	decision = liftFileDeliverableToBoundedTask(decision)
	return liftBoundedImmediateReplyToMaintenance(decision), nil
}

func liftFileDeliverableToBoundedTask(decision turnclassification.TurnDecision) turnclassification.TurnDecision {
	if decision.Classification != agentcontract.IntakeClassificationQuickReply {
		return decision
	}
	if !hasFileDeliverable(decision) && decision.DeliverableKind != agentcontract.DeliverableKindDocument && decision.DeliverableKind != agentcontract.DeliverableKindPresentation {
		return decision
	}
	decision.Classification = agentcontract.IntakeClassificationBoundedTask
	return decision
}

func normalizeDeliverableTools(decision turnclassification.TurnDecision) turnclassification.TurnDecision {
	return removeFileDeliveryToolWithoutFileDeliverable(decision)
}

func liftBoundedImmediateReplyToMaintenance(decision turnclassification.TurnDecision) turnclassification.TurnDecision {
	if decision.Classification != agentcontract.IntakeClassificationBoundedTask || decision.TaskShape != agentcontract.TaskShapeImmediateReply {
		return decision
	}
	decision.TaskShape = agentcontract.TaskShapeMaintenanceTask
	return decision
}

func normalizeDecidedExecution(decision turnclassification.TurnDecision, request agentcontract.AgentRequest) (turnclassification.TurnDecision, error) {
	decision = canonicalizeTurnDecision(decision)
	if isBareContinuation(decision) {
		return normalizeDecidedLanguage(decision, request), nil
	}
	decision.TaskLevel = agentcontract.NormalizeTaskLevel(string(decision.TaskLevel))
	if decision.TaskLevel == "" {
		return turnclassification.TurnDecision{}, errors.New("turn router returned an invalid task level")
	}
	decision.InitialToolNames = turnclassification.RegisteredToolNamesOnly(request.ToolSet, toolcontract.AppendUniqueStrings(decision.InitialToolNames))
	decision.PriorTaskReference = turnclassification.NormalizePriorTaskReference(decision.PriorTaskReference)
	return normalizeDecidedLanguage(decision, request), nil
}

func normalizeDecidedLanguage(decision turnclassification.TurnDecision, request agentcontract.AgentRequest) turnclassification.TurnDecision {
	decision.ResponseLanguage = toolcontract.ResolveResponseLanguage(request.ResponseLanguage, decision.ResponseLanguage)
	return decision
}

func canonicalizeTurnDecision(decision turnclassification.TurnDecision) turnclassification.TurnDecision {
	switch decision.Classification {
	case agentcontract.IntakeClassificationQuickReply:
		decision.Route = answerableTurnRoute(decision.Route)
		decision.TaskShape = agentcontract.TaskShapeImmediateReply
	case agentcontract.IntakeClassificationBoundedTask:
		decision.Route = executableTurnRoute(decision.Route)
	case agentcontract.IntakeClassificationNeedsConfirmation:
		decision.Route = agentcontract.TurnRouteClarify
		decision.TaskShape = agentcontract.TaskShapeApprovalGatedTask
	case agentcontract.IntakeClassificationUnsupported:
		decision.Route = agentcontract.TurnRouteGiveUp
		decision.TaskShape = agentcontract.TaskShapeImmediateReply
	}
	return decision
}

func executableTurnRoute(route agentcontract.TurnRoute) agentcontract.TurnRoute {
	switch route {
	case agentcontract.TurnRouteContinueTask, agentcontract.TurnRouteReviseTask:
		return route
	default:
		return agentcontract.TurnRouteStartTask
	}
}

func answerableTurnRoute(route agentcontract.TurnRoute) agentcontract.TurnRoute {
	switch route {
	case agentcontract.TurnRouteClarify, agentcontract.TurnRouteGiveUp:
		return agentcontract.TurnRouteAnswerQuestion
	default:
		return route
	}
}

func normalizeTurnWords(decision turnclassification.TurnDecision) turnclassification.TurnDecision {
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.ClarificationQuestion = strings.TrimSpace(decision.ClarificationQuestion)
	decision.ClarificationOptions = normalizeClarificationOptions(decision.ClarificationOptions)
	decision.ExpectedResults = turnclassification.NormalizeExpectedResults(decision.ExpectedResults)
	return decision
}

func startClarifiedWork(decision turnclassification.TurnDecision) turnclassification.TurnDecision {
	decision.Route = agentcontract.TurnRouteStartTask
	decision.Classification = agentcontract.IntakeClassificationBoundedTask
	decision.TaskShape = agentcontract.TaskShapeMaintenanceTask
	decision.ClarificationQuestion = ""
	decision.ClarificationOptions = nil
	return decision
}

func removeFileDeliveryToolWithoutFileDeliverable(decision turnclassification.TurnDecision) turnclassification.TurnDecision {
	if hasFileDeliverable(decision) {
		return decision
	}
	decision.InitialToolNames = removeToolName(decision.InitialToolNames, toolcontract.FileDeliverToolName)
	return decision
}

func hasFileDeliverable(decision turnclassification.TurnDecision) bool {
	if len(turnclassification.NormalizeRequestedOutputFormats(decision.RequestedOutputFormats)) > 0 {
		return true
	}
	for _, result := range turnclassification.NormalizeExpectedResults(decision.ExpectedResults) {
		if result.Type == agentcontract.ExpectedResultTypeFile && result.Required {
			return true
		}
	}
	return false
}

func normalizeTaskShape(taskShape agentcontract.TaskShape) agentcontract.TaskShape {
	if agentcontract.IsTaskShapeName(string(taskShape)) {
		return taskShape
	}
	return ""
}

func normalizeTurnRoute(route agentcontract.TurnRoute) agentcontract.TurnRoute {
	if agentcontract.IsTurnRouteName(string(route)) {
		return route
	}
	return ""
}

func normalizeClarificationOptions(options []agentcontract.ClarificationOption) []agentcontract.ClarificationOption {
	normalizedOptions := []agentcontract.ClarificationOption{}
	seenKeys := map[string]bool{}
	for index, option := range options {
		normalizedOption, isUsable := normalizeClarificationOption(option, index)
		if !isUsable || seenKeys[normalizedOption.Key] {
			continue
		}
		seenKeys[normalizedOption.Key] = true
		normalizedOptions = append(normalizedOptions, normalizedOption)
		if len(normalizedOptions) >= maximumClarificationOptionCount {
			break
		}
	}
	if len(normalizedOptions) < 2 {
		return nil
	}
	return normalizedOptions
}

func normalizeClarificationOption(option agentcontract.ClarificationOption, index int) (agentcontract.ClarificationOption, bool) {
	label := strings.TrimSpace(option.Label)
	if label == "" {
		return agentcontract.ClarificationOption{}, false
	}
	key := strings.TrimSpace(option.Key)
	if key == "" {
		key = clarificationOptionKey(index)
	}
	value := strings.TrimSpace(option.Value)
	if value == "" {
		value = label
	}
	return agentcontract.ClarificationOption{Key: key, Label: label, Value: value}, true
}

func clarificationOptionKey(index int) string {
	if index >= 0 && index < 26 {
		return string(rune('A' + index))
	}
	return "O"
}

func removeToolName(toolNames []string, removedToolName string) []string {
	values := []string{}
	for _, toolName := range toolNames {
		if !toolexposure.ToolNamesMatch(toolName, removedToolName) {
			values = toolcontract.AppendUniqueStrings(values, toolName)
		}
	}
	return values
}
