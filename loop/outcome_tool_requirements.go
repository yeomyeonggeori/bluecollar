package loop

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func evidenceAnyOfContainsTool(groups [][]string, toolName string) bool {
	for _, group := range groups {
		for _, candidateToolName := range group {
			if toolexposure.ToolNamesMatch(candidateToolName, toolName) {
				return true
			}
		}
	}
	return false
}

func evidenceToolsContainArtifactDelivery(toolNames []string) bool {
	for _, toolName := range toolNames {
		if toolexposure.IsArtifactDeliveryTool(toolName) {
			return true
		}
	}
	return false
}

func evidenceAnyOfContainsArtifactDelivery(groups [][]string) bool {
	for _, group := range groups {
		if evidenceToolsContainArtifactDelivery(group) {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func toolIsInNamespace(toolSet *toolcontract.ToolSet, toolName string, namespace string) bool {
	toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
	return isFound && toolDefinition.Namespace == strings.TrimSpace(namespace)
}

func toolDefinitionForName(toolSet *toolcontract.ToolSet, toolName string) (toolcontract.ToolDefinition, bool) {
	if toolSet == nil {
		return toolcontract.ToolDefinition{}, false
	}
	return toolSet.ToolDefinition(strings.TrimSpace(toolName))
}

func contractRequiresToolNamespace(toolSet *toolcontract.ToolSet, contract OutcomeContract, namespace string) bool {
	for _, toolName := range outcomeContractRequiredToolNames(contract) {
		if toolIsInNamespace(toolSet, toolName, namespace) {
			return true
		}
	}
	return false
}

func activeGoalMentionsTool(activeGoal ActiveGoal, toolName string) bool {
	normalizedToolName := strings.TrimSpace(toolName)
	if normalizedToolName == "" {
		return false
	}
	for _, activeToolName := range outcomeContractToolNames(activeGoal.OutcomeContract) {
		if toolexposure.ToolNamesMatch(activeToolName, normalizedToolName) {
			return true
		}
	}
	return false
}

func activeGoalRequiresTool(activeGoal ActiveGoal, toolName string) bool {
	normalizedToolName := strings.TrimSpace(toolName)
	if normalizedToolName == "" {
		return false
	}
	for _, activeToolName := range outcomeContractRequiredToolNames(activeGoal.OutcomeContract) {
		if toolexposure.ToolNamesMatch(activeToolName, normalizedToolName) {
			return true
		}
	}
	return false
}

func outcomeContractToolNames(contract OutcomeContract) []string {
	toolNames := outcomeContractRequiredToolNames(contract)
	toolNames = append(toolNames, contract.SelectedEvidenceHints...)
	return toolNames
}

func outcomeContractRequiredToolNames(contract OutcomeContract) []string {
	toolNames := append([]string{}, contract.RequiredEvidenceTools...)
	for _, toolNameGroup := range contract.RequiredEvidenceAnyOf {
		toolNames = append(toolNames, toolNameGroup...)
	}
	return toolNames
}

func outcomeContractSource(hasExecutionPlan bool, requiredAttachmentSuffixes []string) string {
	sources := []string{}
	if hasExecutionPlan {
		sources = append(sources, "execution_plan")
	}
	if len(requiredAttachmentSuffixes) > 0 {
		sources = append(sources, "requested_output")
	}
	if len(sources) == 0 {
		return "explicit_request"
	}
	return strings.Join(sources, "+")
}

func executionPlanEvidenceTools(toolSet *toolcontract.ToolSet, executionPlan ExecutionPlan, evidenceHints []string) []string {
	toolNames := []string{}
	for _, toolName := range evidenceHints {
		if isSendEvidenceTool(toolSet, toolName) && (executionPlan.ExternalSend || executionPlan.ThirdPartyExternalSend) {
			toolNames = appendUniqueStrings(toolNames, toolName)
		}
	}
	return toolNames
}
