package loop

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type attemptLedgerEntry struct {
	ObservationID      string `json:"observationID"`
	ToolName           string `json:"toolName"`
	ToolInputKey       string `json:"toolInputKey,omitempty"`
	AttemptFingerprint string `json:"attemptFingerprint,omitempty"`
	FailureStage       string `json:"failureStage,omitempty"`
	ErrorCode          string `json:"errorCode,omitempty"`
	RecoveryStep       string `json:"recoveryStep,omitempty"`
	Status             string `json:"status"`
}

type failureReportFacts struct {
	Attempts    []failureReportAttempt `json:"attempts"`
	BudgetState string                 `json:"budgetState"`
}

type failureReportAttempt struct {
	ToolName     string `json:"toolName"`
	InputSummary string `json:"inputSummary"`
	ErrorCode    string `json:"errorCode"`
	FailureStage string `json:"failureStage"`
	Message      string `json:"message"`
}

func attemptLedger(observations []turnObservation) []attemptLedgerEntry {
	entries := []attemptLedgerEntry{}
	for _, observation := range observations {
		if observation.Action != "continue" || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		status := "success"
		if observation.Failed() {
			status = "error"
		}
		entries = append(entries, attemptLedgerEntry{
			ObservationID:      observation.ObservationID,
			ToolName:           strings.TrimSpace(observation.Tool),
			ToolInputKey:       strings.TrimSpace(observation.ToolInputKey),
			AttemptFingerprint: strings.TrimSpace(observation.AttemptFingerprint),
			FailureStage:       observation.FailureStage(),
			ErrorCode:          observation.FailureCode(),
			RecoveryStep:       strings.TrimSpace(observation.RecoveryStep),
			Status:             status,
		})
	}
	return entries
}

func buildFailureReportFacts(observations []turnObservation, budget RecoveryBudget) failureReportFacts {
	facts := failureReportFacts{BudgetState: failureReportBudgetState(observations, budget)}
	for _, observation := range observations {
		if observation.Action != "continue" || !observation.Failed() {
			continue
		}
		facts.Attempts = append(facts.Attempts, failureReportAttempt{
			ToolName:     strings.TrimSpace(observation.Tool),
			InputSummary: failureReportInputSummary(observation.ToolInputKey),
			ErrorCode:    firstNonEmptyString(observation.FailureCode(), toolcontract.FailureCodes.OperationFailed.String()),
			FailureStage: firstNonEmptyString(observation.FailureStage(), strings.TrimSpace(observation.Tool)),
			Message:      failureReportMessage(observation),
		})
	}
	return facts
}

func failureReportBudgetState(observations []turnObservation, budget RecoveryBudget) string {
	budget = normalizeRecoveryBudget(budget)
	if budget.NoToolFallback > 0 {
		return "no_tool_fallback_available"
	}
	if recoveryStepUseCount(observations, recoveryStepCorrectedRetry) < budget.CorrectedRetry ||
		recoveryStepUseCount(observations, recoveryStepAlternateRoute) < budget.AlternateRoute ||
		recoveryStepUseCount(observations, recoveryStepAdjacentTool) < budget.AdjacentTool {
		return "recovery_tools_available"
	}
	return "failure_report_required"
}

func failureReportInputSummary(toolInputKey string) string {
	parts := strings.SplitN(toolInputKey, "\x00", 2)
	if len(parts) != 2 {
		return truncateText(compactWhitespace(redactUnsafeText(toolInputKey)), 120)
	}
	var document map[string]any
	if json.Unmarshal([]byte(parts[1]), &document) == nil {
		for _, fieldName := range []string{"expression", "query", "url", "message", "command"} {
			if value, isString := document[fieldName].(string); isString && strings.TrimSpace(value) != "" {
				return truncateText(compactWhitespace(redactUnsafeText(value)), 120)
			}
		}
	}
	return truncateText(compactWhitespace(redactUnsafeText(parts[1])), 120)
}

func failureReportMessage(observation turnObservation) string {
	if terminalSummary := summarizeTerminalFailure(observation); terminalSummary != "" {
		return truncateText(compactWhitespace(redactUnsafeText(terminalSummary)), 240)
	}
	message := observation.FailureSummary()
	if message == "" {
		message = strings.TrimSpace(observation.ContentText())
	}
	return truncateText(compactWhitespace(redactUnsafeText(message)), 240)
}

