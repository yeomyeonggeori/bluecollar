package loop

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
)

func dischargeExpectedResultTool(results []ExpectedResult, toolName string) []ExpectedResult {
	filteredResults := []ExpectedResult{}
	for _, result := range results {
		if !expectedResultRequiresNamedTool(result, toolName) {
			filteredResults = append(filteredResults, result)
			continue
		}
		result.AcceptanceHints = removeToolName(result.AcceptanceHints, toolName)
		if len(result.AcceptanceHints) == 0 {
			continue
		}
		filteredResults = append(filteredResults, result)
	}
	return filteredResults
}

func expectedResultRequiresNamedTool(result ExpectedResult, toolName string) bool {
	for _, hint := range result.AcceptanceHints {
		if toolexposure.ToolNamesMatch(hint, toolName) {
			return true
		}
	}
	return false
}

func removeToolName(toolNames []string, removedToolName string) []string {
	values := []string{}
	for _, toolName := range toolNames {
		if !toolexposure.ToolNamesMatch(toolName, removedToolName) {
			values = appendUniqueStrings(values, toolName)
		}
	}
	return values
}

func removeToolNameGroups(groups [][]string, removedToolName string) [][]string {
	filteredGroups := [][]string{}
	for _, group := range groups {
		filteredGroup := removeToolName(group, removedToolName)
		if len(filteredGroup) > 0 {
			filteredGroups = append(filteredGroups, filteredGroup)
		}
	}
	return filteredGroups
}

func removeExpectedResultsByType(results []ExpectedResult, removedType string) []ExpectedResult {
	filteredResults := []ExpectedResult{}
	for _, result := range results {
		if result.Type != removedType {
			filteredResults = append(filteredResults, result)
		}
	}
	return filteredResults
}

func expectedResultsForRequest(intakeDecision IntakeDecision, requiredAttachmentSuffixes []string) []ExpectedResult {
	results := append([]ExpectedResult{}, intakeDecision.ExpectedResults...)
	if len(requiredAttachmentSuffixes) > 0 {
		results = append(results, ExpectedResult{
			ID:              "attached-file",
			Type:            ExpectedResultTypeFile,
			Description:     "At least one file in the requested format is attached for the user",
			Required:        true,
			AcceptanceHints: appendUniqueStrings(requiredAttachmentSuffixes),
		})
	}
	if len(results) == 0 {
		return nil
	}
	results = append(results, ExpectedResult{
		ID:          finalMessageExpectedResultID,
		Type:        ExpectedResultTypeMessage,
		Description: "A final reply explaining the outcome of this task to the user",
		Required:    true,
	})
	return normalizeExpectedResults(results)
}

const finalMessageExpectedResultID = "final-message"

func appendExpectedResults(results []ExpectedResult, additionalResults ...ExpectedResult) []ExpectedResult {
	nextResults := append([]ExpectedResult{}, results...)
	nextResults = append(nextResults, additionalResults...)
	return normalizeExpectedResults(nextResults)
}

func artifactRequirementForOutcomeContract(intakeDecision IntakeDecision, contract OutcomeContract) string {
	if len(contract.RequiredAttachmentSuffixes) > 0 || evidenceToolsContainArtifactDelivery(contract.RequiredEvidenceTools) || evidenceAnyOfContainsArtifactDelivery(contract.RequiredEvidenceAnyOf) {
		return ArtifactRequirementRequired
	}
	if outcomeContractRequiresPublicLinkOnly(contract) {
		return ArtifactRequirementNone
	}
	for _, outputFormat := range intakeDecision.RequestedOutputFormats {
		if isArtifactOutputFormat(outputFormat) {
			return ArtifactRequirementPreferred
		}
	}
	return ArtifactRequirementNone
}

func outcomeContractRequiresPublicLinkOnly(contract OutcomeContract) bool {
	hasLinkResult := false
	for _, result := range normalizeExpectedResults(contract.ExpectedResults) {
		if !result.Required {
			continue
		}
		switch result.Type {
		case ExpectedResultTypeFile:
			return false
		case ExpectedResultTypeLink:
			hasLinkResult = true
		}
	}
	return hasLinkResult
}

func isArtifactOutputFormat(value string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "."))) {
	case "pdf", "ppt", "pptx", "doc", "docx", "xls", "xlsx", "csv", "tsv", "html", "zip", "png", "jpg", "jpeg":
		return true
	default:
		return false
	}
}

func attachmentSuffixesForRequestedOutputFormats(formats []string) []string {
	suffixes := []string{}
	for _, format := range normalizeRequestedOutputFormats(formats) {
		switch format {
		case "html":
			suffixes = append(suffixes, ".html")
		case "pptx":
			suffixes = append(suffixes, ".pptx")
		case "pdf":
			suffixes = append(suffixes, ".pdf")
		case "txt":
			suffixes = append(suffixes, ".txt")
		case "docx":
			suffixes = append(suffixes, ".docx")
		case "xlsx":
			suffixes = append(suffixes, ".xlsx")
		case "csv":
			suffixes = append(suffixes, ".csv")
		case "json":
			suffixes = append(suffixes, ".json")
		}
	}
	return suffixes
}

func expectedResultIncludesType(outcomeContract OutcomeContract, resultType string) bool {
	for _, expectedResult := range outcomeContract.ExpectedResults {
		if strings.TrimSpace(expectedResult.Type) == resultType {
			return true
		}
	}
	return false
}
