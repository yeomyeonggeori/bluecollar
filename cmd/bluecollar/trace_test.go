package main

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/trace"
)

func TestTheTraceFormatFollowsThePathItIsWrittenTo(t *testing.T) {
	bundle := trace.Build(agentcontract.TaskRun{TaskRunID: "task-1"}, nil, "done")

	asJSON, errorValue := renderTrace("/tmp/run.json", bundle)
	if errorValue != nil {
		t.Fatalf("rendering JSON failed: %v", errorValue)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(asJSON)), "{") {
		t.Fatalf("a path ending in .json gets JSON: %s", asJSON)
	}

	asMarkdown, errorValue := renderTrace("/tmp/run.md", bundle)
	if errorValue != nil {
		t.Fatalf("rendering Markdown failed: %v", errorValue)
	}
	if !strings.HasPrefix(string(asMarkdown), "# Task run task-1") {
		t.Fatalf("every other path gets Markdown: %s", asMarkdown)
	}
}
