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
	if callContext.errorValue != nil {
		record.IsError = true
		record.Error = callContext.errorValue.Error()
	}
	return record
}

func toolSelectionRecord(request agentcontract.IntakeDecisionRequest, answers map[string]model.DecisionAnswer, callError error) *agentcontract.ToolSelectionRecord {
	candidateToolNames := resolveCallableToolNames(request)
	if callError != nil || len(candidateToolNames) == 0 {
		return nil
	}
	record := agentcontract.ToolSelectionRecord{ProbabilityThreshold: likelyToolProbabilityThreshold, CountLimit: likelyToolCountLimit, Probabilities: map[string]float64{}}
	for index := range request.Messages {
		messageKey := decisionMessageKey(index)
		reader := answerReader{answers: answers, messageKey: messageKey}
		probabilityByToolName, errorValue := reader.toolProbabilities(candidateToolNames)
		if errorValue != nil {
			continue
		}
		for toolName, probability := range recordedToolProbabilities(probabilityByToolName) {
			record.Probabilities[messageKey+"."+toolName] = probability
		}
		for _, toolName := range selectLikelyToolNames(probabilityByToolName, candidateToolNames) {
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
