package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type workspaceExposureModel struct{ actionCount int }

func (*workspaceExposureModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (provider *workspaceExposureModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name != "bluecollar_agent_turn_action" {
		return model.StructuredResponse{Content: `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"low","responseLanguage":"en","reason":"read the requested file"}`}, nil
	}
	provider.actionCount++
	if provider.actionCount == 1 {
		return model.StructuredResponse{Content: `{"action":"continue","toolName":"file_read","toolInput":{"path":"sample.txt"}}`}, nil
	}
	return model.StructuredResponse{Content: `{"action":"reply","final":true,"message":"read the file","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"],"qualityReview":[]}`}, nil
}

func TestConversationExposesItsRegisteredWorkspaceTools(t *testing.T) {
	directory := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(directory, "sample.txt"), []byte("fixture-reading-works"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	provider := &workspaceExposureModel{}
	events := taskstate.NewTaskEventService()
	runs := taskstate.NewTaskRunService(events)
	kernel := loop.NewAgentKernel(runs, taskstate.NewTaskStepService())
	kernel.UseLanguageModelProvider(provider)
	session := &conversationSession{options: runOptions{withoutIntake: true}, kernel: kernel, taskRunService: runs, languageModel: provider, runningShell: shell{workingDirectoryPath: directory}, workspacePath: directory}
	result, errorValue := session.runPrompt(t.Context(), "read sample.txt")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, event := range events.ListTaskEvent(result.TaskRun.TaskRunID) {
		if event.Name != agentcontract.ToolTaskEventName(toolcontract.FileReadToolName, agentcontract.ToolTaskEventResultSuffix) {
			continue
		}
		var observation struct {
			Output  toolcontract.ToolOutput   `json:"output"`
			Failure *toolcontract.ToolFailure `json:"failure"`
		}
		if errorValue := json.Unmarshal([]byte(event.Body), &observation); errorValue != nil {
			t.Fatal(errorValue)
		}
		if observation.Failure == nil && strings.Contains(string(observation.Output.Data), "fixture-reading-works") {
			return
		}
	}
	t.Fatal("the CLI registered its file tool but the loop could not execute it")
}
