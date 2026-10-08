package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) requestForStep(_ context.Context, request AgentTurnRequest, state *agentTaskState) AgentTurnRequest {
	plannedRequest := requestWithStepWorkingSetTools(request, *state)
	elapsed := agentTurnRunner.turnElapsed(request.EffortStartedAt)
	pressureStage := limitPressureStageFor(state.IterationCount, state.ToolCallCount, elapsed, agentTurnRunner.reachableLimits(*state))
	stepKey := stepToolExposureKey(plannedRequest, *state)
	if state.StepExposure.Key != stepKey {
		state.StepExposure = stepToolExposureFor(plannedRequest, *state, stepKey)
	}
	iterationRequest := plannedRequest
	iterationRequest.ToolSet = state.StepExposure.ToolSet
	iterationRequest.ToolExposure = state.StepExposure.Exposure
	if len(state.PendingBatchedActions) > 0 {
		iterationRequest.ToolSet, iterationRequest.ToolExposure = pendingBatchedToolExposure(plannedRequest.ToolSet, *state)
	}
	if pressureStage == limitPressureStageNarrowPalette && len(state.PendingBatchedActions) == 0 {
		iterationRequest.ToolSet = iterationRequest.ToolSet.WithAllowedToolNames(wrapUpDeliveryToolNames(plannedRequest))
	}
	iterationRequest.StepBudgetContext = agentTurnRunner.stepBudgetContext(*state)
	iterationRequest.RestrictActionToTerminalOnly = state.ShouldRestrictNextActionToTerminal
	return iterationRequest
}

func pendingBatchedToolExposure(toolSet *toolcontract.ToolSet, state agentTaskState) (*toolcontract.ToolSet, ToolExposureEvent) {
	exposedToolNames := []string{}
	for _, toolName := range state.PendingBatchedToolNames {
		if toolSet.CanExpose(toolName) {
			exposedToolNames = appendUniqueStrings(exposedToolNames, toolName)
		}
	}
	exposure := state.PendingBatchedToolExposure
	exposure.ExposedToolIDs = append([]string{}, exposedToolNames...)
	if len(exposedToolNames) == 0 {
		return toolSet.WithRegisteredToolNamesLimitedTo(nil), exposure
	}
	return toolSet.WithAllowedToolNames(exposedToolNames), exposure
}

func stepToolExposureKey(plannedRequest AgentTurnRequest, state agentTaskState) string {
	instructionBundle := instructionBundleFromTurnRequest(plannedRequest)
	return strings.Join([]string{
		state.ActivePlanStepTitle,
		strings.Join(sortedStrings(plannedRequest.PinnedToolNames), ","),
		strings.Join(sortedStrings(activeRecoveryToolNames(state.Observations)), ","),
		firstPendingRequiredToolName(instructionBundle.RequiredNextTools, state.Observations),
	}, "\x00")
}

func stepToolExposureFor(plannedRequest AgentTurnRequest, state agentTaskState, stepKey string) stepToolExposure {
	filteredToolSet, exposureEvent := toolSetForAgentTurnWithExposure(
		plannedRequest.ToolSet,
		instructionBundleFromTurnRequest(plannedRequest),
		agentRequestFromTurnRequest(plannedRequest),
		ExecutionPlan{},
		false,
		plannedRequest.OutcomeContract,
		ToolExposureEvent{},
		state.Observations,
	)
	return stepToolExposure{Key: stepKey, ToolSet: filteredToolSet, Exposure: exposureEvent}
}

func sortedStrings(values []string) []string {
	sorted := append([]string{}, values...)
	sort.Strings(sorted)
	return sorted
}

func wrapUpDeliveryToolNames(request AgentTurnRequest) []string {
	toolNames := []string{}
	if expectedResultRequiresFileAttachment(request.OutcomeContract) {
		toolNames = appendUniqueStrings(toolNames, availableFileDeliveryToolNames(request)...)
	}
	if externalSendCompletionEvidenceRequired(request) {
		toolNames = appendUniqueStrings(toolNames, requiredSendToolNamesForRequest(request)...)
	}
	return toolNames
}

