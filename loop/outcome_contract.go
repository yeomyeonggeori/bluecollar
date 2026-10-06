package loop

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func shouldBuildExecutionPlanForConfirmation(request AgentRequest, intakeDecision IntakeDecision, requiredEvidenceTools []string) bool {
	if intakeDecision.Classification != IntakeClassificationBoundedTask {
		return false
	}
	if intakeDecision.TaskShape == TaskShapeApprovalGatedTask {
		return true
	}
	if intakeDecision.IsExternalSendRequested {
		return true
	}
	for _, toolName := range requiredEvidenceTools {
		if isSendEvidenceTool(request.ToolSet, toolName) {
			return true
		}
	}
	return false
}

func requestLooksLikeSlidesArtifactWork(request AgentRequest) bool {
	return outcomeContractMentionsAttachmentSuffix(request.ActiveGoal.OutcomeContract, ".pptx") ||
		outcomeContractMentionsAttachmentSuffix(request.ActiveGoal.OutcomeContract, ".ppt")
}

func intakeDecisionRequestsVisualDeliverable(intakeDecision IntakeDecision) bool {
	for _, format := range intakeDecision.RequestedOutputFormats {
		switch strings.ToLower(strings.TrimSpace(format)) {
		case "pptx", "ppt", "html":
			return true
		}
	}
	return false
}

func requestNeedsDerivedSideEffectEvidenceGroup(toolSet *toolcontract.ToolSet, intakeDecision IntakeDecision, contract OutcomeContract) bool {
	switch intakeDecision.TaskShape {
	case TaskShapeMaintenanceTask, TaskShapeScheduledTask, TaskShapeApprovalGatedTask:
	default:
		return false
	}
	return !requiredEvidenceIncludesSideEffect(toolSet, contract.RequiredEvidenceTools)
}

func toolSetForOutcomeReference(toolSet *toolcontract.ToolSet, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) *toolcontract.ToolSet {
	if toolSet == nil {
		return nil
	}
	allowedToolNames := []string{}
	for _, toolName := range toolSet.ListToolNames() {
		if shouldExposeToolForOutcome(toolSet, toolName, request, executionPlan, hasExecutionPlan, outcomeContract) {
			allowedToolNames = append(allowedToolNames, toolName)
		}
	}
	return toolSet.WithAllowedToolNames(allowedToolNames)
}

func shouldExposeToolForOutcome(toolSet *toolcontract.ToolSet, toolName string, request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if stringSliceContains(request.PinnedToolNames, trimmedToolName) {
		return true
	}
	if activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) {
		return true
	}
	if isSendEvidenceTool(toolSet, trimmedToolName) {
		return outcomeAllowsExternalSendTools(toolSet, executionPlan, hasExecutionPlan, outcomeContract)
	}
	return true
}

func outcomeAllowsVisualArtifactReview(request AgentRequest, outcomeContract OutcomeContract) bool {
	artifactRequirement := strings.TrimSpace(outcomeContract.ArtifactRequirement)
	return (artifactRequirement != "" && artifactRequirement != ArtifactRequirementNone) ||
		expectedResultIncludesType(outcomeContract, ExpectedResultTypeFile) ||
		expectedResultIncludesType(outcomeContract, ExpectedResultTypeLink) ||
		requestLooksLikeSlidesArtifactWork(request)
}

func outcomeContractMentionsAttachmentSuffix(contract OutcomeContract, suffix string) bool {
	normalizedSuffix := strings.ToLower(strings.TrimSpace(suffix))
	for _, candidateSuffix := range contract.RequiredAttachmentSuffixes {
		if strings.ToLower(strings.TrimSpace(candidateSuffix)) == normalizedSuffix {
			return true
		}
	}
	for _, result := range contract.ExpectedResults {
		for _, hint := range result.AcceptanceHints {
			if strings.ToLower(strings.TrimSpace(hint)) == normalizedSuffix {
				return true
			}
		}
	}
	return false
}

func selectedSkillNames(skillDecisions []SkillSelectionDecision) map[string]bool {
	selectedSkillName := map[string]bool{}
	for _, skillDecision := range skillDecisions {
		if skillDecision.Status == "selected" {
			selectedSkillName[skillDecision.Name] = true
		}
	}
	return selectedSkillName
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			result[trimmedValue] = true
		}
	}
	return result
}

func selectedEvidenceHintTools(instructionBundle InstructionBundle) []string {
	return appendUniqueStrings(instructionBundle.RequiredEvidenceTools)
}

