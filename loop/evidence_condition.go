package loop

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func observationSatisfiesEvidenceCondition(toolSet *toolcontract.ToolSet, observation turnObservation) bool {
	if toolSet == nil {
		return true
	}
	toolDefinition, isFound := toolSet.ToolDefinition(observation.Tool)
	if !isFound || toolDefinition.ResultContract == nil || toolDefinition.ResultContract.EvidenceCondition == nil {
		return true
	}
	return resultSatisfiesEvidenceCondition(observation.Output.Data, *toolDefinition.ResultContract.EvidenceCondition)
}

func resultSatisfiesEvidenceCondition(result json.RawMessage, condition toolcontract.EvidenceCondition) bool {
	var resultDocument map[string]json.RawMessage
	if json.Unmarshal(result, &resultDocument) != nil {
		return false
	}
	actualValue, isFound := resultDocument[strings.TrimSpace(condition.ResultField)]
	if !isFound {
		return false
	}
	var actual any
	var expected any
	if json.Unmarshal(actualValue, &actual) != nil || json.Unmarshal(condition.Equals, &expected) != nil {
		return false
	}
	return reflect.DeepEqual(actual, expected)
}

func isOneShotCompletionEvidenceTool(toolSet *toolcontract.ToolSet, toolName string) bool {
	toolDefinition, isFound := toolDefinitionForName(toolSet, toolName)
	if !isFound || toolDefinition.Completion.Mode != toolcontract.ToolCompletionObservation {
		return false
	}
	return toolcontract.ToolDefinitionRequiresSideEffectEvidence(toolDefinition)
}

func relativeWorkspacePath(workspaceRootPath string, path string) string {
	relativePath, errorValue := filepath.Rel(workspaceRootPath, path)
	if errorValue != nil || strings.HasPrefix(relativePath, "..") {
		return filepath.Base(path)
	}
	return relativePath
}

func evidenceToolIsReadOnly(toolSet *toolcontract.ToolSet, toolName string) bool {
	if toolSet == nil {
		return false
	}
	definition, isFound := toolSet.ToolDefinition(toolName)
	if !isFound {
		return false
	}
	switch toolcontract.ToolDefinitionSideEffectClass(definition) {
	case toolcontract.ToolSideEffectRead, toolcontract.ToolSideEffectComputation:
		return true
	default:
		return false
	}
}

func requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet *toolcontract.ToolSet, toolName string) bool {
	if toolSet == nil {
		return false
	}
	toolDefinition, isFound := toolSet.ToolDefinition(toolName)
	return isFound && toolcontract.ToolDefinitionRequiresSideEffectEvidence(toolDefinition)
}
