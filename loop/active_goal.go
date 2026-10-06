package loop

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func normalizePersistedActiveGoal(activeGoal ActiveGoal) ActiveGoal {
	activeGoal.RequiredNextTools = normalizePersistedToolNames(activeGoal.RequiredNextTools)
	activeGoal.SelectedToolNames = normalizePersistedToolNames(activeGoal.SelectedToolNames)
	activeGoal.OutcomeContract = normalizePersistedOutcomeContract(activeGoal.OutcomeContract)
	return activeGoal
}

func normalizePersistedOutcomeContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = normalizePersistedToolNames(contract.RequiredEvidenceTools)
	contract.RequiredEvidenceAnyOf = normalizePersistedToolNameGroups(contract.RequiredEvidenceAnyOf)
	contract.SelectedEvidenceHints = normalizePersistedToolNames(contract.SelectedEvidenceHints)
	contract.ExpectedResults = normalizePersistedExpectedResults(contract.ExpectedResults)
	contract.RequiredEffects = normalizePersistedOutcomeEffects(contract.RequiredEffects)
	return normalizeOutcomeContract(contract)
}

func normalizePersistedToolNameGroups(groups [][]string) [][]string {
	normalizedGroups := make([][]string, 0, len(groups))
	for _, group := range groups {
		normalizedGroups = append(normalizedGroups, normalizePersistedToolNames(group))
	}
	return normalizedGroups
}

func normalizePersistedToolNames(toolNames []string) []string {
	normalizedToolNames := make([]string, 0, len(toolNames))
	for _, toolName := range toolNames {
		normalizedToolNames = appendUniqueStrings(normalizedToolNames, normalizePersistedToolName(toolName))
	}
	return normalizedToolNames
}

func normalizePersistedToolName(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "ask_choice":
		return toolcontract.AskInputToolName
	case "artifact.deliver", "file.attach":
		return toolcontract.FileDeliverToolName
	case "terminal.session":
		return toolcontract.BashToolName
	default:
		return toolcontract.CanonicalToolName(toolName)
	}
}

func normalizePersistedExpectedResults(results []ExpectedResult) []ExpectedResult {
	normalizedResults := make([]ExpectedResult, 0, len(results))
	for _, result := range results {
		result.AcceptanceHints = normalizePersistedToolNames(result.AcceptanceHints)
		normalizedResults = append(normalizedResults, result)
	}
	return normalizedResults
}

func normalizePersistedOutcomeEffects(effects []OutcomeEffect) []OutcomeEffect {
	normalizedEffects := make([]OutcomeEffect, 0, len(effects))
	for _, effect := range effects {
		effect.SuggestedNextTools = normalizePersistedToolNames(effect.SuggestedNextTools)
		normalizedEffects = append(normalizedEffects, effect)
	}
	return normalizedEffects
}

func normalizeOutcomeContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools)
	contract.RequiredAttachmentSuffixes = appendUniqueStrings(contract.RequiredAttachmentSuffixes)
	contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints)
	contract.RequiredEvidenceAnyOf = normalizeEvidenceAnyOf(contract.RequiredEvidenceAnyOf)
	contract.RequiredEffects = normalizeOutcomeEffects(contract.RequiredEffects)
	contract.ExpectedResults = normalizeExpectedResults(contract.ExpectedResults)
	contract.ArtifactRequirement = normalizeArtifactRequirement(contract.ArtifactRequirement)
	if expectedResultRequiresFileAttachment(contract) {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, toolcontract.FileDeliverToolName)
		contract.ArtifactRequirement = ArtifactRequirementRequired
	}
	contract.Source = strings.TrimSpace(contract.Source)
	return contract
}

func normalizeOutcomeEffects(effects []OutcomeEffect) []OutcomeEffect {
	normalizedEffects := []OutcomeEffect{}
	seenEffects := map[string]bool{}
	for _, effect := range effects {
		normalizedEffect := normalizeOutcomeEffect(effect)
		if normalizedEffect.ObjectType == "" || normalizedEffect.Effect == "" {
			continue
		}
		key := normalizedEffect.ObjectType + "\x00" + normalizedEffect.Effect
		if seenEffects[key] {
			continue
		}
		seenEffects[key] = true
		normalizedEffects = append(normalizedEffects, normalizedEffect)
	}
	return normalizedEffects
}

func normalizeOutcomeEffect(effect OutcomeEffect) OutcomeEffect {
	return OutcomeEffect{
		ObjectType:         strings.TrimSpace(effect.ObjectType),
		Effect:             strings.TrimSpace(effect.Effect),
		Description:        strings.TrimSpace(effect.Description),
		SuggestedNextTools: appendUniqueStrings(effect.SuggestedNextTools),
	}
}

func normalizeArtifactRequirement(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ArtifactRequirementRequired:
		return ArtifactRequirementRequired
	case ArtifactRequirementPreferred:
		return ArtifactRequirementPreferred
	case ArtifactRequirementNone, "":
		return ArtifactRequirementNone
	default:
		return ArtifactRequirementNone
	}
}

func normalizeEvidenceAnyOf(values [][]string) [][]string {
	result := [][]string{}
	seenGroup := map[string]bool{}
	for _, group := range values {
		normalizedGroup := appendUniqueStrings(group)
		if len(normalizedGroup) == 0 {
			continue
		}
		key := strings.Join(normalizedGroup, "\x00")
		if seenGroup[key] {
			continue
		}
		seenGroup[key] = true
		result = append(result, normalizedGroup)
	}
	return result
}

