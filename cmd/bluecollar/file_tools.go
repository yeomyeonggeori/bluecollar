package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type fileReadInput struct {
	Path string `json:"path"`
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
	"properties": {"path": {"type": "string", "description": "path to read, relative to the working directory"}}
}`)

var fileWriteInputSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["path", "content"],
	"properties": {
		"path": {"type": "string", "description": "path to write, relative to the working directory"},
		"content": {"type": "string", "description": "the complete new contents of the file"}
	}
}`)

var fileEditInputSchema = json.RawMessage(`{
	"type": "object",
	"additionalProperties": false,
	"required": ["path", "findText", "replaceText"],
	"properties": {
		"path": {"type": "string", "description": "path to edit, relative to the working directory"},
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
			Name:            toolcontract.FileReadToolName,
			SideEffectClass: toolcontract.ToolSideEffectRead,
			OutputSchema:    fileReadOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: fileReadOutputSchema},
			Description:     "Read a file and get back its exact contents.",
			WhenToUse:       "you need what a file actually says, before changing it or answering a question about it.",
			WhenNotToUse:    "finding which files exist or which of them contain a string; run terminal_run for that.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     fileReadInputSchema,
		},
		Result: toolcontract.IdentityToolResult,
		Handler: func(ctx context.Context, input fileReadInput) (toolcontract.ToolResult, error) {
			content, errorValue := runningShell.readFile(ctx, input.Path)
			if errorValue != nil {
				return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound,
					toolcontract.FileReadToolName, errorValue.Error()), nil
			}
			document := fileReadDocument(input.Path, content)
			return toolcontract.ToolSuccessData(string(document), document), nil
		},
	})

	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[fileWriteInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/file_write",
			Name:            toolcontract.FileWriteToolName,
			SideEffectClass: toolcontract.ToolSideEffectStateChange,
			OutputSchema:    fileToolOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: fileToolOutputSchema, Effects: changedFileEffectContract},
			Description:     "Write a file from scratch, replacing whatever was there.",
			WhenToUse:       "creating a file, or replacing one whose whole content you are producing.",
			WhenNotToUse:    "changing part of a file that already exists; use file_edit, because retyping the rest from memory changes lines you did not mean to change.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     fileWriteInputSchema,
		},
		Result: toolcontract.IdentityToolResult,
		Handler: func(ctx context.Context, input fileWriteInput) (toolcontract.ToolResult, error) {
			if errorValue := runningShell.writeFile(ctx, input.Path, input.Content); errorValue != nil {
				return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed,
					toolcontract.FileWriteToolName, errorValue.Error()), nil
			}
			return fileChangeResult(input.Path, "wrote "+input.Path), nil
		},
	})

	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[fileEditInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/file_edit",
			Name:            toolcontract.FileEditToolName,
			SideEffectClass: toolcontract.ToolSideEffectStateChange,
			OutputSchema:    fileToolOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: fileToolOutputSchema, Effects: changedFileEffectContract},
			Description:     "Replace one exact passage of a file with another, leaving every other line byte for byte as it was.",
			WhenToUse:       "changing a file that already exists, however small or large the passage.",
			WhenNotToUse:    "creating a file, or when the replacement is the entire content; use file_write.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     fileEditInputSchema,
		},
		Result: toolcontract.IdentityToolResult,
		Handler: func(ctx context.Context, input fileEditInput) (toolcontract.ToolResult, error) {
			return editFileThroughShell(ctx, runningShell, input), nil
		},
	})
}

func editFileThroughShell(ctx context.Context, runningShell shell, input fileEditInput) toolcontract.ToolResult {
	content, errorValue := runningShell.readFile(ctx, input.Path)
	if errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound,
			toolcontract.FileEditToolName, errorValue.Error())
	}
	occurrences := strings.Count(content, input.FindText)
	if occurrences == 0 {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput,
			toolcontract.FileEditToolName, "findText does not appear in "+input.Path+"; read the file and copy the passage exactly as it is written there")
	}
	if occurrences > 1 {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput,
			toolcontract.FileEditToolName, "findText appears "+strconv.Itoa(occurrences)+" times in "+input.Path+"; include enough surrounding lines to name one passage")
	}
	if errorValue := runningShell.writeFile(ctx, input.Path, strings.Replace(content, input.FindText, input.ReplaceText, 1)); errorValue != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed,
			toolcontract.FileEditToolName, errorValue.Error())
	}
	return fileChangeResult(input.Path, "edited "+input.Path)
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
	document, errorValue := json.Marshal(map[string]any{
		"path":            path,
		"content":         content,
		"startLine":       1,
		"endLine":         lineCount,
		"totalLines":      lineCount,
		"totalLinesKnown": true,
		"sizeBytes":       len(content),
		"isTruncated":     false,
	})
	if errorValue != nil {
		return json.RawMessage(`{}`)
	}
	return document
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
	command := runningShell.command(ctx, "base64 < "+shellQuoted(path))
	command.Stdout = capturedOutput
	if errorValue := command.Run(); errorValue != nil {
		return "", errors.New("could not read " + path)
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
	command := runningShell.command(ctx, "printf %s "+shellQuoted(encoded)+" | base64 -d > "+shellQuoted(path))
	if errorValue := command.Run(); errorValue != nil {
		return errors.New("could not write " + path)
	}
	return nil
}

func shellQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