func confirmationEvidenceHintsForRequest(request AgentRequest, intakeDecision IntakeDecision, evidenceHints []string) []string {
	toolNames := []string{}
	for _, toolName := range evidenceHints {
		if evidenceHintMatchesOutcome(toolName, request, intakeDecision, ExecutionPlan{}, false, nil) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func selectedRequiredAttachmentSuffixes(_ InstructionBundle, _ string) []string {
	return nil
}

func selectedEvidenceToolsForRequestContinuation(request AgentRequest, contract OutcomeContract, selectedEvidenceHints []string) []string {
	requiredToolByName := stringSet(outcomeContractRequiredToolNames(contract))
	toolNames := []string{}
	for _, toolName := range selectedEvidenceHints {
		trimmedToolName := strings.TrimSpace(toolName)
		if requiredToolByName[trimmedToolName] {
			toolNames = appendUniqueStrings(toolNames, trimmedToolName)
			continue
		}
		if isSendEvidenceTool(request.ToolSet, trimmedToolName) && !requestLooksLikeExternalSendContinuation(request, contract) {
			continue
		}
		if isSendEvidenceTool(request.ToolSet, trimmedToolName) {
			toolNames = appendUniqueStrings(toolNames, trimmedToolName)
		}
	}
	return toolNames
}

func outcomeContractForRequest(request AgentRequest, intakeDecision IntakeDecision, instructionBundle InstructionBundle, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) OutcomeContract {
	requiredAttachmentSuffixes = attachmentSuffixesForOutcomeContract(requiredAttachmentSuffixes)
	if OutcomeContractHasRequirements(request.ActiveGoal.OutcomeContract) {
		contract := request.ActiveGoal.OutcomeContract
		selectedEvidenceHints := selectedEvidenceHintTools(instructionBundle)
		contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints, selectedEvidenceHints...)
		contract.SelectedEvidenceHints = filterStaleOutcomeHints(request, contract, contract.SelectedEvidenceHints)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, selectedEvidenceToolsForRequestContinuation(request, contract, selectedEvidenceHints)...)
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, requiredSendEvidenceToolsForContract(request.ToolSet, contract)...)
		contract.RequiredEffects = normalizeOutcomeEffects(contract.RequiredEffects)
		if strings.TrimSpace(contract.ArtifactRequirement) == "" || contract.ArtifactRequirement == ArtifactRequirementNone {
			contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
		}
		return sanitizeOutcomeContractForRequest(request, executionPlan, hasExecutionPlan, contract)
	}
	contract := OutcomeContract{
		SelectedEvidenceHints:      appendUniqueStrings(outcomeContractToolNames(request.ActiveGoal.OutcomeContract), selectedEvidenceHintTools(instructionBundle)...),
		RequiredAttachmentSuffixes: append([]string{}, requiredAttachmentSuffixes...),
	}
	contract.RequiredEvidenceTools = outcomeEvidenceTools(request, intakeDecision, executionPlan, hasExecutionPlan, contract.SelectedEvidenceHints, requiredAttachmentSuffixes)
	contract.SelectedEvidenceHints = appendUniqueStrings(contract.SelectedEvidenceHints, workingSetEvidenceGroup(request.ToolSet, intakeDecision.InitialToolNames)...)
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, plannedSendEvidenceTools(request, intakeDecision, executionPlan, hasExecutionPlan)...)
	contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, requiredSendEvidenceToolsForContract(request.ToolSet, contract)...)
	if requestNeedsDerivedSideEffectEvidenceGroup(request.ToolSet, intakeDecision, contract) {
		evidenceGroup := workingSetEvidenceGroup(request.ToolSet, selectedEvidenceHintTools(instructionBundle))
		if len(evidenceGroup) > 0 {
			contract.RequiredEvidenceAnyOf = append(contract.RequiredEvidenceAnyOf, evidenceGroup)
		}
	}
	contract.RequiredEffects = normalizeOutcomeEffects(contract.RequiredEffects)
	contract.SelectedEvidenceHints = filterStaleOutcomeHints(request, contract, contract.SelectedEvidenceHints)
	if len(requiredAttachmentSuffixes) > 0 {
		contract.RequiredEvidenceTools = appendUniqueStrings(contract.RequiredEvidenceTools, toolcontract.FileDeliverToolName)
	}
	contract.ExpectedResults = expectedResultsForRequest(intakeDecision, requiredAttachmentSuffixes)
	contract.ArtifactRequirement = artifactRequirementForOutcomeContract(intakeDecision, contract)
	contract.Source = outcomeContractSource(hasExecutionPlan, requiredAttachmentSuffixes)
	return sanitizeOutcomeContractForRequest(request, executionPlan, hasExecutionPlan, contract)
}

func filterStaleOutcomeHints(request AgentRequest, contract OutcomeContract, toolNames []string) []string {
	filteredToolNames := []string{}
	for _, toolName := range toolNames {
		trimmedToolName := strings.TrimSpace(toolName)
		if trimmedToolName == "" {
			continue
		}
		if trimmedToolName == "artifact_review" && !outcomeAllowsVisualArtifactReview(request, contract) {
			continue
		}
		filteredToolNames = appendUniqueStrings(filteredToolNames, trimmedToolName)
	}
	return filteredToolNames
}

func attachmentSuffixesForOutcomeContract(requiredAttachmentSuffixes []string) []string {
	return append([]string{}, requiredAttachmentSuffixes...)
}

