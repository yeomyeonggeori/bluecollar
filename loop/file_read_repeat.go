package loop

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func repeatedFileReadObservation(observations []turnObservation, actionDocument turnActionDocument, observationID string) (turnObservation, bool) {
	if strings.TrimSpace(actionDocument.ToolName) != "file_read" {
		return turnObservation{}, false
	}
	requestedRange, ok := fileReadRequestedRange(actionDocument.ToolInput)
	if !ok {
		return turnObservation{}, false
	}
	recoveryDirective := stalledReadRecoveryDirective(observations)
	for index, observation := range observations {
		fileContext, isFileRead := progressFileContextFromObservation(observation)
		if !isFileRead || fileContext.Path != requestedRange.Path {
			continue
		}
		if hasNewerFileMutationObservation(observations[index+1:], requestedRange.Path) {
			continue
		}
		for _, readRange := range fileContext.ReadRanges {
			coveredRange, ok := parseFileReadRange(readRange)
			if !ok {
				continue
			}
			if coveredRange.StartLine <= requestedRange.StartLine && coveredRange.EndLine >= requestedRange.EndLine {
				return cachedFileReadObservation(observationID, observation, "Already read "+requestedRange.Path+" lines "+readRange+" as "+observation.ObservationID+". Reuse the cached content below instead of spending another file_read call."+recoveryDirective), true
			}
			if fileReadRangesOverlap(coveredRange, requestedRange) {
				return cachedFileReadObservation(observationID, observation, "Already read overlapping lines "+readRange+" from "+requestedRange.Path+" as "+observation.ObservationID+". Reuse cached content and request only an uncovered range such as "+uncoveredFileReadHint(coveredRange, requestedRange)+" if more text is needed."+recoveryDirective), true
			}
		}
	}
	return turnObservation{}, false
}

func hasNewerFileMutationObservation(observations []turnObservation, path string) bool {
	normalizedPath := tildeInsensitivePath(path)
	for _, observation := range observations {
		if observation.Failed() || !isFileMutationTool(observation.Tool) {
			continue
		}
		for _, mutatedPath := range observationMutatedPaths(observation) {
			if tildeInsensitivePath(mutatedPath) == normalizedPath {
				return true
			}
		}
	}
	return false
}

func tildeInsensitivePath(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(path), "~/")
}

func isFileMutationTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case toolcontract.WriteToolName, toolcontract.EditToolName:
		return true
	default:
		return false
	}
}

func observationMutatedPaths(observation turnObservation) []string {
	payload := map[string]any{}
	if json.Unmarshal(observation.StructuredOutput(), &payload) != nil {
		return nil
	}
	paths := []string{}
	if path := strings.TrimSpace(stringField(payload, "path")); path != "" {
		paths = append(paths, path)
	}
	editedFiles, isList := payload["editedFiles"].([]any)
	if !isList {
		return paths
	}
	for _, editedFile := range editedFiles {
		if path, isString := editedFile.(string); isString && strings.TrimSpace(path) != "" {
			paths = append(paths, strings.TrimSpace(path))
		}
	}
	return paths
}

func stalledReadRecoveryDirective(observations []turnObservation) string {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return ""
	}
	failedTool := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	if failedTool == "" {
		return ""
	}
	return " You already have the file content and an unresolved " + failedTool + " failure. Stop re-reading: edit the file with edit to fix the cause, then re-run " + failedTool + "."
}

func cachedFileReadObservation(observationID string, previousObservation turnObservation, message string) turnObservation {
	payload := map[string]any{}
	if json.Unmarshal(previousObservation.StructuredOutput(), &payload) != nil {
		payload = map[string]any{}
	}
	payload["cacheStatus"] = "hit"
	payload["cachedObservationID"] = previousObservation.ObservationID
	payload["message"] = strings.TrimSpace(message)
	content := marshalEventBody(payload)
	observation := newContentObservation(observationID, "policy", "file_read", content)
	observation.Output.Data = json.RawMessage(content)
	observation.Summary = "file_read cache hit for " + firstNonEmptyString(stringField(payload, "path"), "previous range")
	return observation
}

func fileReadRangesOverlap(firstRange fileReadRange, secondRange fileReadRange) bool {
	return firstRange.StartLine <= secondRange.EndLine && secondRange.StartLine <= firstRange.EndLine
}

func uncoveredFileReadHint(coveredRange fileReadRange, requestedRange fileReadRange) string {
	if requestedRange.EndLine > coveredRange.EndLine {
		return strconv.Itoa(coveredRange.EndLine+1) + "-" + strconv.Itoa(requestedRange.EndLine)
	}
	if requestedRange.StartLine < coveredRange.StartLine {
		return strconv.Itoa(requestedRange.StartLine) + "-" + strconv.Itoa(coveredRange.StartLine-1)
	}
	return "a different range"
}

type fileReadRange struct {
	Path      string
	StartLine int
	EndLine   int
}

func fileReadRequestedRange(toolInput json.RawMessage) (fileReadRange, bool) {
	document := map[string]any{}
	if errorValue := json.Unmarshal(toolInput, &document); errorValue != nil {
		return fileReadRange{}, false
	}
	path := strings.TrimSpace(stringField(document, "path"))
	if path == "" {
		return fileReadRange{}, false
	}
	if intField(document, "startByte") > 0 {
		return fileReadRange{}, false
	}
	startLine := intField(document, "startLine")
	if startLine <= 0 {
		startLine = 1
	}
	lineCount := intField(document, "lineCount")
	if lineCount <= 0 {
		lineCount = 200
	}
	return fileReadRange{Path: path, StartLine: startLine, EndLine: startLine + lineCount - 1}, true
}

func parseFileReadRange(value string) (fileReadRange, bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) == 1 {
		startLine, errorValue := strconv.Atoi(parts[0])
		if errorValue != nil || startLine <= 0 {
			return fileReadRange{}, false
		}
		return fileReadRange{StartLine: startLine, EndLine: startLine}, true
	}
	if len(parts) != 2 {
		return fileReadRange{}, false
	}
	startLine, startError := strconv.Atoi(parts[0])
	endLine, endError := strconv.Atoi(parts[1])
	if startError != nil || endError != nil || startLine <= 0 || endLine < startLine {
		return fileReadRange{}, false
	}
	return fileReadRange{StartLine: startLine, EndLine: endLine}, true
}
