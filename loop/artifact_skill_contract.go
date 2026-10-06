package loop

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const artifactContractKindFile = "file"
const artifactContractKindLink = "link"
const artifactContractKindSlides = "slides"

type artifactContractRequirement struct {
	Kind   string
	Format string
}

func artifactContractRequirementsForRequest(request AgentRequest) []artifactContractRequirement {
	requirements := []artifactContractRequirement{}
	if expectedResultIncludesType(request.ActiveGoal.OutcomeContract, ExpectedResultTypeLink) {
		requirements = appendUniqueArtifactContractRequirements(requirements, artifactContractRequirement{Kind: artifactContractKindLink})
	}
	for _, format := range artifactFormatsForRequest(request) {
		requirements = appendUniqueArtifactContractRequirements(requirements, artifactContractRequirement{Kind: artifactContractKindFile, Format: format})
	}
	if requestNeedsSlidesArtifactContract(request) {
		requirements = appendUniqueArtifactContractRequirements(requirements, artifactContractRequirement{Kind: artifactContractKindSlides})
	}
	return requirements
}

func artifactFormatsForRequest(request AgentRequest) []string {
	formats := []string{}
	formats = appendUniqueStrings(formats, artifactFormatsForAttachmentSuffixes(request.ActiveGoal.OutcomeContract.RequiredAttachmentSuffixes)...)
	for _, result := range request.ActiveGoal.OutcomeContract.ExpectedResults {
		formats = appendUniqueStrings(formats, artifactFormatsForAttachmentSuffixes(result.AcceptanceHints)...)
	}
	return normalizeRequestedOutputFormats(formats)
}

func artifactFormatsForAttachmentSuffixes(suffixes []string) []string {
	formats := []string{}
	for _, suffix := range suffixes {
		format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".")
		formats = appendUniqueStrings(formats, format)
	}
	return normalizeRequestedOutputFormats(formats)
}

func requestNeedsSlidesArtifactContract(request AgentRequest) bool {
	return outcomeContractMentionsAttachmentSuffix(request.ActiveGoal.OutcomeContract, ".pptx") ||
		outcomeContractMentionsAttachmentSuffix(request.ActiveGoal.OutcomeContract, ".ppt")
}

func skillSupportsFileDelivery(skillInstruction SkillInstruction) bool {
	return skillHasToolName(skillInstruction, toolcontract.FileDeliverToolName)
}

func skillHasToolName(skillInstruction SkillInstruction, toolName string) bool {
	for _, candidate := range SkillToolNames(skillInstruction) {
		if strings.TrimSpace(candidate) == toolName {
			return true
		}
	}
	return false
}

func appendUniqueArtifactContractRequirements(requirements []artifactContractRequirement, requirement artifactContractRequirement) []artifactContractRequirement {
	if strings.TrimSpace(requirement.Kind) == "" {
		return requirements
	}
	requirement.Kind = strings.TrimSpace(requirement.Kind)
	requirement.Format = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(requirement.Format)), ".")
	for _, existingRequirement := range requirements {
		if existingRequirement.Kind == requirement.Kind && existingRequirement.Format == requirement.Format {
			return requirements
		}
	}
	return append(requirements, requirement)
}
