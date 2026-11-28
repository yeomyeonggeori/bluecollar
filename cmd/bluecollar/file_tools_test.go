package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func invokeFileTool(t *testing.T, workspacePath string, toolName string, input string) toolcontract.ToolResult {
	t.Helper()
	toolSet := newWorkspaceToolSet(shell{workingDirectoryPath: workspacePath}, nil)
	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolName,
		Input:    json.RawMessage(input),
	})
	if errorValue != nil {
		t.Fatalf("expected tool invocation to return a result: %v", errorValue)
	}
	return result
}

func TestFileReadSupportsLineRangesAndTruncationMetadata(t *testing.T) {
	workspacePath := t.TempDir()
	lines := []string{"line-1", "line-2", "line-3", "line-4", "line-5"}
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "sample.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}

	result := invokeFileTool(t, workspacePath, toolcontract.FileReadToolName, `{"path":"sample.txt","startLine":2,"lineCount":2}`)
	if result.Failed() {
		t.Fatalf("expected ranged read to succeed, got %+v", result)
	}
	var payload struct {
		Content     string `json:"content"`
		StartLine   int    `json:"startLine"`
		EndLine     int    `json:"endLine"`
		TotalLines  int    `json:"totalLines"`
		IsTruncated bool   `json:"isTruncated"`
	}
	if errorValue := json.Unmarshal(result.Output.Data, &payload); errorValue != nil {
		t.Fatal(errorValue)
	}
	if payload.Content != "line-2\nline-3\n" || payload.StartLine != 2 || payload.EndLine != 3 || payload.TotalLines != 5 || !payload.IsTruncated {
		t.Fatalf("unexpected ranged read payload: %+v", payload)
	}

	pastEnd := invokeFileTool(t, workspacePath, toolcontract.FileReadToolName, `{"path":"sample.txt","startLine":10}`)
	if !pastEnd.Failed() || !strings.Contains(pastEnd.UserSafeFailureSummary(), "only has 5 lines") {
		t.Fatalf("expected out-of-range startLine to fail clearly, got %+v", pastEnd)
	}
}

func TestFileWriteCreatesParentDirectoriesAndHandlesLargeContent(t *testing.T) {
	workspacePath := t.TempDir()
	largeContent := strings.Repeat("0123456789abcdef\n", 16000)
	inputDocument, errorValue := json.Marshal(map[string]string{
		"path":    "nested/deep/file.txt",
		"content": largeContent,
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	result := invokeFileTool(t, workspacePath, toolcontract.WriteToolName, string(inputDocument))
	if result.Failed() {
		t.Fatalf("expected nested large write to succeed, got %+v", result)
	}
	written, readError := os.ReadFile(filepath.Join(workspacePath, "nested/deep/file.txt"))
	if readError != nil {
		t.Fatal(readError)
	}
	if string(written) != largeContent {
		t.Fatalf("written content length %d did not match expected %d", len(written), len(largeContent))
	}
}

func TestFileEditHandlesCRLFReportsLineNumbersAndReturnsDiff(t *testing.T) {
	workspacePath := t.TempDir()
	targetPath := filepath.Join(workspacePath, "windows.txt")
	if errorValue := os.WriteFile(targetPath, []byte("alpha\r\nbeta\r\ngamma\r\nbeta\r\n"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}

	ambiguous := invokeFileTool(t, workspacePath, toolcontract.EditToolName, `{"path":"windows.txt","findText":"beta","replaceText":"delta"}`)
	if !ambiguous.Failed() || !strings.Contains(ambiguous.UserSafeFailureSummary(), "at lines 2, 4") {
		t.Fatalf("expected duplicate match error to list line numbers, got %+v", ambiguous)
	}

	edited := invokeFileTool(t, workspacePath, toolcontract.EditToolName, `{"path":"windows.txt","findText":"alpha\nbeta","replaceText":"alpha\ndelta"}`)
	if edited.Failed() {
		t.Fatalf("expected CRLF-normalized edit to succeed, got %+v", edited)
	}
	if !strings.Contains(edited.Output.Content, "lines 1-2") || !strings.Contains(edited.Output.Content, "- beta") || !strings.Contains(edited.Output.Content, "+ delta") {
		t.Fatalf("expected edit result to report line range and diff preview, got %q", edited.Output.Content)
	}
	written, readError := os.ReadFile(targetPath)
	if readError != nil {
		t.Fatal(readError)
	}
	if string(written) != "alpha\r\ndelta\r\ngamma\r\nbeta\r\n" {
		t.Fatalf("expected CRLF line endings to be preserved, got %q", string(written))
	}
}
