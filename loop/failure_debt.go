package loop

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	recoveryStepCorrectedRetry  = "corrected_retry"
	recoveryStepAlternateRoute  = "alternate_route"
	recoveryStepAdjacentTool    = "adjacent_tool"
	recoveryStepInspection      = "inspection"
	recoveryStepRejectedRepeat  = "rejected_repeat"
	recoveryStepIndependentWork = "independent_work"

	failureResolutionRecoveredWithSuccess = "recovered_with_success"
	failureResolutionNoToolFallback       = "no_tool_fallback"
	failureResolutionFailureReport        = "failure_report"
)

type FailureDebt struct {
	LatestFailure turnObservation `json:"latestFailure"`
}

type RecoveryPacket struct {
	WhatFailed          string                            `json:"whatFailed"`
	InputThatFailed     string                            `json:"inputThatFailed,omitempty"`
	WhyLikely           string                            `json:"whyLikely,omitempty"`
	MustDoNext          []string                          `json:"mustDoNext,omitempty"`
	AllowedTools        []string                          `json:"allowedTools,omitempty"`
	ForbiddenRepeats    []string                          `json:"forbiddenRepeats,omitempty"`
	EvidenceNeeded      []string                          `json:"evidenceNeeded,omitempty"`
	FailureClass        string                            `json:"failureClass,omitempty"`
	RetryPolicy         string                            `json:"retryPolicy,omitempty"`
	AffectedResources   []toolcontract.AffectedResource   `json:"affectedResources,omitempty"`
	DiagnosticArtifacts []toolcontract.DiagnosticArtifact `json:"diagnosticArtifacts,omitempty"`
}

func activeFailureDebt(observations []turnObservation) (FailureDebt, bool) {
	failureDebt, _, hasFailureDebt := failureDebtWithEpisodeStart(observations)
	return failureDebt, hasFailureDebt
}

func failureDebtWithEpisodeStart(observations []turnObservation) (FailureDebt, int, bool) {
	var activeDebt FailureDebt
	episodeStartIndex := 0
	for index, observation := range observations {
		if observation.Action != "continue" {
			continue
		}
		if observation.Failed() && strings.TrimSpace(observation.ToolInputKey) != "" {
			if strings.TrimSpace(activeDebt.LatestFailure.ObservationID) == "" {
				episodeStartIndex = index
			}
			activeDebt = FailureDebt{LatestFailure: observation}
			continue
		}
		if !observation.Failed() && strings.TrimSpace(activeDebt.LatestFailure.ObservationID) != "" {
			if successfulObservationKeepsFailureDebt(observation) {
				continue
			}
			activeDebt = FailureDebt{}
		}
	}
	return activeDebt, episodeStartIndex, strings.TrimSpace(activeDebt.LatestFailure.ObservationID) != ""
}

func recoveryEpisodeObservations(observations []turnObservation) []turnObservation {
	_, episodeStartIndex, hasFailureDebt := failureDebtWithEpisodeStart(observations)
	if !hasFailureDebt {
		return nil
	}
	return observations[episodeStartIndex:]
}

func successfulObservationKeepsFailureDebt(observation turnObservation) bool {
	if strings.TrimSpace(observation.RecoveryStep) == recoveryStepIndependentWork {
		return true
	}
	return successfulObservationIsInspection(observation)
}

func successfulObservationIsInspection(observation turnObservation) bool {
	if strings.TrimSpace(observation.RecoveryStep) == recoveryStepInspection {
		return true
	}
	return observation.ToolIsReadOnly
}

func attemptFingerprint(toolInputKey string, errorCode string) string {
	normalizedToolInputKey := strings.TrimSpace(toolInputKey)
	normalizedErrorCode := strings.TrimSpace(errorCode)
	if normalizedErrorCode == "" {
		normalizedErrorCode = toolcontract.FailureCodes.OperationFailed.String()
	}
	if normalizedToolInputKey == "" {
		return normalizedErrorCode
	}
	return normalizedToolInputKey + "\x00" + normalizedErrorCode
}