func failureDebtActionContractMessage(facts failureReportFacts) string {
	return strings.Join([]string{
		"FailureDebt is active. The action schema now requires failureResolution.",
		"A fail action includes the final requester-language reply in message. State the blocker and any uncertain outcome concisely; validated failure facts let the runtime deliver it directly without further model calls.",
		"Budget is a ceiling, not an instruction to spend it. Judge whether the failure can be repaired with the tools, permissions, and information available to you.",
		"If a RecoveryPacket is present, use its facts and suggested tools to identify a repair or an independent route. Before another attempt, explain in the action reason which evidence makes it useful. Do not change titles, people, or other unrelated fields merely to produce a different input.",
		"Do not repeat a failed tool while RecoveryPacket.forbiddenRepeats applies; use an inspect/edit/repair/change-route action first.",
		"If you can answer directly without tools, send a final reply with failureResolution=no_tool_fallback and do not apologize or mention the failed tool unless the user asked about internals.",
		"If no evidence-backed recovery is available, return fail with failureResolution=failure_report immediately, even with budget remaining. Explain the blocker in reason and copy the recorded facts into usedFailureFacts. Do not invent a missing prerequisite or call an error temporary without evidence. A failed result check does not prove that a mutation was not saved; verify current state when possible and report uncertainty when it is not.",
		"FailureReportFacts:\n" + marshalEventBody(facts),
	}, "\n")
}

func isRecoveredFailureDebtResolution(failureResolution string) bool {
	switch strings.TrimSpace(failureResolution) {
	case failureResolutionRecoveredWithSuccess, failureResolutionNoToolFallback:
		return true
	default:
		return false
	}
}

func validateFailureReportAction(actionDocument turnActionDocument, facts failureReportFacts) completionGateResult {
	if strings.TrimSpace(actionDocument.FailureResolution) != failureResolutionFailureReport {
		return completionGateResult{Message: "FailureDebt failure reports require failureResolution=failure_report"}
	}
	if len(actionDocument.UsedFailureFacts.Attempts) == 0 {
		return completionGateResult{Message: "FailureDebt failure reports require usedFailureFacts.attempts"}
	}
	if actionDocument.UsedFailureFacts.BudgetState != facts.BudgetState {
		return completionGateResult{Message: "FailureDebt failure reports must copy usedFailureFacts.budgetState from FailureReportFacts"}
	}
	if !failureReportAttemptsAreRecorded(actionDocument.UsedFailureFacts.Attempts, facts.Attempts) {
		return completionGateResult{Message: "FailureDebt failure reports must copy each cited attempt exactly from FailureReportFacts. Do not add unrecorded attempts or count one recorded call more than once."}
	}
	if expectedAttempt, hasExpectedAttempt := latestFailureReportAttempt(facts); hasExpectedAttempt && !usedFailureFactsContainAttempt(actionDocument.UsedFailureFacts.Attempts, expectedAttempt) {
		return completionGateResult{Message: "FailureDebt failure reports must include the latest recorded failed attempt"}
	}
	return completionGateResult{IsSatisfied: true}
}

func failureReportAttemptsAreRecorded(attempts []failureReportAttempt, recordedAttempts []failureReportAttempt) bool {
	remaining := make(map[failureReportAttempt]int, len(recordedAttempts))
	for _, attempt := range recordedAttempts {
		remaining[attempt]++
	}
	for _, attempt := range attempts {
		if remaining[attempt] == 0 {
			return false
		}
		remaining[attempt]--
	}
	return true
}

func latestFailureReportAttempt(facts failureReportFacts) (failureReportAttempt, bool) {
	for index := len(facts.Attempts) - 1; index >= 0; index-- {
		if strings.TrimSpace(facts.Attempts[index].ToolName) != "" {
			return facts.Attempts[index], true
		}
	}
	return failureReportAttempt{}, false
}

func usedFailureFactsContainAttempt(attempts []failureReportAttempt, expectedAttempt failureReportAttempt) bool {
	for _, attempt := range attempts {
		if attempt == expectedAttempt {
			return true
		}
	}
	return false
}
