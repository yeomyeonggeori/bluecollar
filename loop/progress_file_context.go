package loop

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ProgressFileContext struct {
	Path                 string   `json:"path"`
	LastObservationID    string   `json:"lastObservationID"`
	ReadRanges           []string `json:"readRanges,omitempty"`
	TotalLines           int      `json:"totalLines,omitempty"`
	TotalLinesKnown      bool     `json:"totalLinesKnown,omitempty"`
	SizeBytes            int      `json:"sizeBytes,omitempty"`
	OriginalSizeBytes    int      `json:"originalSizeBytes,omitempty"`
	ReturnedBytes        int      `json:"returnedBytes,omitempty"`
	IsTruncated          bool     `json:"isTruncated,omitempty"`
	Summary              string   `json:"summary,omitempty"`
	Snippet              string   `json:"snippet,omitempty"`
	MarkdownPreview      string   `json:"markdownPreview,omitempty"`
	ConversionStatus     string   `json:"conversionStatus,omitempty"`
	ConversionMessage    string   `json:"conversionMessage,omitempty"`
	RepeatedReadGuidance string   `json:"repeatedReadGuidance,omitempty"`
}

func recentFileContexts(observations []turnObservation) []ProgressFileContext {
	byPath := map[string]ProgressFileContext{}
	order := []string{}
	for _, observation := range observations {
		context, isFileRead := progressFileContextFromObservation(observation)
		if !isFileRead {
			continue
		}
		existingContext, exists := byPath[context.Path]
		if !exists {
			order = append(order, context.Path)
			byPath[context.Path] = context
			continue
		}
		context.ReadRanges = appendUniqueStrings(append(existingContext.ReadRanges, context.ReadRanges...))
		if existingContext.Summary != "" && context.Summary == "" {
			context.Summary = existingContext.Summary
		}
		if existingContext.Snippet != "" && context.Snippet == "" {
			context.Snippet = existingContext.Snippet
		}
		if existingContext.MarkdownPreview != "" && context.MarkdownPreview == "" {
			context.MarkdownPreview = existingContext.MarkdownPreview
		}
		if existingContext.ConversionStatus != "" && context.ConversionStatus == "" {
			context.ConversionStatus = existingContext.ConversionStatus
		}
		if existingContext.ConversionMessage != "" && context.ConversionMessage == "" {
			context.ConversionMessage = existingContext.ConversionMessage
		}
		if existingContext.OriginalSizeBytes > context.OriginalSizeBytes {
			context.OriginalSizeBytes = existingContext.OriginalSizeBytes
		}
		context.RepeatedReadGuidance = repeatedReadGuidance(context.Path, context.ReadRanges)
		if context.RepeatedReadGuidance == "" {
			context.RepeatedReadGuidance = "Already read " + strings.TrimSpace(context.Path) + "; use this context, read a different range, or edit/build instead of rereading the same content."
		}
		byPath[context.Path] = context
	}
	if len(order) > 6 {
		order = order[len(order)-6:]
	}
	result := []ProgressFileContext{}
	for _, path := range order {
		result = append(result, byPath[path])
	}
	return result
}

func progressFileContextFromObservation(observation turnObservation) (ProgressFileContext, bool) {
	toolName := strings.TrimSpace(observation.Tool)
	if (toolName != "file_read" && toolName != "file_preview") || observation.Failed() {
		return ProgressFileContext{}, false
	}
	payload := map[string]any{}
	if json.Unmarshal(observation.StructuredOutput(), &payload) != nil {
		return ProgressFileContext{}, false
	}
	path := stringField(payload, "path")
	content := stringField(payload, "content")
	if path == "" {
		return ProgressFileContext{}, false
	}
	if toolName == "file_preview" {
		return ProgressFileContext{
			Path:              path,
			LastObservationID: observation.ObservationID,
			SizeBytes:         intField(payload, "sizeBytes"),
			OriginalSizeBytes: intField(payload, "sizeBytes"),
			Summary:           summarizeFilePreviewContent(path, stringField(payload, "markdownPreview")),
			MarkdownPreview:   truncateText(compactWhitespace(stringField(payload, "markdownPreview")), 1000),
			ConversionStatus:  stringField(payload, "conversionStatus"),
			ConversionMessage: stringField(payload, "conversionMessage"),
		}, true
	}
	startLine := intField(payload, "startLine")
	endLine := intField(payload, "endLine")
	totalLines := intField(payload, "totalLines")
	sizeBytes := intField(payload, "sizeBytes")
	originalSizeBytes := intField(payload, "originalSizeBytes")
	if originalSizeBytes <= 0 {
		originalSizeBytes = sizeBytes
	}
	readRange := formatLineRange(startLine, endLine)
	return ProgressFileContext{
		Path:              path,
		LastObservationID: observation.ObservationID,
		ReadRanges:        appendUniqueStrings([]string{readRange}),
		TotalLines:        totalLines,
		TotalLinesKnown:   booleanField(payload, "totalLinesKnown"),
		SizeBytes:         sizeBytes,
		OriginalSizeBytes: originalSizeBytes,
		ReturnedBytes:     intField(payload, "returnedBytes"),
		IsTruncated:       booleanField(payload, "isTruncated"),
		Summary:           summarizeFileReadContent(path, content),
		Snippet:           truncateText(content, 1200),
	}, true
}

