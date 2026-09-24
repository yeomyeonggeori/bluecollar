package intake

import (
	"errors"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const maximumClarificationOptionCount = 5

func normalizeTurnDecision(decision agentcontract.TurnDecision, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	decidedFields, errorValue := normalizeDecidedTurnFields(decision, request)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	return normalizeTurnWords(decidedFields), nil
}

func normalizeDecidedTurnFields(decision agentcontract.TurnDecision, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	decision, errorValue := normalizeDecidedRoute(decision, request)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	decision, errorValue = normalizeDecidedWork(decision)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	return normalizeDecidedExecution(decision, request)
}

func normalizeDecidedRoute(decision agentcontract.TurnDecision, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	decision.Route = normalizeTurnRoute(decision.Route)
	if decision.Route == "" {
		return agentcontract.TurnDecision{}, errors.New("turn router returned an invalid route")
	}
	decision = answerPendingInteraction(decision, request)
	decision.ReactionEmojiName = agentcontract.NormalizeReactionEmojiName(decision.ReactionEmojiName)
	return normalizeBusyRoute(decision, request.ActiveTask)
}

func answerPendingInteraction(decision agentcontract.TurnDecision, request agentcontract.AgentRequest) agentcontract.TurnDecision {
	hasPendingConfirmation := strings.TrimSpace(request.PendingConfirmation.TaskRunID) != ""
	decision.Approval = normalizeApprovalSignal(decision.Approval, hasPendingConfirmation)
	decision.Choices = normalizeChoiceSelections(decision.Choices, pendingChoiceContext(request))
	if !pendingInteractionIsAnswered(decision) {
		return decision
	}
	decision.Route = agentcontract.TurnRouteContinueTask
	decision.Classification = agentcontract.IntakeClassificationBoundedTask
	if decision.TaskShape == agentcontract.TaskShapeApprovalGatedTask {
		decision.TaskShape = agentcontract.TaskShapeMaintenanceTask
	}
	return decision
}

func pendingInteractionIsAnswered(decision agentcontract.TurnDecision) bool {
	if decision.Approval != nil && agentcontract.IsApprovingSignal(*decision.Approval) {
		return true
	}
	return len(decision.Choices) > 0
}

func normalizeBusyRoute(decision agentcontract.TurnDecision, activeTask agentcontract.ActiveTaskContext) (agentcontract.TurnDecision, error) {
	if strings.TrimSpace(activeTask.TaskRunID) == "" {
		decision.BusyRoute = ""
		return decision, nil
	}
	if !agentcontract.IsBusyRouteName(string(decision.BusyRoute)) {
		return agentcontract.TurnDecision{}, errors.New("turn router returned an invalid busy route")
	}
	return decision, nil
}

func normalizeDecidedWork(decision agentcontract.TurnDecision) (agentcontract.TurnDecision, error) {
	decision.Classification = agentcontract.NormalizeIntakeClassification(decision.Classification)
	if decision.Classification == "" {
		return agentcontract.TurnDecision{}, errors.New("turn router returned an invalid classification")
	}
	decision.TaskShape = normalizeTaskShape(decision.TaskShape)
	if decision.TaskShape == "" {
		return agentcontract.TurnDecision{}, errors.New("turn router returned an invalid task shape")
	}
	decision.RequestedOutputFormats = agentcontract.NormalizeRequestedOutputFormats(decision.RequestedOutputFormats)
	decision = normalizeDeliverableTools(decision)
	return liftBoundedImmediateReplyToMaintenance(decision), nil
}

func normalizeDeliverableTools(decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	decision = normalizeWebsiteDeliverableKind(decision)
	decision = normalizeSiteDeliverableFormats(decision)
	return removeFileDeliveryToolWithoutArtifactFormat(decision)
}

func liftBoundedImmediateReplyToMaintenance(decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	if decision.Classification != agentcontract.IntakeClassificationBoundedTask || decision.TaskShape != agentcontract.TaskShapeImmediateReply {
		return decision
	}
	decision.TaskShape = agentcontract.TaskShapeMaintenanceTask
	return decision
}

func normalizeDecidedExecution(decision agentcontract.TurnDecision, request agentcontract.AgentRequest) (agentcontract.TurnDecision, error) {
	decision = canonicalizeTurnDecision(decision)
	if decision.Route == agentcontract.TurnRouteConsume {
		decision.InitialToolNames = nil
	}
	decision.TaskLevel = agentcontract.NormalizeTaskLevel(string(decision.TaskLevel))
	if decision.TaskLevel == "" {
		return agentcontract.TurnDecision{}, errors.New("turn router returned an invalid task level")
	}
	decision.InitialToolNames = agentcontract.RegisteredToolNamesOnly(request.ToolSet, toolcontract.AppendUniqueStrings(decision.InitialToolNames))
	decision.ResponseLanguage = toolcontract.ResolveResponseLanguage(request.ResponseLanguage, decision.ResponseLanguage)
	decision.PriorTaskReference = agentcontract.NormalizePriorTaskReference(decision.PriorTaskReference)
	return decision, nil
}

func canonicalizeTurnDecision(decision agentcontract.TurnDecision) agentcontract.TurnDecision {
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

func classificationOf(route agentcontract.TurnRoute, needsTool bool) agentcontract.IntakeClassification {
	switch {
	case route == agentcontract.TurnRouteClarify:
		return agentcontract.IntakeClassificationNeedsConfirmation
	case route == agentcontract.TurnRouteGiveUp:
		return agentcontract.IntakeClassificationUnsupported
	case needsTool:
		return agentcontract.IntakeClassificationBoundedTask
	default:
		return agentcontract.IntakeClassificationQuickReply
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

func normalizeTurnWords(decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.BusyInstruction = strings.TrimSpace(decision.BusyInstruction)
	decision.ClarificationQuestion = strings.TrimSpace(decision.ClarificationQuestion)
	decision.ClarificationOptions = normalizeClarificationOptions(decision.ClarificationOptions)
	decision.ExpectedResults = agentcontract.NormalizeExpectedResults(decision.ExpectedResults)
	return removeFileExpectedResultsWithoutArtifactFormat(decision)
}

func normalizeWebsiteDeliverableKind(decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	if decision.DeliverableKind != agentcontract.DeliverableKindWebsite || decisionSuggestsSiteTool(decision) {
		return decision
	}
	decision.InitialToolNames = toolcontract.AppendUniqueStrings(decision.InitialToolNames, "site_serve")
	return decision
}

func normalizeSiteDeliverableFormats(decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	if !decisionSuggestsSiteTool(decision) {
		return decision
	}
	retainedFormats := []string{}
	for _, format := range decision.RequestedOutputFormats {
		if format == "html" {
			continue
		}
		retainedFormats = append(retainedFormats, format)
	}
	decision.RequestedOutputFormats = retainedFormats
	return decision
}

func decisionSuggestsSiteTool(decision agentcontract.TurnDecision) bool {
	for _, toolName := range decision.InitialToolNames {
		if strings.HasPrefix(strings.TrimSpace(toolName), "site_") {
			return true
		}
	}
	return false
}

func removeFileDeliveryToolWithoutArtifactFormat(decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	if hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		return decision
	}
	decision.InitialToolNames = removeToolName(decision.InitialToolNames, toolcontract.FileDeliverToolName)
	return decision
}

func removeFileExpectedResultsWithoutArtifactFormat(decision agentcontract.TurnDecision) agentcontract.TurnDecision {
	if hasArtifactOutputFormat(decision.RequestedOutputFormats) {
		return decision
	}
	decision.ExpectedResults = removeExpectedResultsByType(decision.ExpectedResults, agentcontract.ExpectedResultTypeFile)
	return decision
}

func hasArtifactOutputFormat(formats []string) bool {
	return len(agentcontract.NormalizeRequestedOutputFormats(formats)) > 0
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

func normalizeApprovalSignal(signal *agentcontract.ApprovalSignal, hasPendingConfirmation bool) *agentcontract.ApprovalSignal {
	if !hasPendingConfirmation {
		return nil
	}
	if signal == nil {
		unclear := agentcontract.ApprovalSignalUnclear
		return &unclear
	}
	normalizedSignal := agentcontract.ApprovalSignal(strings.TrimSpace(string(*signal)))
	if agentcontract.IsApprovalSignalName(string(normalizedSignal)) {
		return &normalizedSignal
	}
	unclear := agentcontract.ApprovalSignalUnclear
	return &unclear
}

func normalizeChoiceSelections(selections []string, pendingChoice agentcontract.PendingChoiceContext) []string {
	if strings.TrimSpace(pendingChoice.TaskRunID) == "" {
		return nil
	}
	selectedKeys := selectedChoiceKeys(selections, pendingChoice.Options)
	if strings.TrimSpace(pendingChoice.SelectionMode) != "multiple" && len(selectedKeys) > 1 {
		return nil
	}
	return selectedKeys
}

func selectedChoiceKeys(selections []string, options []agentcontract.ChoiceReplyOption) []string {
	offeredKeys, keyByPosition := offeredChoiceKeys(options)
	selectedKeys := []string{}
	seenKeys := map[string]bool{}
	for _, selection := range selections {
		key := strings.TrimSpace(selection)
		if keyAtPosition, isPosition := keyByPosition[key]; isPosition {
			key = keyAtPosition
		}
		if !offeredKeys[key] || seenKeys[key] {
			continue
		}
		seenKeys[key] = true
		selectedKeys = append(selectedKeys, key)
	}
	return selectedKeys
}

func offeredChoiceKeys(options []agentcontract.ChoiceReplyOption) (map[string]bool, map[string]string) {
	offeredKeys := map[string]bool{}
	keyByPosition := map[string]string{}
	for index, option := range options {
		key := strings.TrimSpace(option.Key)
		if key == "" {
			continue
		}
		offeredKeys[key] = true
		keyByPosition[strconv.Itoa(index+1)] = key
	}
	return offeredKeys, keyByPosition
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

func removeExpectedResultsByType(results []agentcontract.ExpectedResult, removedType string) []agentcontract.ExpectedResult {
	filteredResults := []agentcontract.ExpectedResult{}
	for _, result := range results {
		if result.Type != removedType {
			filteredResults = append(filteredResults, result)
		}
	}
	return filteredResults
}

func removeToolName(toolNames []string, removedToolName string) []string {
	values := []string{}
	for _, toolName := range toolNames {
		if !toolcontract.ToolNamesMatch(toolName, removedToolName) {
			values = toolcontract.AppendUniqueStrings(values, toolName)
		}
	}
	return values
}
