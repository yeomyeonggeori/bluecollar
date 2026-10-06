package loop

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func outcomeAllowsExternalSendTools(toolSet *toolcontract.ToolSet, executionPlan ExecutionPlan, hasExecutionPlan bool, outcomeContract OutcomeContract) bool {
	return contractRequiresSendTool(toolSet, outcomeContract) ||
		(hasExecutionPlan && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend))
}

func requestExpectsExternalSend(request AgentRequest, executionPlan ExecutionPlan, hasExecutionPlan bool) bool {
	_ = request
	if !hasExecutionPlan {
		return true
	}
	return executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend
}

func outcomeContractRequiresPlatformMessageMaintenance(toolSet *toolcontract.ToolSet, contract OutcomeContract) bool {
	for _, toolName := range outcomeContractToolNames(contract) {
		if toolIsInNamespace(toolSet, toolName, "message") && !isSendEvidenceTool(toolSet, toolName) {
			return true
		}
	}
	return false
}

func removePlatformMessageSendContract(contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, "message_send")
	contract.SelectedEvidenceHints = removeToolName(contract.SelectedEvidenceHints, "message_send")
	contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, "message_send")
	return contract
}

func removeExternalSendContract(toolSet *toolcontract.ToolSet, contract OutcomeContract) OutcomeContract {
	for _, toolName := range outcomeContractToolNames(contract) {
		if !isSendEvidenceTool(toolSet, toolName) {
			continue
		}
		contract.RequiredEvidenceTools = removeToolName(contract.RequiredEvidenceTools, toolName)
		contract.RequiredEvidenceAnyOf = removeToolNameGroups(contract.RequiredEvidenceAnyOf, toolName)
	}
	return contract
}

func outcomeEvidenceTools(request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, evidenceHints []string, requiredAttachmentSuffixes []string) []string {
	toolNames := []string{}
	for _, toolName := range evidenceHints {
		if evidenceHintMatchesOutcome(toolName, request, intakeDecision, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func plannedSendEvidenceTools(request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool) []string {
	if !hasExecutionPlan {
		return nil
	}
	if !executionPlan.ExternalSend && !executionPlan.ThirdPartyExternalSend {
		return nil
	}
	return sendEvidenceToolsFromValues(request.ToolSet, workingSetEvidenceGroup(request.ToolSet, intakeDecision.InitialToolNames))
}

func requiredSendEvidenceToolsForContract(toolSet *toolcontract.ToolSet, contract OutcomeContract) []string {
	if contractRequiresSendTool(toolSet, contract) {
		return sendEvidenceToolsFromValues(toolSet, outcomeContractRequiredToolNames(contract))
	}
	return nil
}

func sendEvidenceToolsFromValues(toolSet *toolcontract.ToolSet, values []string) []string {
	toolNames := []string{}
	for _, value := range values {
		if isSendEvidenceTool(toolSet, value) {
			toolNames = appendUniqueStrings(toolNames, value)
		}
	}
	return toolNames
}

func availableSendEvidenceToolNames(toolSet *toolcontract.ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	toolNames := []string{}
	for _, toolName := range toolSet.ListToolNames() {
		if isSendEvidenceTool(toolSet, toolName) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}

func singleAvailableSendEvidenceTool(toolSet *toolcontract.ToolSet) []string {
	toolNames := availableSendEvidenceToolNames(toolSet)
	if len(toolNames) != 1 {
		return nil
	}
	return toolNames
}

func evidenceHintMatchesOutcome(toolName string, request AgentRequest, intakeDecision IntakeDecision, executionPlan ExecutionPlan, hasExecutionPlan bool, requiredAttachmentSuffixes []string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return false
	}
	if isSendEvidenceTool(request.ToolSet, trimmedToolName) {
		return activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) ||
			(hasExecutionPlan && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend))
	}
	if toolIsInNamespace(request.ToolSet, trimmedToolName, "message") {
		return intakeDecision.TaskShape == TaskShapeMaintenanceTask ||
			activeGoalMentionsTool(request.ActiveGoal, trimmedToolName) ||
			contractRequiresToolNamespace(request.ToolSet, request.ActiveGoal.OutcomeContract, "message")
	}
	if activeGoalRequiresTool(request.ActiveGoal, trimmedToolName) {
		return true
	}
	if toolexposure.IsArtifactDeliveryTool(trimmedToolName) {
		return len(requiredAttachmentSuffixes) > 0
	}
	if toolIsInNamespace(request.ToolSet, trimmedToolName, "schedule") {
		return intakeDecision.TaskShape == TaskShapeScheduledTask
	}
	return false
}

func isSendEvidenceTool(toolSet *toolcontract.ToolSet, toolName string) bool {
	toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
	return isFound && toolDefinition.SideEffectClass == toolcontract.ToolSideEffectExternalSend
}

func contractRequiresSendTool(toolSet *toolcontract.ToolSet, contract OutcomeContract) bool {
	for _, toolName := range outcomeContractRequiredToolNames(contract) {
		if isSendEvidenceTool(toolSet, toolName) {
			return true
		}
	}
	return false
}

func requestLooksLikeExternalSendContinuation(request AgentRequest, contract OutcomeContract) bool {
	return contractRequiresSendTool(request.ToolSet, contract)
}
