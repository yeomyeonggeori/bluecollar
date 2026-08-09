package loop

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type planUpdateDocument struct {
	Goal  string     `json:"goal,omitempty"`
	Level TaskLevel  `json:"level,omitempty"`
	Steps []PlanStep `json:"steps"`
}

func planUpdateFromObservation(observation turnObservation) (planUpdateDocument, bool) {
	if observation.Action != "continue" || observation.Failed() || !toolcontract.ToolNamesMatch(observation.Tool, toolcontract.PlanUpdateToolName) {
		return planUpdateDocument{}, false
	}
	var document planUpdateDocument
	if json.Unmarshal(observation.Output.Data, &document) != nil {
		return planUpdateDocument{}, false
	}
	document.Goal, document.Steps = NormalizePlan(document.Goal, document.Steps)
	return document, true
}

func (agentTurnRunner *AgentTurnRunner) applyPlanUpdateObservation(taskRunID string, state *agentTaskState, observation turnObservation) {
	document, isPlanUpdate := planUpdateFromObservation(observation)
	if !isPlanUpdate {
		return
	}
	if document.Goal != "" {
		state.ExecutionState.Goal = document.Goal
	}
	state.ExecutionState.Steps = document.Steps
	agentTurnRunner.widenBudgetForPlannedLevel(taskRunID, state, document.Level)
	agentTurnRunner.appendEvent(taskRunID, "agent.plan.updated", marshalEventBody(planUpdateDocument{Goal: state.ExecutionState.Goal, Level: document.Level, Steps: state.ExecutionState.Steps}))
	agentTurnRunner.appendEvent(taskRunID, "agent.execution_state", marshalEventBody(normalizeExecutionState(state.ExecutionState)))
}

func (agentTurnRunner *AgentTurnRunner) notePlanMissingBeforeStateChange(taskRunID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) {
	if state.DidNudgePlan || len(state.ExecutionState.Steps) > 0 || !taskLevelRequiresPlan(request.TaskLevel) {
		return
	}
	if request.ToolSet == nil || !requestToolSetCanReachTool(request.ToolSet, toolcontract.PlanUpdateToolName) {
		return
	}
	toolDefinition, isFound := request.ToolSet.ToolDefinition(actionDocument.ToolName)
	if !isFound || !toolDefinitionIsStateChanging(toolDefinition) {
		return
	}
	state.DidNudgePlan = true
	observation := newContentObservation(nextObservationIDForObservations(state.Observations), "policy", actionDocument.ToolName, "This multi-step task has no recorded plan yet. The current call proceeds; after it completes, record your goal and step plan with plan_update, then continue.")
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, "agent.plan.nudged", marshalEventBody(observation))
}

func toolDefinitionIsStateChanging(toolDefinition toolcontract.ToolDefinition) bool {
	switch toolcontract.ToolDefinitionSideEffectClass(toolDefinition) {
	case "", toolcontract.ToolSideEffectNone, toolcontract.ToolSideEffectRead, toolcontract.ToolSideEffectComputation, toolcontract.ToolSideEffectApproval:
		return false
	default:
		return true
	}
}

func latestPlanUpdate(observations []turnObservation) (planUpdateDocument, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		if document, isPlanUpdate := planUpdateFromObservation(observations[index]); isPlanUpdate {
			return document, true
		}
	}
	return planUpdateDocument{}, false
}

func (agentTurnRunner *AgentTurnRunner) widenBudgetForPlannedLevel(taskRunID string, state *agentTaskState, plannedLevel TaskLevel) {
	normalizedLevel := NormalizeTaskLevel(string(plannedLevel))
	if normalizedLevel == "" || taskLevelRank(normalizedLevel) <= taskLevelRank(state.Request.TaskLevel) {
		return
	}
	plannedProfile := TaskLevelProfileForLevel(normalizedLevel)
	agentTurnRunner.options.MaxIterationCount = plannedProfile.MaxIterationCount
	agentTurnRunner.options.MaxToolCallCount = plannedProfile.MaxToolCallCount
	agentTurnRunner.options.MaxElapsedSecond = int(elapsedBudgetForProfile(plannedProfile, agentTurnRunner.iterationCostObserver.CostOfModelInUse()).Seconds())
	state.Request.TaskLevel = normalizedLevel
	agentTurnRunner.appendEvent(taskRunID, "agent.plan.sized", marshalEventBody(map[string]any{
		"level":             string(normalizedLevel),
		"maxToolCallCount":  agentTurnRunner.options.MaxToolCallCount,
		"maxIterationCount": agentTurnRunner.options.MaxIterationCount,
		"maxElapsedSecond":  agentTurnRunner.options.MaxElapsedSecond,
	}))
}
