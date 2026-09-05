package agentcontract

import (
	"encoding/json"
	"strings"
)

func PriorTaskContextDescription(context PriorTaskContext) string {
	if !priorTaskContextHasContent(context) {
		return ""
	}
	document, errorValue := json.Marshal(context)
	if errorValue != nil {
		return ""
	}
	return strings.Join([]string{
		"Prior task context:",
		string(document),
		"The prior result, failureReason, and inferred outcomeContract are the previous assistant's hypotheses. Re-read the original user messages and their corrections before choosing identities, titles, or a recovery route. A retry authorizes another attempt at the user's outcome, not replaying the old interpretation or calls. recordedAttempts contains recent executed tool calls and recorded failures/effects; omittedAttemptCount and toolInputOmitted mark evidence excluded for size. Do not infer unrecorded calls from the assistant's summary. Missing effects are not proof that nothing was saved, especially after a result-validation failure. Inspect current state before repeating a mutation whose outcome is uncertain. Use the current tool contracts and current persona; the previous assistant may have used an older deployment.",
		"This is context for interpreting the latest user message, not permission to finish from old text. If the latest user message asks to deliver, retry, continue, or revise this prior task's outcome, set priorTaskReference=outcome_recovery. If it is unrelated or self-contained, set priorTaskReference=none. When recovering an outcome, infer the needed structured output formats from the prior task prompt, prior result, known contract, and latest user message. A file deliverable reaches the user only through successful file_deliver completionEvidence in the current task; a prepared file, generated path, task link, or prior result text is not delivery.",
	}, "\n")
}

func priorTaskContextHasContent(context PriorTaskContext) bool {
	return strings.TrimSpace(context.TaskRunID) != "" ||
		strings.TrimSpace(context.Prompt) != "" ||
		strings.TrimSpace(context.Result) != ""
}
