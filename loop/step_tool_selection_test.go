package loop

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recordingToolSelector struct {
	needs         []string
	countLimits   []int
	selectedTools []agentcontract.SelectedTool
}

func (selector *recordingToolSelector) SelectToolNames(_ context.Context, need agentcontract.ToolSelectionNeed) ([]agentcontract.SelectedTool, error) {
	selector.needs = append(selector.needs, need.Need)
	selector.countLimits = append(selector.countLimits, need.CountLimit)
	return selector.selectedTools, nil
}

func TestConsecutiveIterationsOfOneStepSendTheSameInstructionAndToolCatalog(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	request := AgentTurnRequest{
		Prompt:          "move the deal forward",
		TaskLevel:       TaskLevelMedium,
		ToolSet:         testToolSet(append(toolcontract.KernelToolNames(), "deal_update", "deal_list")),
		PinnedToolNames: []string{"deal_update"},
	}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-1")

	firstIteration := services.runner.requestForStep(context.Background(), request, &state)
	firstPrompt := buildAgentActionRequest(services.runner.actionStateForIteration(firstIteration, nil, state, false), true, false)

	state.Observations = append(state.Observations, newContentObservation("obs-001", "continue", "deal_update", "moved the deal"))
	state.IterationCount = 1
	state.ToolCallCount = 1
	secondIteration := services.runner.requestForStep(context.Background(), request, &state)
	secondPrompt := buildAgentActionRequest(services.runner.actionStateForIteration(secondIteration, nil, state, false), true, false)

	if firstPrompt.Messages[0].Content != secondPrompt.Messages[0].Content {
		t.Fatalf("expected a byte-identical system instruction within one step.\nfirst:\n%s\nsecond:\n%s", firstPrompt.Messages[0].Content, secondPrompt.Messages[0].Content)
	}
	firstCatalog := buildAgentToolDescription(modelCallableToolSet(firstIteration.ToolSet, false))
	secondCatalog := buildAgentToolDescription(modelCallableToolSet(secondIteration.ToolSet, false))
	if firstCatalog != secondCatalog {
		t.Fatalf("expected a byte-identical tool catalog within one step.\nfirst:\n%s\nsecond:\n%s", firstCatalog, secondCatalog)
	}
	if firstPrompt.Messages[1].Content != secondPrompt.Messages[1].Content {
		t.Fatalf("expected a byte-identical unchanging context within one step.\nfirst:\n%s\nsecond:\n%s", firstPrompt.Messages[1].Content, secondPrompt.Messages[1].Content)
	}
}

func TestAPlanStepChangeReselectsTheShortlist(t *testing.T) {
	services := newTurnRunnerTestServices(&completionJudgeStubLanguageModel{}, TurnOptions{})
	selector := &recordingToolSelector{selectedTools: []agentcontract.SelectedTool{{Name: "deal_update"}}}
	services.runner.UseToolSelector(selector)
	request := AgentTurnRequest{ToolSet: testToolSet(append(toolcontract.KernelToolNames(), "deal_update", "deal_list"))}
	state := buildInitialAgentTaskState(request, TurnOptions{}, "task-step-2")

	services.runner.applyPlanObservation(context.Background(), "task-step-2", &state, planUpdateSuccessObservation("obs-001",
		`{"steps":[{"title":"read the deal","status":"in_progress"},{"title":"move the deal","status":"pending"}]}`))
	services.runner.applyPlanObservation(context.Background(), "task-step-2", &state, planUpdateSuccessObservation("obs-002",
		`{"steps":[{"title":"read the deal","status":"in_progress"},{"title":"move the deal","status":"pending"}]}`))
	services.runner.applyPlanObservation(context.Background(), "task-step-2", &state, planUpdateSuccessObservation("obs-003",
		`{"steps":[{"title":"read the deal","status":"done"},{"title":"move the deal","status":"in_progress"}]}`))

	if len(selector.needs) != 2 || selector.needs[0] != "read the deal" || selector.needs[1] != "move the deal" {
		t.Fatalf("expected one selection per active step, got %+v", selector.needs)
	}
	for _, countLimit := range selector.countLimits {
		if countLimit != toolcontract.MaxLikelyToolCountForOnePlanStep {
			t.Fatalf("expected the step-sized cap, got %d", countLimit)
		}
	}
	if len(state.PlanStepToolNames) != 1 || state.PlanStepToolNames[0] != "deal_update" {
		t.Fatalf("expected the step shortlist to replace the pinned tools, got %+v", state.PlanStepToolNames)
	}
	stepRequest := services.runner.requestForStep(context.Background(), request, &state)
	if !stepRequest.ToolSet.IsAllowed("deal_update") || stepRequest.ToolSet.IsAllowed("deal_list") {
		t.Fatalf("expected only the step shortlist exposed, got %+v", stepRequest.ToolSet.ListToolNames())
	}
}