func (agentTurnRunner *AgentTurnRunner) stepBudgetContext(state agentTaskState) string {
	limits := agentTurnRunner.reachableLimits(state)
	maxToolCallCount := limits.MaxToolCallCount
	remainingToolCallCount := maxToolCallCount - state.ToolCallCount
	if remainingToolCallCount < 0 {
		remainingToolCallCount = 0
	}
	maxIterationCount := limits.MaxIterationCount
	remainingIterationCount := maxIterationCount - state.IterationCount
	if remainingIterationCount < 0 {
		remainingIterationCount = 0
	}
	return strings.Join([]string{
		"Step budget:",
		fmt.Sprintf("Tool calls: %d/%d used, %d remaining.", state.ToolCallCount, maxToolCallCount, remainingToolCallCount),
		fmt.Sprintf("Steps: %d/%d used, %d remaining.", state.IterationCount, maxIterationCount, remainingIterationCount),
		"Use the shortest path to the expected result. Avoid extra inspection when the next edit, build, publish, file delivery, or final action is already clear.",
		"Keep at least two tool calls for delivery when the requested link or file has not been delivered yet.",
	}, "\n")
}

func requestWithStepWorkingSetTools(request AgentTurnRequest, state agentTaskState) AgentTurnRequest {
	observations := state.Observations
	request.PinnedToolNames = planStepPinnedToolNames(request, state)
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, pendingFileDeliveryToolNames(request, observations)...)
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, observedSuggestedNextToolNames(observations)...)
	foundToolNames := foundToolNamesFromObservations(observations)
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, foundToolNames...)
	request.SkillDecisions = withOwningSkillDecisions(request.SkillDecisions, request.AvailableSkills, foundToolNames)
	return request
}

func planStepPinnedToolNames(request AgentTurnRequest, state agentTaskState) []string {
	if len(state.PlanStepToolNames) == 0 {
		return appendUniqueStrings(request.PinnedToolNames)
	}
	return appendUniqueStrings(toolNamesExcept(request.PinnedToolNames, request.LikelyToolNames), state.PlanStepToolNames...)
}

func toolNamesExcept(toolNames []string, excludedToolNames []string) []string {
	remaining := []string{}
	for _, toolName := range toolNames {
		if stringSliceContains(excludedToolNames, toolName) {
			continue
		}
		remaining = append(remaining, toolName)
	}
	return remaining
}

func withOwningSkillDecisions(decisions []SkillSelectionDecision, availableSkills []SkillInstruction, requestedToolNames []string) []SkillSelectionDecision {
	if len(requestedToolNames) == 0 {
		return decisions
	}
	selectedSkillNames := map[string]bool{}
	for _, decision := range decisions {
		if decision.Status == "selected" {
			selectedSkillNames[decision.Name] = true
		}
	}
	amendedDecisions := append([]SkillSelectionDecision{}, decisions...)
	for _, skillInstruction := range availableSkills {
		if selectedSkillNames[skillInstruction.Name] {
			continue
		}
		for _, toolName := range SkillToolNames(skillInstruction) {
			if !stringSliceContains(requestedToolNames, toolName) {
				continue
			}
			amendedDecisions = append(amendedDecisions, SkillSelectionDecision{
				Name:   skillInstruction.Name,
				Status: "selected",
				Reason: "owns requested tool " + toolName,
			})
			selectedSkillNames[skillInstruction.Name] = true
			break
		}
	}
	return amendedDecisions
}

func foundToolNamesFromObservations(observations []turnObservation) []string {
	toolNames := []string{}
	for _, observation := range observations {
		if observation.Action != "continue" || observation.Failed() || !toolexposure.ToolNamesMatch(observation.Tool, toolcontract.EquipToolName) {
			continue
		}
		var foundTools agentcontract.EquippedTools
		if json.Unmarshal(observation.Output.Data, &foundTools) != nil {
			continue
		}
		for _, selectedTool := range foundTools.SelectedTools {
			toolNames = appendUniqueStrings(toolNames, selectedTool.Name)
		}
	}
	return toolNames
}

