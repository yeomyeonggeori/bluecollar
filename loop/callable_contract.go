package loop

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func contractReducedToCallableTools(toolSet *toolcontract.ToolSet, contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = callableToolNames(toolSet, contract.RequiredEvidenceTools)
	anyOfGroups := [][]string{}
	for _, toolNames := range contract.RequiredEvidenceAnyOf {
		if callable := callableToolNames(toolSet, toolNames); len(callable) > 0 {
			anyOfGroups = append(anyOfGroups, callable)
		}
	}
	contract.RequiredEvidenceAnyOf = anyOfGroups
	if isToolCallable(toolSet, toolcontract.FileDeliverToolName) {
		return contract
	}
	if contract.ArtifactRequirement == ArtifactRequirementRequired {
		contract.ArtifactRequirement = ArtifactRequirementPreferred
	}
	contract.RequiredAttachmentSuffixes = nil
	contract.ExpectedResults = expectedResultsWithFilesNoLongerRequired(contract.ExpectedResults)
	return contract
}

func expectedResultsWithFilesNoLongerRequired(expectedResults []ExpectedResult) []ExpectedResult {
	relaxed := make([]ExpectedResult, 0, len(expectedResults))
	for _, expectedResult := range expectedResults {
		if expectedResult.Type == ExpectedResultTypeFile {
			expectedResult.Required = false
		}
		relaxed = append(relaxed, expectedResult)
	}
	return relaxed
}

func callableToolNames(toolSet *toolcontract.ToolSet, toolNames []string) []string {
	callable := []string{}
	for _, toolName := range toolNames {
		if isToolCallable(toolSet, toolName) {
			callable = append(callable, toolName)
		}
	}
	return callable
}

func isToolCallable(toolSet *toolcontract.ToolSet, toolName string) bool {
	if toolSet == nil {
		return true
	}
	return toolSet.IsRegistered(strings.TrimSpace(toolName))
}

func expectedResultRequiresFileAttachment(contract OutcomeContract) bool {
	if strings.TrimSpace(contract.ArtifactRequirement) == ArtifactRequirementRequired {
		return true
	}
	if len(contract.RequiredAttachmentSuffixes) > 0 {
		return true
	}
	for _, result := range normalizeExpectedResults(contract.ExpectedResults) {
		if result.Required && result.Type == ExpectedResultTypeFile {
			return true
		}
	}
	return false
}
