package trace

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestBothRenderingsComeFromOneSnapshot(t *testing.T) {
	bundle := Build(
		agentcontract.TaskRun{TaskRunID: "task-1", Status: agentcontract.TaskStatusCompleted, Prompt: "회의록 보내줘"},
		[]agentcontract.TaskEvent{
			{Name: "task.created", Body: `{"prompt":"회의록 보내줘"}`},
			{Name: "tool.message_send.result", Body: `{"output":{"content":"sent to 이샘플"}}`},
			{Name: "task.completed", Body: ""},
		},
		"보냈습니다",
	)

	document, errorValue := bundle.JSON()
	if errorValue != nil {
		t.Fatalf("rendering the trace as JSON failed: %v", errorValue)
	}
	decoded := Bundle{}
	if errorValue := json.Unmarshal(document, &decoded); errorValue != nil {
		t.Fatalf("the JSON rendering has to be readable back: %v", errorValue)
	}
	markdown := bundle.Markdown()

	for _, expected := range []string{"task-1", "회의록 보내줘", "보냈습니다", "sent to 이샘플", "tool.message_send.result", PrivacyNotice} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("the Markdown rendering dropped %q, so the two renderings disagree about what happened", expected)
		}
		if !strings.Contains(string(document), expected) {
			t.Fatalf("the JSON rendering dropped %q", expected)
		}
	}
	if decoded.Metrics.ToolCalls != bundle.Metrics.ToolCalls || decoded.Status != bundle.Status {
		t.Fatal("both renderings are the same snapshot, so neither can carry a number the other does not")
	}
}

func TestATraceKeepsWhatTheRunCarriedAndSaysSo(t *testing.T) {
	bundle := Build(
		agentcontract.TaskRun{TaskRunID: "task-1", Status: agentcontract.TaskStatusFailed, FailureReason: "the endpoint refused"},
		[]agentcontract.TaskEvent{{Name: "tool.shell.requested", Body: `{"input":{"command":"deploy --token hunter2"}}`}},
		"",
	)

	markdown := bundle.Markdown()

	if !strings.Contains(markdown, "hunter2") {
		t.Fatal("a diagnostic that quietly drops the line you are looking for is worse than none; the trace keeps what the run carried")
	}
	if !strings.Contains(markdown, PrivacyNotice) {
		t.Fatal("and says so, in the same breath, because the reader is about to paste it somewhere")
	}
	if !strings.Contains(markdown, "the endpoint refused") {
		t.Fatal("a failed run's reason is the first thing the reader came for")
	}
}
