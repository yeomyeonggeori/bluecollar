package loop

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type planDocument struct {
	Goal  string     `json:"goal,omitempty"`
	Level TaskLevel  `json:"level,omitempty"`
	Steps []PlanStep `json:"steps"`
}

func planFromObservation(observation turnObservation) (planDocument, bool) {
	if observation.Action != "continue" || observation.Failed() || !toolcontract.ToolNamesMatch(observation.Tool, toolcontract.PlanToolName) {
		return planDocument{}, false
	}
	var document planDocument
	if json.Unmarshal(observation.Output.Data, &document) != nil {
		return planDocument{}, false
	}
	document.Goal, document.Steps = NormalizePlan(document.Goal, document.Steps)
	return document, true
}

func (agentTurnRunner *AgentTurnRunner) applyPlanObservation(ctx context.Context, taskRunID string, state *agentTaskState, observation turnObservation) {
	document, isPlan := planFromObservation(observation)
	if !isPlan {
		return
	}
	if document.Goal != "" {
		state.ExecutionState.Goal = document.Goal
	}
	state.ExecutionState.Steps = document.Steps
	agentTurnRunner.widenPaceForPlannedLevel(taskRunID, state, document.Level)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentPlanUpdated, marshalEventBody(planDocument{Goal: state.ExecutionState.Goal, Level: document.Level, Steps: state.ExecutionState.Steps}))
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentExecutionState, marshalEventBody(normalizeExecutionState(state.ExecutionState)))
	agentTurnRunner.selectToolsForActivePlanStep(ctx, taskRunID, state)
}

func activePlanStepTitle(steps []PlanStep) string {
	for _, step := range steps {
		if step.Status == toolcontract.PlanStepStatusInProgress {
			return step.Title
		}
	}
	if len(steps) > 0 {
		return steps[0].Title
	}
	return ""
}

func (agentTurnRunner *AgentTurnRunner) selectToolsForActivePlanStep(ctx context.Context, taskRunID string, state *agentTaskState) {
	stepTitle := activePlanStepTitle(state.ExecutionState.Steps)
	if stepTitle == "" || stepTitle == state.ActivePlanStepTitle {
		return
	}
	state.ActivePlanStepTitle = stepTitle
	if agentTurnRunner.toolSelector == nil {
		return
	}
	selectedTools, errorValue := agentTurnRunner.toolSelector.SelectToolNames(ctx, agentcontract.ToolSelectionNeed{
		Need:       stepTitle,
		ToolSet:    state.Request.ToolSet,
		CountLimit: toolcontract.MaxLikelyToolCountForOnePlanStep,
	})
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStepToolsSelected, marshalEventBody(map[string]any{
			"step":  stepTitle,
			"error": errorValue.Error(),
		}))
		return
	}
	state.PlanStepToolNames = selectedToolNamesOf(selectedTools)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStepToolsSelected, marshalEventBody(map[string]any{
		"step":       stepTitle,
		"toolNames":  state.PlanStepToolNames,
		"countLimit": toolcontract.MaxLikelyToolCountForOnePlanStep,
	}))
}

func selectedToolNamesOf(selectedTools []agentcontract.SelectedTool) []string {
	toolNames := []string{}
	for _, selectedTool := range selectedTools {
		if toolName := strings.TrimSpace(selectedTool.Name); toolName != "" {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func (agentTurnRunner *AgentTurnRunner) notePlanMissingBeforeStateChange(taskRunID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) {
	if state.DidNudgePlan || len(state.ExecutionState.Steps) > 0 || !taskLevelRequiresPlan(request.TaskLevel) {
		return
	}
	if request.ToolSet == nil || !requestToolSetCanReachTool(request.ToolSet, toolcontract.PlanToolName) {
		return
	}
	toolDefinition, isFound := request.ToolSet.ToolDefinition(actionDocument.ToolName)
	if !isFound || !toolDefinitionIsStateChanging(toolDefinition) {
		return
	}
	state.DidNudgePlan = true
	observation := newContentObservation(nextObservationIDForObservations(state.Observations), "policy", actionDocument.ToolName, "This multi-step task has no recorded plan yet. The current call proceeds; after it completes, record your goal and step plan with plan, then continue.")
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentPlanNudged, marshalEventBody(observation))
}

func toolDefinitionIsStateChanging(toolDefinition toolcontract.ToolDefinition) bool {
	switch toolcontract.ToolDefinitionSideEffectClass(toolDefinition) {
	case "", toolcontract.ToolSideEffectNone, toolcontract.ToolSideEffectRead, toolcontract.ToolSideEffectComputation, toolcontract.ToolSideEffectApproval:
		return false
	default:
		return true
	}
}

func latestPlan(observations []turnObservation) (planDocument, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		if document, isPlan := planFromObservation(observations[index]); isPlan {
			return document, true
		}
	}
	return planDocument{}, false
}

func (agentTurnRunner *AgentTurnRunner) widenPaceForPlannedLevel(taskRunID string, state *agentTaskState, plannedLevel TaskLevel) {
	normalizedLevel := NormalizeTaskLevel(string(plannedLevel))
	if normalizedLevel == "" || taskLevelRank(normalizedLevel) <= taskLevelRank(state.Request.TaskLevel) {
		return
	}
	plannedProfile := TaskLevelProfileForLevel(normalizedLevel)
	agentTurnRunner.options.MaxIterationCount = plannedProfile.MaxIterationCount
	agentTurnRunner.options.MaxToolCallCount = plannedProfile.MaxToolCallCount
	agentTurnRunner.setElapsedBudgetFromProfile(plannedProfile)
	state.Request.TaskLevel = normalizedLevel
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentPlanSized, marshalEventBody(map[string]any{
		"level":             string(normalizedLevel),
		"maxToolCallCount":  agentTurnRunner.options.MaxToolCallCount,
		"maxIterationCount": agentTurnRunner.options.MaxIterationCount,
		"maxElapsedSecond":  agentTurnRunner.options.MaxElapsedSecond,
	}))
}
