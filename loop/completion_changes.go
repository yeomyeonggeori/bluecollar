package loop

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
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
	check, isChecked := agentTurnRunner.checkRequestedChanges(ctx, taskRunID, request, observations)
	if !isChecked {
		return completionGateResult{IsSatisfied: true}
	}
	if len(check.Unmet) == 0 {
		return completionGateResult{IsSatisfied: true, AreChangesConfirmed: true}
	}
	if check.RepeatsRefusalOf != "" || check.awaitsOnlyTheRequester() {
		return completionGateResult{IsSatisfied: true, ChangeCheck: &check}
	}
	return completionGateResult{
		Message:            unmetChangesMessage(check),
		EvidenceKind:       evidenceKindExpectedResult,
		IsChangeCheckUnmet: true,
		ChangeCheck:        &check,
	}
}

func (agentTurnRunner *AgentTurnRunner) checkRequestedChanges(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation) (changeCheck, bool) {
	expected, isDefined := agentTurnRunner.expectedChangesFor(ctx, taskRunID, request)
	if !isDefined || len(expected) == 0 {
		return changeCheck{}, false
	}
	if check, isKnown := agentTurnRunner.latestChangeCheck(taskRunID, len(observations)); isKnown {
		return check, true
	}
	if agentTurnRunner.decisionModel == nil {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionCheckDegraded, marshalEventBody(map[string]string{"stage": "change_check", "error": "decision model is not configured"}))
		return changeCheck{}, false
	}
	decisionModel := observedDecisionModel{decisionModel: agentTurnRunner.decisionModel, observe: agentTurnRunner.llmCallObserverForTaskRun(taskRunID)}
	check, errorValue := checkExpectedChanges(ctx, decisionModel, request, expected, observations)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionCheckDegraded, marshalEventBody(map[string]string{"stage": "change_check", "error": errorValue.Error()}))
		return changeCheck{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionChangeCheck, marshalEventBody(check))
	agentTurnRunner.rememberChangeCheck(taskRunID, len(observations), check)
	return check, true
}

func (agentTurnRunner *AgentTurnRunner) requestedChangesAtStop(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation) []agentcontract.RequestedChange {
	check, isChecked := agentTurnRunner.checkRequestedChanges(ctx, taskRunID, request, observations)
	if !isChecked {
		return nil
	}
	requestedChanges := make([]agentcontract.RequestedChange, 0, len(check.ExpectedChanges))
	for _, change := range check.ExpectedChanges {
		requestedChanges = append(requestedChanges, agentcontract.RequestedChange{
			Change:       change,
			IsCarriedOut: !slices.Contains(check.Unmet, change),
		})
	}
	return requestedChanges
}

type observedDecisionModel struct {
	decisionModel model.DecisionModel
	observe       llmCallObserver
	schemaName    string
}

func (observed observedDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	startedAt := time.Now()
	response, errorValue := observed.decisionModel.Decide(ctx, request)
	record := agentcontract.DecisionCallRecord(request, response, time.Since(startedAt), errorValue)
	record.SchemaName = firstNonEmptyString(observed.schemaName, changeCheckSchemaName)
	observed.observe(record)
	return response, errorValue
}

func unmetChangesMessage(check changeCheck) string {
	lines := []string{}
	for _, change := range check.Unmet {
		reason := "what was done does not carry it out"
		if fieldName := check.unwrittenFieldOf(change); fieldName != "" {
			reason = "it is held in the field " + fieldName + ", and no recorded write set that field"
		}
		if slices.Contains(check.AwaitsRequester, change) {
			reason = "it needs information only the requester can give, which the final reply will ask them for"
		}
		lines = append(lines, "\""+change+"\": "+reason)
	}
	return "Requested changes not done yet:\n" + strings.Join(lines, "\n")
}

func (check changeCheck) unwrittenFieldOf(wanted string) string {
	index := slices.Index(check.ExpectedChanges, wanted)
	if index < 0 {
		return ""
	}
	return check.UnwrittenFields[changeQuestionKey(index)]
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
	return toolexposure.IsArtifactDeliveryTool(toolName) || requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName)
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
