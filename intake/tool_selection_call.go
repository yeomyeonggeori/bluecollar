package intake

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func (planner DecisionPlanner) withLikelyTools(ctx context.Context, request agentcontract.IntakeDecisionRequest, decisions agentcontract.IntakeDecisions, callLedger *agentcontract.IntakeCallLedger) agentcontract.IntakeDecisions {
	messageKeys := messageKeysThatStartWork(decisions)
	candidateToolNames := resolveCallableToolNames(request)
	if len(messageKeys) == 0 || len(candidateToolNames) == 0 {
		return decisions
	}
	calls := planner.decideEveryRequest(ctx, planToolSelectionRequests(request, messageKeys, candidateToolNames))
	answers := mergedDecisionAnswers(calls)
	selectedToolNames, selectionError := likelyToolNamesByMessageKey(messageKeys, candidateToolNames, answers, firstCallError(calls))
	recordDecisionCalls(callLedger, calls, decisionCallContext{
		errorValue:    selectionError,
		toolSelection: toolSelectionRecord(messageKeys, candidateToolNames, answers, selectionError),
	})
	return withLikelyToolNames(decisions, selectedToolNames)
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

func likelyToolNamesByMessageKey(messageKeys []string, candidateToolNames []string, answers map[string]model.DecisionAnswer, callError error) (map[string][]string, error) {
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
		selectedToolNames[messageKey] = selectLikelyToolNames(probabilityByToolName, candidateToolNames)
	}
	return selectedToolNames, nil
}

func withLikelyToolNames(decisions agentcontract.IntakeDecisions, selectedToolNames map[string][]string) agentcontract.IntakeDecisions {
	for index := range decisions.Messages {
		decisions.Messages[index].TurnFields.InitialToolNames = selectedToolNames[decisionMessageKey(index)]
	}
	return decisions
}

func planToolSelectionRequests(request agentcontract.IntakeDecisionRequest, messageKeys []string, toolNames []string) []model.DecisionRequest {
	wholeRequest := toolSelectionRequestPart(request, messageKeys, toolNames)
	if decisionRequestByteCount(wholeRequest) <= decisionRequestByteBudget {
		return []model.DecisionRequest{wholeRequest}
	}
	for partCount := 2; partCount < len(toolNames); partCount++ {
		requests := balancedToolSelectionRequests(request, messageKeys, toolNames, partCount)
		if largestDecisionRequestByteCount(requests) <= decisionRequestByteBudget {
			return requests
		}
	}
	return balancedToolSelectionRequests(request, messageKeys, toolNames, len(toolNames))
}

func balancedToolSelectionRequests(request agentcontract.IntakeDecisionRequest, messageKeys []string, toolNames []string, partCount int) []model.DecisionRequest {
	requests := []model.DecisionRequest{}
	for _, partToolNames := range balancedToolNameParts(toolNames, partCount) {
		requests = append(requests, toolSelectionRequestPart(request, messageKeys, partToolNames))
	}
	return requests
}

func balancedToolNameParts(toolNames []string, partCount int) [][]string {
	parts := [][]string{}
	startIndex := 0
	for remainingPartCount := partCount; remainingPartCount > 0; remainingPartCount-- {
		endIndex := startIndex + len(toolNames[startIndex:])/remainingPartCount
		parts = append(parts, toolNames[startIndex:endIndex])
		startIndex = endIndex
	}
	return parts
}

func toolSelectionRequestPart(request agentcontract.IntakeDecisionRequest, messageKeys []string, toolNames []string) model.DecisionRequest {
	return model.DecisionRequest{
		State:     buildDecisionState(request, decisionToolDescriptions(request.ToolSet, toolNames)),
		Questions: newQuestionBuilder(request).toolQuestions(messageKeys, toolNames),
	}
}
