package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func TestTerminalFallbackCannotCompleteAnUnrecordedAttachment(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
	services.runner.decisionModel = &scriptedDecisionModel{}
	request := AgentTurnRequest{Prompt: "Attach the screenshot and explain the service.", ToolSet: kernelFileToolSet()}
	done := make(chan struct{})
	close(done)
	services.runner.expectedChanges.Store("sample-run", &pendingExpectedChanges{done: done, isDefined: true, changes: []expectedChange{{Change: "file attached", Asked: "Attach the screenshot"}}})
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: noToolFallbackFinishMessageDocument("The screenshot failed; explanation delivered.")})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	_, isComplete, reason := services.runner.completeTerminalNoToolsFinish(context.Background(), "sample-run", "sample-step", request, &agentTaskState{}, action)
	if isComplete || !strings.Contains(reason, "file attached") {
		t.Fatalf("an unrecorded attachment completed the task: complete=%v reason=%q", isComplete, reason)
	}
}