func pendingFileDeliveryToolNames(request AgentTurnRequest, observations []turnObservation) []string {
	if !expectedResultRequiresFileAttachment(request.OutcomeContract) || hasSuccessfulArtifactDeliveryObservation(observations) {
		return nil
	}
	return availableFileDeliveryToolNames(request)
}

func availableFileDeliveryToolNames(request AgentTurnRequest) []string {
	toolNames := []string{toolcontract.BashToolName, toolcontract.FileDeliverToolName}
	if request.ToolSet == nil {
		return toolNames
	}
	return registeredToolNamesOnly(request.ToolSet, toolNames)
}

func hasSuccessfulArtifactDeliveryObservation(observations []turnObservation) bool {
	for _, observation := range observations {
		if !observation.Failed() && toolexposure.IsArtifactDeliveryTool(observation.Tool) {
			return true
		}
	}
	return false
}

func instructionBundleFromTurnRequest(request AgentTurnRequest) InstructionBundle {
	contractToolWorkingSet := request.ContractToolWorkingSet
	return InstructionBundle{
		Prompt:                      request.InstructionPrompt,
		Skills:                      append([]SkillInstruction{}, request.AvailableSkills...),
		Sources:                     append([]InstructionSource{}, request.InstructionSources...),
		SkillDecisions:              append([]SkillSelectionDecision{}, request.SkillDecisions...),
		RequiredNextTools:           append([]string{}, contractToolWorkingSet.RequiredNextTools...),
		RequiredEvidenceTools:       append([]string{}, contractToolWorkingSet.RequiredEvidenceTools...),
		HasContractSkillArbitration: contractToolWorkingSet.IsAuthoritative(),
		RetrievalMode:               request.SkillRetrievalMode,
		IndexStatus:                 request.SkillIndexStatus,
		CandidateCount:              request.SkillCandidateCount,
		SkillQueries:                append([]string{}, request.SkillQueries...),
	}
}

func agentRequestFromTurnRequest(request AgentTurnRequest) AgentRequest {
	return AgentRequest{
		RequesterPersonID:    request.RequesterPersonID,
		RequesterName:        request.RequesterName,
		RequesterCallingName: request.RequesterCallingName,
		RequesterHandle:      request.RequesterHandle,
		RequesterCircles:     append([]string{}, request.RequesterCircles...),
		ExistingTaskRunID:    request.ExistingTaskRunID,
		ProfileName:          request.ProfileName,
		ConversationID:       request.ConversationID,
		ConversationType:     request.ConversationType,
		Prompt:               request.Prompt,
		ResponseLanguage:     request.ResponseLanguage,
		VisibleContext:       request.VisibleContext,
		MemoryFacts:          append([]MemoryFact{}, request.MemoryFacts...),
		ToolSet:              request.ToolSet,
		PinnedToolNames:      append([]string{}, request.PinnedToolNames...),
		LikelyToolNames:      append([]string{}, request.LikelyToolNames...),
		PinnedSkillNames:     append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:    request.WorkspaceRootPath,
		ActivePaths:          append([]string{}, request.ActivePaths...),
		InstructionPrompt:    request.InstructionPrompt,
		ActiveGoal:           request.ActiveGoal,
		TaskShape:            request.TaskShape,
		TurnStartedAt:        request.TurnStartedAt,
		CheckpointSender:     request.CheckpointSender,
		TaskRunChosen:        request.TaskRunChosen,
	}
}

func (agentTurnRunner *AgentTurnRunner) buildTurnMessages(request AgentTurnRequest, observations []turnObservation, executionState ExecutionState) []model.Message {
	return (PromptAssembler{}).BuildTurnMessages(
		request,
		observations,
		systemInstructionFor(agentTurnRunner.options, request).Text(),
		buildAgentToolDescription(request.ToolSet),
		executionState,
	)
}
