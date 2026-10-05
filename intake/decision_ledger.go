package intake

import (
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type decisionCallContext struct {
	errorValue           error
	decisions            agentcontract.IntakeDecisions
	messageIDs           []string
	input                json.RawMessage
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
	return decisionCallContext{errorValue: callContext.errorValue, messageIDs: callContext.messageIDs}
}

func decisionLLMCallRecord(call decisionCall, callContext decisionCallContext) agentcontract.LLMCallRecord {
	record := agentcontract.DecisionCallRecord(call.request, call.response, call.latency, callContext.errorValue)
	record.DecisionDraws = reactionDrawsOf(callContext.decisions)
	record.ToolSelection = callContext.toolSelection
	record.DecidedMessageIDs = callContext.messageIDs
	record.AttachmentsDescribed = callContext.attachmentsDescribed
	record.AttachmentDescriptions = attachmentDescriptionsOf(callContext.decisions)
	record.Input = callContext.input
	if call.wasCut {
		record.UsedFallback = true
		record.FallbackReason = "the first ask was cut at the measured patience and asked again"
	}
	return record.WithWireExchange(call.wireExchange)
}

func toolSelectionRecord(messageKeys []string, plan toolSelectionPlan, answers map[string]model.DecisionAnswer, selectionError error, countLimit int) *agentcontract.ToolSelectionRecord {
	if selectionError != nil {
		return nil
	}
	record := agentcontract.ToolSelectionRecord{
		ProbabilityThreshold:    likelyToolProbabilityThreshold,
		CountLimit:              countLimit,
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
		for _, toolName := range selectLikelyToolNames(probabilityByToolName, plan.candidateToolNames, countLimit) {
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

func decidedMessageIDs(messages []agentcontract.IntakeDecisionMessage) []string {
	messageIDs := make([]string, 0, len(messages))
	for _, message := range messages {
		messageIDs = append(messageIDs, message.MessageID)
	}
	return messageIDs
}

func decisionInput(request agentcontract.IntakeDecisionRequest) json.RawMessage {
	document, errorValue := json.Marshal(request)
	if errorValue != nil {
		return nil
	}
	return document
}