func previousNonRetryableToolFailure(observations []turnObservation, toolName string) (turnObservation, bool) {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return turnObservation{}, false
	}
	for _, observation := range observations {
		if observation.Action == "policy" {
			continue
		}
		if observation.Failure == nil || strings.TrimSpace(observation.Failure.RetryPolicy) != toolcontract.RetryPolicyDoNotRetry {
			continue
		}
		if strings.TrimSpace(observation.Tool) == trimmedToolName {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func previousFailedToolInput(observations []turnObservation, toolName string, toolInput json.RawMessage) (turnObservation, bool) {
	expectedKey := canonicalToolCallKey(toolName, toolInput)
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "continue" {
			continue
		}
		if !observation.Failed() {
			return turnObservation{}, false
		}
		if strings.TrimSpace(observation.ToolInputKey) == expectedKey {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func classifyRecoveryStep(toolSet *toolcontract.ToolSet, failureDebt FailureDebt, toolName string, toolInput json.RawMessage) string {
	failedToolName := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	recoveryToolName := strings.TrimSpace(toolName)
	if failedToolName == recoveryToolName {
		return recoveryStepCorrectedRetry
	}
	if evidenceToolIsReadOnly(toolSet, recoveryToolName) {
		return recoveryStepInspection
	}
	if hasDistinctTarget(failureDebt.LatestFailure.ToolInputKey, toolInput) {
		return recoveryStepIndependentWork
	}
	if isAlternateRouteToolPair(toolSet, failedToolName, recoveryToolName) {
		return recoveryStepAlternateRoute
	}
	return recoveryStepAdjacentTool
}

func hasDistinctTarget(failedToolInputKey string, toolInput json.RawMessage) bool {
	failedFields := comparableInputFields(inputDocumentFromToolInputKey(failedToolInputKey))
	candidateFields := comparableInputFields(inputDocumentFromToolInput(toolInput))
	for fieldName, failedValue := range failedFields {
		candidateValue, isShared := candidateFields[fieldName]
		if isShared && candidateValue != failedValue {
			return true
		}
	}
	return false
}

func comparableInputFields(inputDocument map[string]any) map[string]string {
	fields := map[string]string{}
	for fieldName, value := range inputDocument {
		comparableValue := comparableInputValue(value)
		if comparableValue == "" {
			continue
		}
		fields[strings.TrimSpace(fieldName)] = comparableValue
	}
	return fields
}

func comparableInputValue(value any) string {
	switch typedValue := value.(type) {
	case string:
		return strings.TrimSpace(typedValue)
	case float64:
		return strconv.FormatFloat(typedValue, 'f', -1, 64)
	default:
		return ""
	}
}

func inputDocumentFromToolInputKey(toolInputKey string) map[string]any {
	_, inputDocument, isFound := strings.Cut(toolInputKey, "\x00")
	if !isFound {
		return nil
	}
	return inputDocumentFromToolInput(json.RawMessage(inputDocument))
}

func inputDocumentFromToolInput(toolInput json.RawMessage) map[string]any {
	if len(toolInput) == 0 {
		return nil
	}
	inputDocument := map[string]any{}
	if json.Unmarshal(toolInput, &inputDocument) != nil {
		return nil
	}
	return inputDocument
}

func isAlternateRouteToolPair(toolSet *toolcontract.ToolSet, firstToolName string, secondToolName string) bool {
	firstNamespace := recoveryToolNamespace(toolSet, firstToolName)
	secondNamespace := recoveryToolNamespace(toolSet, secondToolName)
	return firstNamespace != "" && firstNamespace == secondNamespace
}

func recoveryToolNamespace(toolSet *toolcontract.ToolSet, toolName string) string {
	definition, isFound := toolDefinitionForName(toolSet, toolName)
	if !isFound {
		return ""
	}
	return strings.TrimSpace(definition.Namespace)
}

func repeatedFailedAttemptObservation(toolSet *toolcontract.ToolSet, index int, failedObservation turnObservation, originalInstruction string) turnObservation {
	content := "This exact tool/input/error fingerprint already failed. Do not repeat it. Recover only when evidence explains how a correction or another route addresses this failure. Answer without tools using failureResolution=no_tool_fallback if enough context exists, or fail with failureResolution=failure_report when no evidence-backed recovery is available. Budget is a ceiling, not a requirement to keep trying."
	observation := recoveryGuidanceObservation(toolSet, index, failedObservation, originalInstruction)
	observation.Action = "policy"
	observation = withObservationContent(observation, content+" "+observation.ContentText())
	observation.Summary = observation.ContentText()
	observation.RecoveryStep = recoveryStepRejectedRepeat
	observation.RecoveryAttemptSpent = true
	return observation
}

func activeFailureDebtEventBody(observations []turnObservation, budget RecoveryBudget) map[string]any {
	failureDebt, _ := activeFailureDebt(observations)
	return map[string]any{
		"failureDebt":        failureDebt,
		"failureReportFacts": buildFailureReportFacts(observations, budget),
		"attemptLedger":      attemptLedger(observations),
		"recoveryBudget":     normalizeRecoveryBudget(budget),
	}
}
