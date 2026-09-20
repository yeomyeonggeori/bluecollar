package intake

import (
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type decisionCallContext struct {
	errorValue           error
	decisions            agentcontract.IntakeDecisions
	messageCount         int
	attachmentsDescribed bool
	toolSelection        *agentcontract.ToolSelectionRecord
}

func recordDecisionCalls(callLedger *agentcontract.IntakeCallLedger, calls []decisionCall, callContext decisionCallContext) {
	if callLedger == nil {
		return
	}
	for index, call := range calls {
		callLedger.Observe(decisionLLMCallRecord(call, decidedCallContext(callContext, index)))
	}
}

func decidedCallContext(callContext decisionCallContext, index int) decisionCallContext {
	if index == 0 {
		return callContext
	}
	return decisionCallContext{errorValue: callContext.errorValue}
}

func decisionLLMCallRecord(call decisionCall, callContext decisionCallContext) agentcontract.LLMCallRecord {
	record := agentcontract.LLMCallRecord{
		Kind:                   agentcontract.LLMCallKindDecision,
		Transport:              "decisions",
		Provider:               call.response.ProviderName,
		UpstreamProvider:       call.response.UpstreamProvider,
		Model:                  call.response.ModelName,
		LatencyMS:              call.latency.Milliseconds(),
		PromptBytes:            decisionStateByteCount(call.request.State),
		SchemaBytes:            decisionQuestionsByteCount(call.request.Questions),
		QuestionCount:          len(call.request.Questions),
		PromptTokens:           call.response.Usage.PromptTokens,
		CompletionTokens:       call.response.Usage.CompletionTokens,
		TotalTokens:            call.response.Usage.TotalTokens,
		CostUSD:                call.response.Usage.CostUSD,
		DecisionAnswers:        call.response.Answers,
		DecisionDraws:          reactionDrawsOf(callContext.decisions),
		ToolSelection:          callContext.toolSelection,
		DecidedMessageCount:    callContext.messageCount,
		AttachmentsDescribed:   callContext.attachmentsDescribed,
		AttachmentDescriptions: attachmentDescriptionsOf(callContext.decisions),
	}
	if call.wasCut {
		record.UsedFallback = true
		record.FallbackReason = "the first ask was cut at the measured patience and asked again"
	}
	if callContext.errorValue != nil {
		record.IsError = true
		record.Error = callContext.errorValue.Error()
	}
	return record
}

func toolSelectionRecord(messageKeys []string, plan toolSelectionPlan, answers map[string]model.DecisionAnswer, selectionError error) *agentcontract.ToolSelectionRecord {
	if selectionError != nil {
		return nil
	}
	record := agentcontract.ToolSelectionRecord{
		ProbabilityThreshold:    likelyToolProbabilityThreshold,
		CountLimit:              likelyToolCountLimit,
		CandidateCount:          len(plan.candidateToolNames),
		BatchByteCounts:         batchByteCounts(plan.requests),
		ClippedDescriptionCount: plan.clippedDescriptionCount,
		Probabilities:           map[string]float64{},
	}
	for _, messageKey := range messageKeys {
		reader := answerReader{answers: answers, messageKey: messageKey}
		probabilityByToolName, errorValue := reader.toolProbabilities(plan.candidateToolNames)
		if errorValue != nil {
			continue
		}
		for toolName, probability := range recordedToolProbabilities(probabilityByToolName) {
			record.Probabilities[messageKey+"."+toolName] = probability
		}
		for _, toolName := range selectLikelyToolNames(probabilityByToolName, plan.candidateToolNames, likelyToolCountLimit) {
			record.SelectedToolNames = append(record.SelectedToolNames, messageKey+"."+toolName)
		}
	}
	return &record
}

func reactionDrawsOf(decisions agentcontract.IntakeDecisions) map[string]float64 {
	draws := map[string]float64{}
	for index, decision := range decisions.Messages {
		draws[decisionMessageKey(index)+"."+agentcontract.IntakeQuestionReaction] = decision.ReactionDraw
	}
	if len(draws) == 0 {
		return nil
	}
	return draws
}

func attachmentDescriptionsOf(decisions agentcontract.IntakeDecisions) []string {
	descriptions := []string{}
	for _, decision := range decisions.Messages {
		descriptions = append(descriptions, decision.AttachmentDescriptions()...)
	}
	if len(descriptions) == 0 {
		return nil
	}
	return descriptions
}

func decisionStateByteCount(state any) int {
	document, errorValue := json.Marshal(state)
	if errorValue != nil {
		return 0
	}
	return len(document)
}

func decisionQuestionsByteCount(questions map[string]model.DecisionQuestion) int {
	document, errorValue := json.Marshal(questions)
	if errorValue != nil {
		return 0
	}
	return len(document)
}
