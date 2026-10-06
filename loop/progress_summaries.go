package loop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func summarizeStructuredFailure(observation turnObservation) string {
	if terminalSummary := summarizeTerminalFailure(observation); terminalSummary != "" {
		return terminalSummary
	}
	parts := []string{}
	if observation.FailureCode() != "" {
		parts = append(parts, "errorCode="+observation.FailureCode())
	}
	if observation.FailureStage() != "" {
		parts = append(parts, "failureStage="+observation.FailureStage())
	}
	if observation.FailureSummary() != "" {
		parts = append(parts, "message="+truncateText(compactWhitespace(observation.FailureSummary()), 240))
	}
	if len(parts) == 0 {
		return ""
	}
	if observation.FailureSummary() == "" {
		parts = append(parts, "message="+truncateText(compactWhitespace(redactUnsafeText(observation.ContentText())), 240))
	}
	return strings.Join(parts, "; ")
}

// summarizeTerminalRun keeps a shell result diagnosable instead of
// collapsing a long build log to a bare "success": it always surfaces the exit
// code and the tail of stdout and stderr, so warnings like a failed browser
// render are visible in the task record and to the model.
func summarizeTerminalRun(observation turnObservation) string {
	tail, ok := terminalObservationTail(observation)
	if !ok {
		return ""
	}
	if len(tail.StdoutTail) == 0 && len(tail.StderrTail) == 0 {
		return ""
	}
	parts := []string{}
	if tail.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exitCode=%d", *tail.ExitCode))
	}
	if tail.TimedOut {
		parts = append(parts, "timedOut=true")
	}
	if len(tail.StdoutTail) > 0 {
		parts = append(parts, "stdout:\n"+strings.Join(tail.StdoutTail, "\n"))
	}
	if len(tail.StderrTail) > 0 {
		parts = append(parts, "stderr:\n"+strings.Join(tail.StderrTail, "\n"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func summarizeTerminalFailure(observation turnObservation) string {
	if strings.TrimSpace(observation.Tool) != toolcontract.BashToolName {
		return ""
	}
	tail, ok := terminalObservationTail(observation)
	if !ok {
		return ""
	}
	parts := []string{}
	if observation.FailureCode() != "" {
		parts = append(parts, "errorCode="+observation.FailureCode())
	}
	if observation.FailureStage() != "" {
		parts = append(parts, "failureStage="+observation.FailureStage())
	}
	if tail.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exitCode=%d", *tail.ExitCode))
	}
	if len(tail.StderrTail) > 0 {
		parts = append(parts, "stderrTail="+truncateText(compactWhitespace(strings.Join(tail.StderrTail, " | ")), 240))
	} else if len(tail.StdoutTail) > 0 {
		parts = append(parts, "stdoutTail="+truncateText(compactWhitespace(strings.Join(tail.StdoutTail, " | ")), 240))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func carriesPageSnapshot(content string) bool {
	var document struct {
		SnapshotText *string `json:"snapshotText"`
	}
	return json.Unmarshal([]byte(content), &document) == nil && document.SnapshotText != nil
}

func carriesImageAttachment(observation turnObservation) bool {
	for _, attachment := range observation.Attachments {
		if strings.HasPrefix(attachment.ContentType, "image/") {
			return true
		}
	}
	return false
}

func summarizeBrowserSnapshot(content string) string {
	var document map[string]any
	if json.Unmarshal([]byte(content), &document) != nil {
		return "Browser snapshot captured. " + truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	parts := []string{}
	if value := stringField(document, "url"); value != "" {
		parts = append(parts, "url="+value)
	}
	if value := stringField(document, "title"); value != "" {
		parts = append(parts, "title="+value)
	}
	if value := stringField(document, "snapshotText"); value != "" {
		parts = append(parts, "visibleText="+truncateText(compactWhitespace(value), maxSummaryTextLength))
	}
	references := stringSliceField(document, "interactiveRefs")
	if len(references) > maxInteractiveReferences {
		references = references[:maxInteractiveReferences]
	}
	if len(references) > 0 {
		parts = append(parts, "interactiveRefs="+strings.Join(references, ", "))
	}
	if booleanField(document, "hasMore") {
		parts = append(parts, "hasMore=true")
	}
	if len(parts) == 0 {
		return "Browser snapshot captured."
	}
	return strings.Join(parts, "; ")
}

func summarizeCollection(content string) string {
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	switch typedValue := value.(type) {
	case []any:
		excerpts := []string{}
		for _, item := range typedValue {
			excerpt := summarizeJSONValue(item, []string{"speaker", "text", "content", "title", "fact", "score"})
			if excerpt != "" {
				excerpts = append(excerpts, excerpt)
			}
			if len(excerpts) >= 5 {
				break
			}
		}
		return fmt.Sprintf("Returned %d item(s). Top excerpts: %s", len(typedValue), strings.Join(excerpts, " | "))
	default:
		return summarizeJSONValue(value, []string{"messages", "hasMoreBefore", "historyCursor", "facts"})
	}
}

func summarizeSafeJSONFields(content string, fieldNames []string) string {
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return truncateText(compactWhitespace(redactUnsafeText(content)), 500)
	}
	summary := summarizeJSONValue(value, fieldNames)
	if summary == "" {
		return "Tool completed successfully."
	}
	return summary
}

func summarizeJSONValue(value any, fieldNames []string) string {
	document, isDocument := value.(map[string]any)
	if !isDocument {
		return truncateText(compactWhitespace(fmt.Sprintf("%v", value)), 500)
	}
	parts := []string{}
	for _, fieldName := range fieldNames {
		fieldValue, isFound := document[fieldName]
		if !isFound {
			continue
		}
		if isUnsafePromptField(fieldName, fieldValue) {
			continue
		}
		parts = append(parts, fieldName+"="+truncateText(compactWhitespace(fmt.Sprintf("%v", fieldValue)), 300))
	}
	return strings.Join(parts, "; ")
}

func stringField(document map[string]any, fieldName string) string {
	value, isString := document[fieldName].(string)
	if !isString {
		return ""
	}
	return strings.TrimSpace(value)
}

func booleanField(document map[string]any, fieldName string) bool {
	value, isBool := document[fieldName].(bool)
	return isBool && value
}

func stringSliceField(document map[string]any, fieldName string) []string {
	values, isSlice := document[fieldName].([]any)
	if !isSlice {
		return nil
	}
	result := []string{}
	for _, value := range values {
		text, isString := value.(string)
		if isString && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

func compactWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func isUnsafePromptField(fieldName string, fieldValue any) bool {
	normalizedFieldName := strings.ToLower(fieldName)
	if isAgentWorkspacePathField(normalizedFieldName) && isAgentWorkspacePathValue(fieldValue) {
		return false
	}
	return strings.Contains(normalizedFieldName, "path") || strings.Contains(normalizedFieldName, "cookie") || strings.Contains(normalizedFieldName, "token") || strings.Contains(normalizedFieldName, "authorization") || strings.Contains(normalizedFieldName, "cdp") || strings.Contains(normalizedFieldName, "profile")
}

func isAgentWorkspacePathField(fieldName string) bool {
	return fieldName == "workspacepath" || fieldName == "sourceworkspacepath"
}

func isAgentWorkspacePathValue(value any) bool {
	text, isString := value.(string)
	if !isString {
		return false
	}
	trimmedText := strings.TrimSpace(text)
	return strings.HasPrefix(trimmedText, "~/") || trimmedText == "~"
}
