package intake

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
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
	plan := planToolSelection(request, messageKeys, candidateToolNames)
	calls := planner.decideEveryRequest(ctx, plan.requests)
	answers := mergedDecisionAnswers(calls)
	selectedToolNames, selectionError := likelyToolNamesByMessageKey(messageKeys, candidateToolNames, answers, firstCallError(calls))
	recordDecisionCalls(callLedger, calls, decisionCallContext{
		errorValue:    selectionError,
		toolSelection: toolSelectionRecord(messageKeys, plan, answers, selectionError),
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
