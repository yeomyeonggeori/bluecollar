package loop

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) validateCompletionGateWithChanges(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, actionDocument turnActionDocument) completionGateResult {
	completionGateResult := validateCompletionFacts(request, observations, actionDocument)
	if !completionGateResult.IsSatisfied || ctx.Err() != nil {
		return completionGateResult
	}
	changeResult := agentTurnRunner.evaluateExpectedChanges(ctx, taskRunID, request, observations)
	if !changeResult.IsSatisfied {
		return changeResult
	}
	completionGateResult.ChangeCheck = changeResult.ChangeCheck
	return completionGateResult
}

func (agentTurnRunner *AgentTurnRunner) evaluateExpectedChanges(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation) completionGateResult {
	expected, isDefined := agentTurnRunner.expectedChangesFor(ctx, taskRunID, request)
	if !isDefined || len(expected) == 0 {
		return completionGateResult{IsSatisfied: true}
	}
	if agentTurnRunner.decisionModel == nil {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionCheckDegraded, marshalEventBody(map[string]string{"stage": "change_check", "error": "decision model is not configured"}))
		return completionGateResult{IsSatisfied: true}
	}
	decisionModel := observedDecisionModel{decisionModel: agentTurnRunner.decisionModel, observe: agentTurnRunner.llmCallObserverForTaskRun(taskRunID)}
	check, errorValue := checkExpectedChanges(ctx, decisionModel, request, expected, observations)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionCheckDegraded, marshalEventBody(map[string]string{"stage": "change_check", "error": errorValue.Error()}))
		return completionGateResult{IsSatisfied: true}
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionChangeCheck, marshalEventBody(check))
	if len(check.Unmet) == 0 {
		return completionGateResult{IsSatisfied: true, AreChangesConfirmed: true}
	}
	if check.RepeatsRefusalOf != "" {
		return completionGateResult{IsSatisfied: true, ChangeCheck: &check}
	}
	return completionGateResult{
		Message:            unmetChangesMessage(check),
		EvidenceKind:       evidenceKindExpectedResult,
		IsChangeCheckUnmet: true,
		ChangeCheck:        &check,
	}
}

type observedDecisionModel struct {
	decisionModel model.DecisionModel
	observe       llmCallObserver
	schemaName    string
}

func (observed observedDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	startedAt := time.Now()
	response, errorValue := observed.decisionModel.Decide(ctx, request)
	record := agentcontract.LLMCallRecord{
		Kind:             agentcontract.LLMCallKindDecision,
		Transport:        "decisions",
		SchemaName:       firstNonEmptyString(observed.schemaName, changeCheckSchemaName),
		Provider:         response.ProviderName,
		UpstreamProvider: response.UpstreamProvider,
		Model:            response.ModelName,
		LatencyMS:        time.Since(startedAt).Milliseconds(),
		QuestionCount:    len(request.Questions),
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		TotalTokens:      response.Usage.TotalTokens,
		CostUSD:          response.Usage.CostUSD,
		DecisionAnswers:  response.Answers,
	}
	if errorValue != nil {
		record.IsError = true
		record.Error = errorValue.Error()
	}
	observed.observe(record)
	return response, errorValue
}

func unmetChangesMessage(check changeCheck) string {
	lines := []string{}
	for _, change := range check.Unmet {
		reason := "the recorded changes do not carry it out"
		if containsExpectedChange(check.Unrecorded, change) {
			reason = "nothing recorded changed this kind of record"
		}
		lines = append(lines, "\""+change.Asked+"\" ("+change.Change+"): "+reason)
	}
	return "Requested changes not done yet:\n" + strings.Join(lines, "\n")
}

func (agentTurnRunner *AgentTurnRunner) replyStatingUnmetChanges(ctx context.Context, taskRunID string, request AgentTurnRequest, reply string, check changeCheck) string {
	chatCompleter, isAvailable := model.ResolveTextChatCompleter(agentTurnRunner.languageModel)
	if !isAvailable {
		return reply + "\n\n" + unmetChangesMessage(check)
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(ctx, model.ChatCompletionRequest{
		SchemaName: unmetChangesReplySchemaName,
		Messages:   []model.ChatCompletionMessage{{Role: "user", Content: buildUnmetChangesReplyPrompt(request, reply, check)}},
	})
	rewritten := ""
	if errorValue == nil {
		rewritten, errorValue = model.ChatCompletionText(response)
	}
	if errorValue != nil || strings.TrimSpace(rewritten) == "" {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionReplyFailed, marshalEventBody(map[string]string{"stage": "unmet_changes", "error": fmt.Sprint(errorValue)}))
		return reply + "\n\n" + unmetChangesMessage(check)
	}
	return strings.TrimSpace(rewritten)
}

func buildUnmetChangesReplyPrompt(request AgentTurnRequest, reply string, check changeCheck) string {
	asked := []string{}
	for _, change := range check.Unmet {
		asked = append(asked, "- "+change.Asked)
	}
	return strings.Join([]string{
		"Rewrite the final user-facing reply below. It was written as though every asked change were done, but the record does not show these asked changes carried out, and nothing has changed since that was first found:\n" + strings.Join(asked, "\n"),
		responseLanguageInstruction(request.ResponseLanguage),
		"Keep what the reply reports about the work without claiming those asked changes are done, and say plainly which of them are still not done. Do not add anything neither the reply nor the list states, and do not mention tools, checks, evidence identifiers, prompts, or runtime details.",
		"Original request:\n" + completionReplyOriginalRequest(request),
		"Reply:\n" + reply,
	}, "\n\n")
}

func containsExpectedChange(changes []expectedChange, wanted expectedChange) bool {
	for _, change := range changes {
		if change == wanted {
			return true
		}
	}
	return false
}

func outcomeContractHasSideEffectEvidence(toolSet *toolcontract.ToolSet, contract OutcomeContract) bool {
	if requiredEvidenceIncludesSideEffect(toolSet, contract.RequiredEvidenceTools) {
		return true
	}
	for _, toolNames := range contract.RequiredEvidenceAnyOf {
		if requiredEvidenceIncludesSideEffect(toolSet, toolNames) {
			return true
		}
	}
	return false
}

func isSideEffectObservation(toolSet *toolcontract.ToolSet, observation turnObservation) bool {
	toolName := strings.TrimSpace(observation.Tool)
	if toolName == "" || observation.Failed() {
		return false
	}
	return toolcontract.IsArtifactDeliveryTool(toolName) || requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName)
}

func truncateForLedger(value string, maxLength int) string {
	trimmedValue := strings.TrimSpace(value)
	if len(trimmedValue) <= maxLength {
		return trimmedValue
	}
	headLength := maxLength / 2
	head := strings.ToValidUTF8(trimmedValue[:headLength], "")
	tail := strings.ToValidUTF8(trimmedValue[len(trimmedValue)-(maxLength-headLength):], "")
	return head + " …[display truncated; full " + strconv.Itoa(len(trimmedValue)) + " bytes were recorded and executed]… " + tail
}