func sanitizeOutcomeContractForRequest(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool, contract OutcomeContract) OutcomeContract {
	contract = normalizeOutcomeContract(contract)
	if outcomeContractExpectsFileResult(contract) {
		contract = removeIntermediateAttachmentEvidence(request.ToolSet, contract)
	}
	if expectedResultIncludesType(contract, ExpectedResultTypeLink) && !outcomeContractExpectsFileResult(contract) {
		contract = removeImplicitFileContract(contract)
	}
	if outcomeContractRequiresPublicLinkOnly(contract) {
		contract.ArtifactRequirement = ArtifactRequirementNone
	}
	if outcomeContractRequiresPlatformMessageMaintenance(request.ToolSet, contract) {
		contract = removePlatformMessageSendContract(contract)
	}
	if !requestExpectsExternalSend(request, executionPlan, hasExecutionPlan) {
		contract = removeExternalSendContract(request.ToolSet, contract)
	}
	return normalizeOutcomeContract(contract)
}

func removeIntermediateAttachmentEvidence(toolSet *toolcontract.ToolSet, contract OutcomeContract) OutcomeContract {
	requiredEvidenceTools := []string{}
	for _, toolName := range contract.RequiredEvidenceTools {
		if toolProducesIntermediateAttachmentSource(toolSet, toolName) {
			continue
		}
		requiredEvidenceTools = appendUniqueStrings(requiredEvidenceTools, toolName)
	}
	contract.RequiredEvidenceTools = requiredEvidenceTools
	filteredGroups := [][]string{}
	for _, group := range contract.RequiredEvidenceAnyOf {
		filteredGroup := []string{}
		for _, toolName := range group {
			if !toolProducesIntermediateAttachmentSource(toolSet, toolName) {
				filteredGroup = appendUniqueStrings(filteredGroup, toolName)
			}
		}
		if len(filteredGroup) > 0 {
			filteredGroups = append(filteredGroups, filteredGroup)
		}
	}
	contract.RequiredEvidenceAnyOf = filteredGroups
	return contract
}

func toolProducesIntermediateAttachmentSource(toolSet *toolcontract.ToolSet, toolName string) bool {
	toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
	if !isFound {
		return false
	}
	return toolDefinition.SideEffectClass == toolcontract.ToolSideEffectWorkspaceWrite &&
		toolResultContractDeclaresFile(toolDefinition.ResultContract) &&
		!toolResultContractAttachesFile(toolDefinition.ResultContract)
}

func toolResultContractDeclaresFile(resultContract *toolcontract.ToolResultContract) bool {
	if resultContract == nil {
		return false
	}
	for _, effect := range resultContract.Effects {
		if effect.ObjectType == "file" {
			return true
		}
	}
	return false
}

func toolResultContractAttachesFile(resultContract *toolcontract.ToolResultContract) bool {
	if resultContract == nil {
		return false
	}
	for _, effect := range resultContract.Effects {
		if effect.ObjectType == "file" && effect.Effect == "attached" {
			return true
		}
	}
	return false
}

func outcomeContractExpectsFileResult(contract OutcomeContract) bool {
	return len(contract.RequiredAttachmentSuffixes) > 0 ||
		evidenceToolsContainArtifactDelivery(contract.RequiredEvidenceTools) ||
		evidenceAnyOfContainsArtifactDelivery(contract.RequiredEvidenceAnyOf) ||
		expectedResultIncludesType(contract, ExpectedResultTypeFile)
}

func removeImplicitFileContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredAttachmentSuffixes = nil
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, toolcontract.FileDeliverToolName)
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, toolcontract.FileDeliverToolName)
	contract.ExpectedResults = removeExpectedResultsByType(contract.ExpectedResults, ExpectedResultTypeFile)
	return contract
}

func dischargeResolvedInputContract(request AgentRequest, turnDecision TurnDecision, contract OutcomeContract) OutcomeContract {
	if !resolvesActiveGoalInput(request, turnDecision) {
		return contract
	}
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, toolcontract.AskInputToolName)
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, toolcontract.AskInputToolName)
	contract.SelectedEvidenceHints = removeToolName(contract.SelectedEvidenceHints, toolcontract.AskInputToolName)
	contract.ExpectedResults = dischargeExpectedResultTool(contract.ExpectedResults, toolcontract.AskInputToolName)
	return normalizeOutcomeContract(contract)
}

func resolvesActiveGoalInput(request AgentRequest, turnDecision TurnDecision) bool {
	if request.ActiveGoal.Status != ActiveGoalStatusWaitingUserInput {
		return false
	}
	taskRunID := strings.TrimSpace(request.ActiveGoal.TaskRunID)
	if taskRunID == "" || taskRunID != strings.TrimSpace(request.ExistingTaskRunID) {
		return false
	}
	return turnDecision.Route == TurnRouteContinueTask || turnDecision.Route == TurnRouteReviseTask
}