func summarizeFileReadObservation(observation turnObservation) string {
	context, isFileRead := progressFileContextFromObservation(observation)
	if !isFileRead {
		return summarizeSafeJSONFields(observation.ContentText(), []string{"path", "startLine", "endLine", "totalLines", "sizeBytes", "isTruncated"})
	}
	parts := []string{
		"path=" + context.Path,
		"range=" + strings.Join(context.ReadRanges, ","),
	}
	if context.TotalLines > 0 {
		parts = append(parts, fmt.Sprintf("totalLines=%d", context.TotalLines))
	}
	if context.SizeBytes > 0 {
		parts = append(parts, fmt.Sprintf("sizeBytes=%d", context.SizeBytes))
	}
	if context.Summary != "" {
		parts = append(parts, "summary="+context.Summary)
	}
	return strings.Join(parts, "; ")
}

func formatLineRange(startLine int, endLine int) string {
	if startLine <= 0 || endLine <= 0 {
		return "default"
	}
	if startLine == endLine {
		return fmt.Sprintf("%d", startLine)
	}
	return fmt.Sprintf("%d-%d", startLine, endLine)
}

func repeatedReadGuidance(path string, ranges []string) string {
	if len(ranges) < 2 {
		return ""
	}
	return "Already read " + strings.TrimSpace(path) + " ranges " + strings.Join(ranges, ", ") + "; use this context, read a different range, or edit/build instead of rereading the same content."
}

func summarizeFileReadContent(path string, content string) string {
	values := appendUniqueStrings(fileContentExportNames(content))
	values = appendUniqueStrings(values, markdownHeadingNames(content)...)
	if len(values) == 0 {
		values = append(values, truncateText(compactWhitespace(content), 240))
	}
	if len(values) > 12 {
		values = values[:12]
	}
	return strings.TrimSpace(filepathBase(path) + " symbols/headings: " + strings.Join(values, ", "))
}

func summarizeFilePreviewContent(path string, content string) string {
	if strings.TrimSpace(content) == "" {
		return filepathBase(path) + " preview: no markdown preview available"
	}
	return filepathBase(path) + " preview: " + truncateText(compactWhitespace(content), 240)
}

func fileContentExportNames(content string) []string {
	names := []string{}
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		for index, field := range fields {
			if field != "export" || index+2 >= len(fields) {
				continue
			}
			kind := fields[index+1]
			if kind != "const" && kind != "let" && kind != "var" && kind != "function" && kind != "type" && kind != "interface" && kind != "class" {
				continue
			}
			names = appendUniqueStrings(names, cleanSymbolName(fields[index+2]))
		}
	}
	return names
}

func markdownHeadingNames(content string) []string {
	names := []string{}
	for _, line := range strings.Split(content, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmedLine, "#") {
			continue
		}
		names = appendUniqueStrings(names, truncateText(compactWhitespace(strings.TrimLeft(trimmedLine, "# ")), 80))
	}
	return names
}

func cleanSymbolName(value string) string {
	return strings.Trim(strings.TrimSpace(value), "=:;,{(")
}

func filepathBase(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	index := strings.LastIndex(path, "/")
	if index < 0 {
		return path
	}
	return path[index+1:]
}

func intField(document map[string]any, key string) int {
	switch value := document[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}
