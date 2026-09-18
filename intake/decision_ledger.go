package intake

import (
	"encoding/json"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type decisionCallRecord struct {
	request              model.DecisionRequest
	response             model.DecisionResponse
	latency              time.Duration
	errorValue           error
	decisions            agentcontract.IntakeDecisions
	messageCount         int
	attachmentsDescribed bool
}

func recordDecisionCall(callLedger *agentcontract.IntakeCallLedger, call decisionCallRecord) {
	if callLedger == nil {
		return
	}
	callLedger.Observe(decisionLLMCallRecord(call))
}

func decisionLLMCallRecord(call decisionCallRecord) agentcontract.LLMCallRecord {
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
		DecisionDraws:          reactionDrawsOf(call.decisions),
		DecidedMessageCount:    call.messageCount,
		AttachmentsDescribed:   call.attachmentsDescribed,
		AttachmentDescriptions: attachmentDescriptionsOf(call.decisions),
	}
	if call.errorValue != nil {
		record.IsError = true
		record.Error = call.errorValue.Error()
	}
	return record
}

func reactionDrawsOf(decisions agentcontract.IntakeDecisions) map[string]float64 {
	draws := map[string]float64{}
	for index, decision := range decisions.Messages {
		draws[decisionMessageKey(index)+"."+questionNameReaction] = decision.ReactionDraw
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
