package intake

import (
	"context"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type toolSelectionPlan struct {
	requests                []model.DecisionRequest
	candidateToolNames      []string
	clippedDescriptionCount int
}

func (planner DecisionPlanner) withLikelyTools(ctx context.Context, request agentcontract.IntakeDecisionRequest, decisions agentcontract.IntakeDecisions, callLedger *agentcontract.IntakeCallLedger) agentcontract.IntakeDecisions {
	messageKeys := messageKeysThatStartWork(decisions)
	candidateToolNames := resolveCallableToolNames(request)
	if len(messageKeys) == 0 || len(candidateToolNames) == 0 {
		return decisions
	}
	singleToolKeys, severalToolKeys := messageKeysBySelectionShape(decisions, messageKeys)
	plan := planToolSelectionForShapes(request, singleToolKeys, severalToolKeys, candidateToolNames)
	calls := planner.decideEveryRequest(ctx, plan.requests)
	answers := mergedDecisionAnswers(calls)
	selectedToolNames, selectionError := likelyToolNamesByShape(singleToolKeys, severalToolKeys, candidateToolNames, answers, firstCallError(calls), likelyToolCountLimit)
	recordDecisionCalls(callLedger, calls, decisionCallContext{
		errorValue:    selectionError,
		toolSelection: toolSelectionRecord(messageKeys, plan, answers, selectionError, likelyToolCountLimit),
	})
	return withLikelyToolNames(decisions, selectedToolNames)
}

func messageKeysBySelectionShape(decisions agentcontract.IntakeDecisions, messageKeys []string) (singleToolKeys []string, severalToolKeys []string) {
	for _, messageKey := range messageKeys {
		if expectedToolCountOfMessage(decisions, messageKey) == agentcontract.ExpectedToolCountOne {
			singleToolKeys = append(singleToolKeys, messageKey)
			continue
		}
		severalToolKeys = append(severalToolKeys, messageKey)
	}
	return singleToolKeys, severalToolKeys
}

func expectedToolCountOfMessage(decisions agentcontract.IntakeDecisions, messageKey string) agentcontract.ExpectedToolCount {
	for index, decision := range decisions.Messages {
		if decisionMessageKey(index) == messageKey {
			return decision.TurnFields.ExpectedToolCount
		}
	}
	return ""
}

func planToolSelectionForShapes(request agentcontract.IntakeDecisionRequest, singleToolKeys []string, severalToolKeys []string, candidateToolNames []string) toolSelectionPlan {
	plan := toolSelectionPlan{candidateToolNames: candidateToolNames}
	if len(severalToolKeys) > 0 {
		plan = planToolSelection(request, severalToolKeys, candidateToolNames)
	}
	plan.requests = append(plan.requests, singleToolChoiceRequests(request, singleToolKeys, candidateToolNames)...)
	return plan
}

func singleToolChoiceRequests(request agentcontract.IntakeDecisionRequest, messageKeys []string, candidateToolNames []string) []model.DecisionRequest {
	if len(messageKeys) == 0 {
		return nil
	}
	described := decisionToolDescriptions(request.ToolSet, candidateToolNames)
	builder := newQuestionBuilder(request)
	questions := map[string]model.DecisionQuestion{}
	for _, messageKey := range messageKeys {
		questions[singleToolChoiceQuestionName(messageKey)] = builder.singleToolChoiceQuestion(messageKey, described.tools)
	}
	return []model.DecisionRequest{{State: buildDecisionState(request, described.tools), Questions: questions}}
}

func likelyToolNamesByShape(singleToolKeys []string, severalToolKeys []string, candidateToolNames []string, answers map[string]model.DecisionAnswer, callError error, countLimit int) (map[string][]string, error) {
	selectedToolNames, errorValue := likelyToolNamesByMessageKey(severalToolKeys, candidateToolNames, answers, callError, countLimit)
	if errorValue != nil {
		return nil, errorValue
	}
	for _, messageKey := range singleToolKeys {
		selectedToolNames[messageKey] = toolNamesCoveringBelief(answers[singleToolChoiceQuestionName(messageKey)].Probabilities, countLimit)
	}
	return selectedToolNames, nil
}

func toolNamesCoveringBelief(probabilityByToolName map[string]float64, countLimit int) []string {
	toolNames := make([]string, 0, len(probabilityByToolName))
	for toolName := range probabilityByToolName {
		if toolName != agentcontract.IntakeChoiceOptionNone {
			toolNames = append(toolNames, toolName)
		}
	}
	sort.Slice(toolNames, func(first, second int) bool {
		return probabilityByToolName[toolNames[first]] > probabilityByToolName[toolNames[second]]
	})
	covered, selected := 0.0, []string{}
	for _, toolName := range toolNames {
		if len(selected) >= countLimit || (covered >= singleToolBeliefMass && len(selected) > 0) {
			break
		}
		if probabilityByToolName[toolName] < recordedToolProbabilityFloor {
			break
		}
		selected = append(selected, toolName)
		covered += probabilityByToolName[toolName]
	}
	return selected
}

func messageKeysThatStartWork(decisions agentcontract.IntakeDecisions) []string {
	messageKeys := []string{}
	for index, decision := range decisions.Messages {
		if turnRouteStartsWork(canonicalizeTurnDecision(decision.TurnFields)) {
			messageKeys = append(messageKeys, decisionMessageKey(index))
		}
	}
	return messageKeys
}

func likelyToolNamesByMessageKey(messageKeys []string, candidateToolNames []string, answers map[string]model.DecisionAnswer, callError error, countLimit int) (map[string][]string, error) {
	if callError != nil {
		return nil, callError
	}
	selectedToolNames := map[string][]string{}
	for _, messageKey := range messageKeys {
		reader := answerReader{answers: answers, messageKey: messageKey}
		probabilityByToolName, errorValue := reader.toolProbabilities(candidateToolNames)
		if errorValue != nil {
			return nil, errorValue
		}
		selectedToolNames[messageKey] = selectLikelyToolNames(probabilityByToolName, candidateToolNames, countLimit)
	}
	return selectedToolNames, nil
}

func withLikelyToolNames(decisions agentcontract.IntakeDecisions, selectedToolNames map[string][]string) agentcontract.IntakeDecisions {
	for index := range decisions.Messages {
		decisions.Messages[index].TurnFields.InitialToolNames = selectedToolNames[decisionMessageKey(index)]
	}
	return decisions
}

func planToolSelection(request agentcontract.IntakeDecisionRequest, messageKeys []string, candidateToolNames []string) toolSelectionPlan {
	described := decisionToolDescriptions(request.ToolSet, candidateToolNames)
	plan := toolSelectionPlan{candidateToolNames: candidateToolNames, clippedDescriptionCount: described.clippedDescriptionCount}
	wholeRequest := toolSelectionRequestPart(request, messageKeys, described.tools)
	if decisionRequestByteCount(wholeRequest) <= decisionRequestByteBudget {
		plan.requests = []model.DecisionRequest{wholeRequest}
		return plan
	}
	byteCountByToolName := toolSelectionByteCounts(newQuestionBuilder(request), messageKeys, described.tools)
	fixedByteCount := decisionRequestByteCount(toolSelectionRequestPart(request, messageKeys, nil)) + len(toolLikelihoodGuidance)
	for batchCount := max(smallestBatchCountThatFits(fixedByteCount, byteCountByToolName), 2); batchCount <= len(described.tools); batchCount++ {
		requests := toolSelectionRequests(request, messageKeys, toolSelectionBatches(described.tools, byteCountByToolName, batchCount))
		if largestDecisionRequestByteCount(requests) <= decisionRequestByteBudget {
			plan.requests = requests
			return plan
		}
	}
	plan.requests = toolSelectionRequests(request, messageKeys, toolSelectionBatches(described.tools, byteCountByToolName, len(described.tools)))
	return plan
}

func toolSelectionRequests(request agentcontract.IntakeDecisionRequest, messageKeys []string, batches [][]decisionTool) []model.DecisionRequest {
	requests := make([]model.DecisionRequest, 0, len(batches))
	for _, batch := range batches {
		requests = append(requests, toolSelectionRequestPart(request, messageKeys, batch))
	}
	return requests
}

func toolSelectionRequestPart(request agentcontract.IntakeDecisionRequest, messageKeys []string, tools []decisionTool) model.DecisionRequest {
	return model.DecisionRequest{
		State:     buildDecisionState(request, tools),
		Questions: newQuestionBuilder(request).toolQuestions(messageKeys, toolNamesOf(tools)),
	}
}

func (planner DecisionPlanner) SelectToolNames(ctx context.Context, need agentcontract.ToolSelectionNeed) ([]agentcontract.SelectedTool, error) {
	if planner.decisionModel == nil {
		return nil, ErrDecisionModelUnavailable
	}
	request := toolSelectionNeedRequest(need)
	candidateToolNames := resolveCallableToolNames(request)
	if strings.TrimSpace(need.Need) == "" || len(candidateToolNames) == 0 {
		return nil, nil
	}
	messageKeys := []string{decisionMessageKey(0)}
	plan := planToolSelection(request, messageKeys, candidateToolNames)
	calls := planner.decideEveryRequest(ctx, plan.requests)
	answers := mergedDecisionAnswers(calls)
	countLimit := toolSelectionCountLimit(need)
	selectedToolNames, selectionError := likelyToolNamesByMessageKey(messageKeys, candidateToolNames, answers, firstCallError(calls), countLimit)
	recordDecisionCalls(need.CallLedger, calls, decisionCallContext{
		errorValue:    selectionError,
		toolSelection: toolSelectionRecord(messageKeys, plan, answers, selectionError, countLimit),
	})
	if selectionError != nil {
		return nil, selectionError
	}
	return describedSelectedTools(need.ToolSet, selectedToolNames[messageKeys[0]]), nil
}

func toolSelectionNeedRequest(need agentcontract.ToolSelectionNeed) agentcontract.IntakeDecisionRequest {
	return agentcontract.IntakeDecisionRequest{
		Messages:          []agentcontract.IntakeDecisionMessage{{MessageID: "tool-need", Prompt: strings.TrimSpace(need.Need)}},
		ToolSet:           need.ToolSet,
		CallableToolNames: need.CallableToolNames,
	}
}

func toolSelectionCountLimit(need agentcontract.ToolSelectionNeed) int {
	if need.CountLimit > 0 && need.CountLimit < likelyToolCountLimit {
		return need.CountLimit
	}
	return likelyToolCountLimit
}

func describedSelectedTools(toolSet *toolcontract.ToolSet, selectedToolNames []string) []agentcontract.SelectedTool {
	described := decisionToolDescriptions(toolSet, selectedToolNames)
	selectedTools := make([]agentcontract.SelectedTool, 0, len(described.tools))
	for _, tool := range described.tools {
		selectedTools = append(selectedTools, agentcontract.SelectedTool{Name: tool.Name, Description: firstSentenceOf(tool.Description)})
	}
	return selectedTools
}

func firstSentenceOf(description string) string {
	summary := strings.TrimSpace(description)
	if sentenceEnd := strings.IndexAny(summary, ".;\n"); sentenceEnd > 0 {
		summary = summary[:sentenceEnd]
	}
	return strings.TrimSpace(summary)
}
