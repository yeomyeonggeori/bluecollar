package loop

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	requiredEvidenceToolKindCapabilityOperation = "capability_operation"
	requiredEvidenceToolKindNativeTool          = "native_tool"
)

func workingSetEvidenceGroup(toolSet *toolcontract.ToolSet, candidateToolNames []string) []string {
	evidenceToolNames := []string{}
	for _, toolName := range appendUniqueStrings(candidateToolNames) {
		if !requiredEvidenceToolCanBeSatisfied(toolSet, toolName) {
			continue
		}
		evidenceToolNames = appendUniqueStrings(evidenceToolNames, toolName)
	}
	return evidenceToolNames
}

func requiredEvidenceToolCanBeSatisfied(toolSet *toolcontract.ToolSet, toolName string) bool {
	_, isValid := requiredEvidenceToolKind(toolSet, toolName)
	return isValid
}

func requiredEvidenceToolKind(toolSet *toolcontract.ToolSet, toolName string) (string, bool) {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return "", false
	}
	if toolSet == nil {
		return "", false
	}
	registeredToolName, isRegistered := requiredEvidenceRegisteredToolName(toolSet, trimmedToolName)
	if !isRegistered {
		return "", false
	}
	if toolSet.IsAllowed(registeredToolName) {
		return requiredEvidenceToolKindNativeTool, true
	}
	if requiredEvidenceToolIsCapabilityOperation(toolSet, registeredToolName) {
		return requiredEvidenceToolKindCapabilityOperation, true
	}
	return "", false
}

func requiredEvidenceRegisteredToolName(toolSet *toolcontract.ToolSet, toolName string) (string, bool) {
	trimmedToolName := strings.TrimSpace(toolName)
	return trimmedToolName, toolSet.IsRegistered(trimmedToolName)
}

func requiredEvidenceToolIsCapabilityOperation(toolSet *toolcontract.ToolSet, toolName string) bool {
	return !toolSet.IsBuiltInTool(toolName) && toolSet.CanExpose(toolName)
}

func requiredEvidenceIncludesSideEffect(toolSet *toolcontract.ToolSet, toolNames []string) bool {
	for _, toolName := range toolNames {
		if evidenceToolChangesSomething(toolSet, toolName) {
			return true
		}
	}
	return false
}

func evidenceToolChangesSomething(toolSet *toolcontract.ToolSet, toolName string) bool {
	return toolexposure.IsArtifactDeliveryTool(toolName) || requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName)
}