func activeGoalForTurn(request AgentRequest, outcomeContract OutcomeContract, executionPlan ExecutionPlan, hasExecutionPlan bool) ActiveGoal {
	activeGoal := request.ActiveGoal
	activeGoal.SelectedToolNames = appendUniqueStrings(activeGoal.SelectedToolNames, request.PinnedToolNames...)
	activeGoal.SelectedSkillNames = appendUniqueStrings(activeGoal.SelectedSkillNames, request.PinnedSkillNames...)
	activeGoal.OutcomeContract = normalizeOutcomeContract(outcomeContract)
	if strings.TrimSpace(activeGoal.OriginalInstruction) == "" {
		activeGoal.OriginalInstruction = strings.TrimSpace(request.Prompt)
	}
	if hasExecutionPlan {
		activeGoal.OriginalInstruction = firstNonEmptyString(executionPlan.OriginalInstruction, activeGoal.OriginalInstruction)
		activeGoal.CurrentObjective = firstNonEmptyString(executionPlan.Summary, activeGoal.CurrentObjective)
		activeGoal.MissingInformation = append([]string{}, executionPlan.MissingInformation...)
	}
	if activeGoal.Status == "" {
		activeGoal.Status = ActiveGoalStatusActive
	}
	return activeGoal
}

func selectedSkillNameList(skillDecisions []SkillSelectionDecision) []string {
	selectedNames := []string{}
	for _, skillDecision := range skillDecisions {
		if skillDecision.Status == "selected" {
			selectedNames = appendUniqueStrings(selectedNames, skillDecision.Name)
		}
	}
	return selectedNames
}

func activeGoalFromExecutionPlan(taskRunID string, executionPlan ExecutionPlan, status ActiveGoalStatus, toolSet *toolcontract.ToolSet, evidenceHints []string, requiredAttachmentSuffixes []string) ActiveGoal {
	outcomeContract := normalizeOutcomeContract(OutcomeContract{
		RequiredEvidenceTools:      executionPlanEvidenceTools(toolSet, executionPlan, evidenceHints),
		RequiredAttachmentSuffixes: append([]string{}, requiredAttachmentSuffixes...),
		SelectedEvidenceHints:      append([]string{}, evidenceHints...),
		Source:                     "execution_plan",
	})
	return ActiveGoal{
		GoalID:              strings.TrimSpace(taskRunID),
		TaskRunID:           strings.TrimSpace(taskRunID),
		OriginalInstruction: strings.TrimSpace(executionPlan.OriginalInstruction),
		CurrentObjective:    strings.TrimSpace(executionPlan.Summary),
		MissingInformation:  append([]string{}, executionPlan.MissingInformation...),
		OutcomeContract:     outcomeContract,
		Status:              status,
	}
}

func activeGoalFromIntakeOnly(routing turnclassification.Routing, taskRunID string, request AgentRequest, intakeDecision IntakeDecision, status agentcontract.TaskStatus) ActiveGoal {
	goal := ActiveGoal{}
	if canPreserveIntakeGoal(routing, taskRunID, request) {
		goal = request.ActiveGoal
	}
	goal.GoalID = strings.TrimSpace(taskRunID)
	goal.TaskRunID = strings.TrimSpace(taskRunID)
	goal.OriginalInstruction = firstNonEmptyString(goal.OriginalInstruction, request.Prompt)
	goal.CurrentObjective = firstNonEmptyString(intakeDecision.Reason, goal.CurrentObjective)
	goal.Status = activeGoalStatusForTaskStatus(status)
	if !OutcomeContractHasRequirements(goal.OutcomeContract) {
		goal.OutcomeContract.ExpectedResults = turnclassification.NormalizeExpectedResults(intakeDecision.ExpectedResults)
		goal.OutcomeContract.RequiredAttachmentSuffixes = attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)
	}
	goal.SelectedToolNames = appendUniqueStrings(goal.SelectedToolNames, registeredToolNamesOnly(request.ToolSet, request.PinnedToolNames)...)
	goal.SelectedSkillNames = appendUniqueStrings(goal.SelectedSkillNames, request.PinnedSkillNames...)
	return goal
}

func canPreserveIntakeGoal(routing turnclassification.Routing, taskRunID string, request AgentRequest) bool {
	if strings.TrimSpace(request.ActiveGoal.TaskRunID) != strings.TrimSpace(taskRunID) {
		return false
	}
	decision := routing.Decision
	if decision == nil {
		return true
	}
	route := decision.Route
	return route != TurnRouteStartTask && route != TurnRouteReviseTask
}

func activeGoalStatusForTaskStatus(status agentcontract.TaskStatus) ActiveGoalStatus {
	switch status {
	case agentcontract.TaskStatusWaitingUserInput:
		return ActiveGoalStatusWaitingUserInput
	case agentcontract.TaskStatusWaitingApproval:
		return ActiveGoalStatusWaitingApproval
	case agentcontract.TaskStatusCompleted:
		return ActiveGoalStatusCompleted
	case agentcontract.TaskStatusBlocked, agentcontract.TaskStatusFailed, agentcontract.TaskStatusCancelled:
		return ActiveGoalStatusBlocked
	default:
		return ActiveGoalStatusActive
	}
}

func activeGoalEventNameForTaskStatus(status agentcontract.TaskStatus) string {
	switch status {
	case agentcontract.TaskStatusWaitingUserInput:
		return agentcontract.TaskEventAgentGoalWaitingUserInput
	case agentcontract.TaskStatusWaitingApproval:
		return agentcontract.TaskEventAgentGoalWaitingApproval
	case agentcontract.TaskStatusCompleted:
		return agentcontract.TaskEventAgentGoalCompleted
	case agentcontract.TaskStatusBlocked, agentcontract.TaskStatusFailed, agentcontract.TaskStatusCancelled:
		return agentcontract.TaskEventAgentGoalBlocked
	default:
		return agentcontract.TaskEventAgentGoalUpdated
	}
}
