package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	defaultFileReadLineLimit = 2000
	defaultFileReadByteLimit = 64 * 1024
	maximumEditDiffLines     = 20
)

type fileReadInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine,omitempty"`
	LineCount int    `json:"lineCount,omitempty"`
}

type fileWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type fileEditInput struct {
	Path        string `json:"path"`
	FindText    string `json:"findText"`
	ReplaceText string `json:"replaceText"`
}

var fileReadInputSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["path"],
	"properties": {
		"path": {"type": "string", "description": "path to read, relative to the current workspace directory (e.g. main.go); do not use virtual sandbox paths like /mnt/data"},
		"startLine": {"type": "integer", "minimum": 1, "description": "optional 1-indexed line number to start reading from"},
		"lineCount": {"type": "integer", "minimum": 1, "description": "optional maximum number of lines to return"}
	}
}`)

var fileWriteInputSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["path", "content"],
	"properties": {
		"path": {"type": "string", "description": "path to write, relative to the current workspace directory (e.g. hello.c or src/main.c); parent directories are created automatically. Do not use virtual sandbox paths like /mnt/data"},
		"content": {"type": "string", "description": "the complete new contents of the file"}
	}
}`)

var fileEditInputSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["path", "findText", "replaceText"],
	"properties": {
		"path": {"type": "string", "description": "path to edit, relative to the current workspace directory; do not use virtual sandbox paths like /mnt/data"},
		"findText": {"type": "string", "description": "the exact text to replace, which must appear exactly once in the file"},
		"replaceText": {"type": "string", "description": "the text to put in its place"}
	}
}`)

var changedFileEffectContract = []toolcontract.ResourceEffectContract{{
	ObjectType:     "file",
	Effect:         "changed",
	ResultField:    "path",
	EffectIdentity: "path",
}}

var fileToolOutputSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["path", "content"],
	"properties": {
		"path": {"type": "string"},
		"content": {"type": "string"}
	}
}`)

var fileReadOutputSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["path", "content", "startLine", "endLine", "totalLines", "totalLinesKnown", "sizeBytes", "isTruncated"],
	"properties": {
		"path": {"type": "string"},
		"content": {"type": "string"},
		"startLine": {"type": "integer"},
		"endLine": {"type": "integer"},
		"totalLines": {"type": "integer"},
		"totalLinesKnown": {"type": "boolean"},
		"sizeBytes": {"type": "integer"},
		"isTruncated": {"type": "boolean"}
	}
}`)

func registerFileTools(toolSet *toolcontract.ToolSet, runningShell shell) {
	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[fileReadInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/file_read",
			ProviderID:      toolcontract.BuiltInToolProviderID,
			Name:            toolcontract.FileReadToolName,
			SideEffectClass: toolcontract.ToolSideEffectRead,
			OutputSchema:    fileReadOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: fileReadOutputSchema},
			Description:     "Read a file or a line range of a file and get back its exact contents.",
			WhenToUse:       "you need what a file actually says, before changing it or answering a question about it. Use startLine and lineCount for large files.",
			WhenNotToUse:    "finding which files exist or which of them contain a string; run bash for that.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     fileReadInputSchema,
		},
		Result: toolcontract.IdentityToolResult,
		Handler: func(ctx context.Context, input fileReadInput) (toolcontract.ToolResult, error) {
			return readFileThroughShell(ctx, runningShell, input), nil
		},
	})

	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[fileWriteInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/write",
			ProviderID:      toolcontract.BuiltInToolProviderID,
			Name:            toolcontract.WriteToolName,
			SideEffectClass: toolcontract.ToolSideEffectStateChange,
			OutputSchema:    fileToolOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: fileToolOutputSchema, Effects: changedFileEffectContract},
			Description:     "Write a file from scratch, creating parent directories and replacing whatever was there.",
			WhenToUse:       "creating a file, or replacing one whose whole content you are producing.",
			WhenNotToUse:    "changing part of a file that already exists; use edit, because retyping the rest from memory changes lines you did not mean to change.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     fileWriteInputSchema,
		},
		Result: toolcontract.IdentityToolResult,
		Handler: func(ctx context.Context, input fileWriteInput) (toolcontract.ToolResult, error) {
			if errorValue := runningShell.writeFile(ctx, input.Path, input.Content); errorValue != nil {
				return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed,
					toolcontract.WriteToolName, errorValue.Error()), nil
			}
			return fileChangeResult(input.Path, "wrote "+input.Path), nil
		},
	})

	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[fileEditInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/edit",
			ProviderID:      toolcontract.BuiltInToolProviderID,
			Name:            toolcontract.EditToolName,
			SideEffectClass: toolcontract.ToolSideEffectStateChange,
			OutputSchema:    fileToolOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: fileToolOutputSchema, Effects: changedFileEffectContract},
			Description:     "Replace one exact passage of a file with another, leaving every other line byte for byte as it was.",
			WhenToUse:       "changing a file that already exists, however small or large the passage.",
			WhenNotToUse:    "creating a file, or when the replacement is the entire content; use write.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     fileEditInputSchema,
		},
		Result: toolcontract.IdentityToolResult,
		Handler: func(ctx context.Context, input fileEditInput) (toolcontract.ToolResult, error) {
			return editFileThroughShell(ctx, runningShell, input), nil
		},
	})
}

func readFileThroughShell(ctx context.Context, runningShell shell, input fileReadInput) toolcontract.ToolResult {
	content, errorValue := runningShell.readFile(ctx, input.Path)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound,
			toolcontract.FileReadToolName, errorValue.Error())
	}
	totalLines := countLines(content)
	if input.StartLine > 1 && totalLines > 0 && input.StartLine > totalLines {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput,
			toolcontract.FileReadToolName, input.Path+" only has "+strconv.Itoa(totalLines)+" lines; startLine "+strconv.Itoa(input.StartLine)+" is past the end of the file")
	}
	document := fileReadRangeDocument(input.Path, content, input.StartLine, input.LineCount)
	return toolcontract.ToolSuccessData(string(document), document)
}

func fileReadRangeDocument(path string, content string, requestedStartLine int, requestedLineCount int) json.RawMessage {
	totalLines := countLines(content)
	if totalLines == 0 {
		return marshalFileReadDocument(path, "", 1, 0, 0, 0, false)
	}
	startLine := max(requestedStartLine, 1)
	lineLimit := defaultFileReadLineLimit
	if requestedLineCount > 0 {
		lineLimit = requestedLineCount
	}
	lines := splitPreservingNewlines(content)
	startIndex := startLine - 1
	endIndex := min(startIndex+lineLimit, len(lines))
	selected, actualEndIndex, byteTruncated := collectLinesWithinByteLimit(lines[startIndex:endIndex], defaultFileReadByteLimit)
	endLine := startLine + actualEndIndex - 1
	isTruncated := byteTruncated || endLine < totalLines
	return marshalFileReadDocument(path, selected, startLine, endLine, totalLines, len(content), isTruncated)
}

func splitPreservingNewlines(content string) []string {
	if content == "" {
		return nil
	}
	parts := strings.SplitAfter(content, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		return parts[:len(parts)-1]
	}
	return parts
}

func collectLinesWithinByteLimit(lines []string, byteLimit int) (string, int, bool) {
	var builder strings.Builder
	for index, line := range lines {
		if index > 0 && builder.Len()+len(line) > byteLimit {
			return builder.String(), index, true
		}
		if index == 0 && len(line) > byteLimit {
			return strings.ToValidUTF8(line[:byteLimit], ""), 1, true
		}
		builder.WriteString(line)
	}
	return builder.String(), len(lines), false
}

func marshalFileReadDocument(path string, content string, startLine int, endLine int, totalLines int, sizeBytes int, isTruncated bool) json.RawMessage {
	document, errorValue := json.Marshal(map[string]any{
		"path":            path,
		"content":         content,
		"startLine":       startLine,
		"endLine":         endLine,
		"totalLines":      totalLines,
		"totalLinesKnown": true,
		"sizeBytes":       sizeBytes,
		"isTruncated":     isTruncated,
	})
	if errorValue != nil {
		return json.RawMessage(`{}`)
	}
	return document
}

func editFileThroughShell(ctx context.Context, runningShell shell, input fileEditInput) toolcontract.ToolResult {
	content, errorValue := runningShell.readFile(ctx, input.Path)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound,
			toolcontract.EditToolName, errorValue.Error())
	}
	workingContent, workingFind, workingReplace, restoreCRLF := normalizeLineEndingsForEdit(content, input.FindText, input.ReplaceText)
	occurrences := strings.Count(workingContent, workingFind)
	if occurrences == 0 {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput,
			toolcontract.EditToolName, editNotFoundMessage(input.Path, workingContent, workingFind))
	}
	if occurrences > 1 {
		lineNumbers := matchingStartLines(workingContent, workingFind)
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput,
			toolcontract.EditToolName, "findText appears "+strconv.Itoa(occurrences)+" times in "+input.Path+" (at lines "+strings.Join(lineNumbers, ", ")+"); include enough surrounding lines to name one passage")
	}
	matchOffset := strings.Index(workingContent, workingFind)
	updatedContent := strings.Replace(workingContent, workingFind, workingReplace, 1)
	if restoreCRLF {
		updatedContent = strings.ReplaceAll(updatedContent, "\n", "\r\n")
	}
	if errorValue := runningShell.writeFile(ctx, input.Path, updatedContent); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed,
			toolcontract.EditToolName, errorValue.Error())
	}
	startLine := 1 + strings.Count(workingContent[:matchOffset], "\n")
	return fileChangeResult(input.Path, formatEditSummary(input.Path, startLine, workingFind, workingReplace))
}

func normalizeLineEndingsForEdit(content string, findText string, replaceText string) (string, string, string, bool) {
	if !strings.Contains(content, "\r\n") || strings.Contains(findText, "\r\n") {
		return content, findText, replaceText, false
	}
	normalizedContent := strings.ReplaceAll(content, "\r\n", "\n")
	normalizedFind := strings.ReplaceAll(findText, "\r\n", "\n")
	normalizedReplace := strings.ReplaceAll(replaceText, "\r\n", "\n")
	return normalizedContent, normalizedFind, normalizedReplace, true
}

func matchingStartLines(content string, target string) []string {
	lines := []string{}
	searchStart := 0
	for {
		relativeIndex := strings.Index(content[searchStart:], target)
		if relativeIndex < 0 {
			return lines
		}
		absoluteOffset := searchStart + relativeIndex
		lineNumber := 1 + strings.Count(content[:absoluteOffset], "\n")
		lines = append(lines, strconv.Itoa(lineNumber))
		searchStart = absoluteOffset + max(len(target), 1)
	}
}

func editNotFoundMessage(path string, content string, findText string) string {
	baseMessage := "findText does not appear in " + path + "; read the file and copy the passage exactly as it is written there"
	firstNonEmptyLine := ""
	for _, line := range strings.Split(findText, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			firstNonEmptyLine = trimmed
			break
		}
	}
	if firstNonEmptyLine == "" {
		return baseMessage
	}
	for index, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == firstNonEmptyLine {
			return baseMessage + " (a similar line appears at line " + strconv.Itoa(index+1) + ", likely differing in indentation or surrounding lines)"
		}
	}
	return baseMessage
}

func formatEditSummary(path string, startLine int, oldText string, newText string) string {
	oldLines := strings.Split(strings.TrimSuffix(oldText, "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(newText, "\n"), "\n")
	endLine := startLine + max(len(newLines)-1, 0)
	var builder strings.Builder
	builder.WriteString("edited ")
	builder.WriteString(path)
	builder.WriteString(" (lines ")
	builder.WriteString(strconv.Itoa(startLine))
	if endLine > startLine {
		builder.WriteString("-")
		builder.WriteString(strconv.Itoa(endLine))
	}
	builder.WriteString(")\n")
	writePrefixedPreview(&builder, "-", oldLines)
	writePrefixedPreview(&builder, "+", newLines)
	return strings.TrimRight(builder.String(), "\n")
}

func writePrefixedPreview(builder *strings.Builder, prefix string, lines []string) {
	limit := min(len(lines), maximumEditDiffLines)
	for index := 0; index < limit; index++ {
		builder.WriteString(prefix)
		builder.WriteString(" ")
		builder.WriteString(lines[index])
		builder.WriteString("\n")
	}
	if len(lines) > limit {
		builder.WriteString(prefix)
		builder.WriteString(" ... (+")
		builder.WriteString(strconv.Itoa(len(lines) - limit))
		builder.WriteString(" more lines)\n")
	}
}

func fileChangeResult(path string, summary string) toolcontract.ToolResult {
	result := toolcontract.ToolSuccessData(summary, mustMarshalFileOutput(path, summary))
	result.Effects = []toolcontract.ResourceEffect{{ObjectType: "file", Effect: "changed", Path: path}}
	return result
}

func countLines(content string) int {
	if content == "" {
		return 0
	}
	lineCount := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lineCount++
	}
	return lineCount
}

func fileReadDocument(path string, content string) json.RawMessage {
	lineCount := countLines(content)
	return marshalFileReadDocument(path, content, 1, lineCount, lineCount, len(content), false)
}

func mustMarshalFileOutput(path string, content string) json.RawMessage {
	document, errorValue := json.Marshal(map[string]string{"path": path, "content": content})
	if errorValue != nil {
		return json.RawMessage(`{}`)
	}
	return document
}

func (runningShell shell) readFileBase64(ctx context.Context, path string) (string, error) {
	capturedOutput := &bytes.Buffer{}
	capturedError := &bytes.Buffer{}
	command := runningShell.command(ctx, "base64 < "+shellQuoted(path))
	command.Stdout = capturedOutput
	command.Stderr = capturedError
	if errorValue := command.Run(); errorValue != nil {
		return "", shellFileError("could not read "+path, capturedError.String())
	}
	return strings.Join(strings.Fields(capturedOutput.String()), ""), nil
}

func (runningShell shell) readFile(ctx context.Context, path string) (string, error) {
	encoded, errorValue := runningShell.readFileBase64(ctx, path)
	if errorValue != nil {
		return "", errorValue
	}
	decoded, errorValue := base64.StdEncoding.DecodeString(encoded)
	if errorValue != nil {
		return "", errorValue
	}
	return string(decoded), nil
}

func (runningShell shell) writeFile(ctx context.Context, path string, content string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	capturedError := &bytes.Buffer{}
	quotedPath := shellQuoted(path)
	command := runningShell.command(ctx, `mkdir -p "$(dirname -- `+quotedPath+`)" && base64 -d > `+quotedPath)
	command.Stdin = strings.NewReader(encoded)
	command.Stderr = capturedError
	if errorValue := command.Run(); errorValue != nil {
		return shellFileError("could not write "+path, capturedError.String())
	}
	return nil
}

func shellFileError(prefix string, stderrText string) error {
	trimmed := strings.TrimSpace(stderrText)
	if trimmed == "" {
		return errors.New(prefix)
	}
	return errors.New(prefix + ": " + trimmed)
}

func shellQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
