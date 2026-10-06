package loop

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
)

func selectedSkillInstructionList(instructionBundle InstructionBundle) []SkillInstruction {
	skillInstructionsByName := skillInstructionByName(instructionBundle.Skills)
	skillInstructions := []SkillInstruction{}
	for _, skillDecision := range instructionBundle.SkillDecisions {
		if skillDecision.Status != "selected" {
			continue
		}
		skillInstruction, isFound := skillInstructionsByName[skillDecision.Name]
		if !isFound {
			continue
		}
		skillInstructions = append(skillInstructions, skillInstruction)
	}
	return skillInstructions
}

func (agentKernel *AgentKernel) currentInstructionBundle() InstructionBundle {
	if agentKernel.instructionLoader != nil {
		return agentKernel.instructionLoader()
	}
	return InstructionBundle{
		Prompt:  agentKernel.instructionPrompt,
		Sources: append([]InstructionSource{}, agentKernel.instructionSources...),
	}
}

func selectInstructionBundleForRequest(instructionBundle InstructionBundle, request AgentRequest) InstructionBundle {
	return selectInstructionBundleForRequestWithRetriever(context.Background(), instructionBundle, request, nil)
}

func selectInstructionBundleForRequestWithRetriever(ctx context.Context, instructionBundle InstructionBundle, request AgentRequest, skillRetriever SkillRetriever) InstructionBundle {
	return selectInstructionBundleForRequestWithRetrieverAndRouter(ctx, instructionBundle, request, skillRetriever, SkillSearchQueryRouter{})
}

func selectInstructionBundleForRequestWithRetrieverAndRouter(ctx context.Context, instructionBundle InstructionBundle, request AgentRequest, skillRetriever SkillRetriever, skillSearchQueryRouter SkillSearchQueryRouter) InstructionBundle {
	prompts := []string{strings.TrimSpace(instructionBundle.Prompt)}
	sources := append([]InstructionSource{}, instructionBundle.Sources...)
	skillDecisions := []SkillSelectionDecision{}
	defaultSkillInstructions := DefaultSkillInstructions()
	selectedSkillInstructions := []SkillInstruction{}
	querySet, hasStructuredQueries := skillSearchQueryRouter.Build(ctx, request)
	retrievalResult := retrieveSkillCandidates(ctx, request, instructionBundle.Skills, skillRetriever, querySet, hasStructuredQueries)
	candidateByName := skillCandidateByName(retrievalResult.SelectedCandidates)
	candidateInstructions := visibleCandidateSkillInstructions(candidateSkillInstructions(instructionBundle.Skills, retrievalResult.SelectedCandidates), candidateByName, request.RequesterCircles)
	contractArbitrationResult := skillSearchQueryRouter.ArbitrateContractSkills(ctx, request, candidateInstructions, candidateByName)
	contractArbitration := contractArbitrationResult.Arbitration
	hasContractArbitration := contractArbitrationResult.Status == contractSkillArbitrationSucceeded
	contractSelectedSkillNames := stringSet(contractArbitration.SelectedSkillNames)
	for _, skillInstruction := range candidateInstructions {
		skillCandidate, isFound := candidateByName[skillInstruction.Name]
		if !isFound {
			continue
		}
		skillDecision := skillDecisionForCandidate(skillInstruction, skillCandidate, normalizedAgentProfileName(request.ProfileName))
		if hasContractArbitration {
			skillDecision = skillDecisionForArbitratedCandidate(skillInstruction, skillCandidate, contractSelectedSkillNames, normalizedAgentProfileName(request.ProfileName))
		}
		if skillDecision.Status == "selected" {
			availabilityDecision := skillAvailabilityDecision(skillInstruction, request, normalizedAgentProfileName(request.ProfileName))
			if availabilityDecision.Status == "skipped" && availabilityDecision.Reason != "no_trigger_matched" {
				skillDecision = availabilityDecision
				skillDecision.Score = skillCandidate.Score
			}
		}
		if !hasContractArbitration && skillDecision.Status == "selected" && shouldSkipArtifactSkillForNonArtifactRequest(skillInstruction, skillCandidate, request) {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "outside_artifact_request", nil)
			skillDecision.Score = skillCandidate.Score
		}
		if skillDecision.Status == "selected" && len(selectedSkillInstructions) >= maxSelectedSkillInstructionCount {
			skillDecision = skippedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "selected_skill_limit_reached", nil)
			skillDecision.Score = skillCandidate.Score
		}
		skillDecisions = append(skillDecisions, skillDecision)
		if skillDecision.Status != "selected" {
			continue
		}
		selectedSkillInstructions = append(selectedSkillInstructions, skillInstruction)
		sources = append(sources, skillInstruction.Source)
	}
	if os.Getenv("BLUECOLLAR_DEBUG_SKILL_SELECTION") != "" {
		fmt.Printf("DBG2 mode=%s candidates=%d decisions=%+v\n", retrievalResult.RetrievalMode, len(candidateInstructions), skillDecisions)
	}
	skillDecisions = append(skillDecisions, blockedSkillSelectionDecisions(instructionBundle.Skills, skillDecisions, request, normalizedAgentProfileName(request.ProfileName))...)
	requiredNextTools := validatedContractNextTools(contractArbitration, selectedSkillInstructions, request)
	requiredEvidenceTools := validatedContractEvidenceTools(contractArbitration, selectedSkillInstructions, request)
	prompts = append(prompts, buildCompactSkillIndexPrompt(candidateInstructions))
	prompts = append(prompts, buildSelectedSkillInstructionPrompt(skillInstructionsWhoseToolsAreCallable(request.ToolSet, defaultSkillInstructions)))
	prompts = append(prompts, buildSelectedSkillInstructionPrompt(skillInstructionsWhoseToolsAreCallable(request.ToolSet, selectedSkillInstructions)))
	return InstructionBundle{
		Prompt:                         strings.Join(nonEmptyStrings(prompts), "\n\n"),
		Sources:                        sources,
		Skills:                         appendSkillInstructions(instructionBundle.Skills, defaultSkillInstructions...),
		SkillDecisions:                 skillDecisions,
		RequiredNextTools:              requiredNextTools,
		RequiredEvidenceTools:          requiredEvidenceTools,
		HasContractSkillArbitration:    hasContractArbitration,
		ContractSkillArbitrationFailed: contractArbitrationResult.Status == contractSkillArbitrationFailed,
		RetrievalMode:                  retrievalResult.RetrievalMode,
		IndexStatus:                    retrievalResult.IndexStatus,
		CandidateCount:                 len(candidateInstructions),
		SkillQueries:                   append([]string{}, retrievalResult.QueryDescriptions...),
	}
}

func validatedContractNextTools(arbitration contractSkillArbitration, selectedSkills []SkillInstruction, request AgentRequest) []string {
	return validateArbitratedToolNames(arbitration.RequiredNextTools, selectedSkillNextToolNameSet(selectedSkills, request), request, false)
}

func validatedContractEvidenceTools(arbitration contractSkillArbitration, selectedSkills []SkillInstruction, request AgentRequest) []string {
	selectedToolNames := selectedSkillEvidenceToolNameSet(selectedSkills, request)
	requiresSideEffect := requiredEvidenceIncludesSideEffect(request.ToolSet, request.ActiveGoal.OutcomeContract.RequiredEvidenceTools) ||
		arbitrationHasSelectedSideEffect(arbitration.RequiredNextTools, selectedToolNames, request)
	return validateArbitratedToolNames(arbitration.ExpectedEvidence, selectedToolNames, request, requiresSideEffect)
}

func selectedSkillNextToolNameSet(selectedSkills []SkillInstruction, request AgentRequest) map[string]bool {
	toolNames := selectedSkillToolNameSet(selectedSkills)
	for _, toolName := range request.ToolSet.BuiltInToolNames() {
		if requestHasToolName(request, toolName) {
			toolNames[toolName] = true
		}
	}
	return toolNames
}

func selectedSkillEvidenceToolNameSet(selectedSkills []SkillInstruction, request AgentRequest) map[string]bool {
	toolNames := selectedSkillToolNameSet(selectedSkills)
	for _, toolName := range request.ActiveGoal.OutcomeContract.RequiredEvidenceTools {
		if requiredEvidenceToolCanBeSatisfied(request.ToolSet, toolName) {
			toolNames[toolName] = true
		}
	}
	return toolNames
}

func selectedSkillToolNameSet(selectedSkills []SkillInstruction) map[string]bool {
	selectedToolNames := map[string]bool{}
	for _, skillInstruction := range selectedSkills {
		for _, toolName := range SkillToolNames(skillInstruction) {
			selectedToolNames[toolName] = true
		}
	}
	return selectedToolNames
}

func arbitrationHasSelectedSideEffect(toolNames []string, selectedToolNames map[string]bool, request AgentRequest) bool {
	for _, toolName := range appendUniqueStrings(toolNames) {
		if selectedToolNames[toolName] && requestHasToolName(request, toolName) && (toolexposure.IsArtifactDeliveryTool(toolName) || requiredEvidenceToolNeedsSuccessfulSideEffect(request.ToolSet, toolName)) {
			return true
		}
	}
	return false
}

func validateArbitratedToolNames(toolNames []string, selectedToolNames map[string]bool, request AgentRequest, requiresSideEffect bool) []string {
	validatedToolNames := []string{}
	for _, toolName := range appendUniqueStrings(toolNames) {
		if !selectedToolNames[toolName] || !requestHasToolName(request, toolName) {
			continue
		}
		if requiresSideEffect && !toolexposure.IsArtifactDeliveryTool(toolName) && !requiredEvidenceToolNeedsSuccessfulSideEffect(request.ToolSet, toolName) {
			continue
		}
		validatedToolNames = append(validatedToolNames, toolName)
	}
	return validatedToolNames
}

func instructionBundleWithPinnedSkills(instructionBundle InstructionBundle, request AgentRequest) InstructionBundle {
	pinnedSkillNames := stringSet(request.PinnedSkillNames)
	if len(pinnedSkillNames) == 0 {
		return instructionBundle
	}
	selectedSkillName := selectedSkillNames(instructionBundle.SkillDecisions)
	pinnedSkillInstructions := []SkillInstruction{}
	for _, skillInstruction := range instructionBundle.Skills {
		if !pinnedSkillNames[skillInstruction.Name] || selectedSkillName[skillInstruction.Name] {
			continue
		}
		pinnedSkillInstructions = append(pinnedSkillInstructions, skillInstruction)
		instructionBundle.SkillDecisions = append(instructionBundle.SkillDecisions, selectedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "manual_require"))
		instructionBundle.Sources = append(instructionBundle.Sources, skillInstruction.Source)
	}
	if len(pinnedSkillInstructions) == 0 {
		return instructionBundle
	}
	instructionBundle.Prompt = strings.Join(nonEmptyStrings([]string{
		instructionBundle.Prompt,
		buildSelectedSkillInstructionPrompt(pinnedSkillInstructions),
	}), "\n\n")
	return instructionBundle
}

func instructionBundleWithToolOwningSkills(instructionBundle InstructionBundle, request AgentRequest, suggestedToolNames []string) InstructionBundle {
	if len(suggestedToolNames) == 0 {
		return instructionBundle
	}
	selectedSkillName := selectedSkillNames(instructionBundle.SkillDecisions)
	owningSkillInstructions := []SkillInstruction{}
	for _, skillInstruction := range instructionBundle.Skills {
		if selectedSkillName[skillInstruction.Name] {
			continue
		}
		owningToolName := firstOwnedSuggestedToolName(skillInstruction, suggestedToolNames)
		if owningToolName == "" {
			continue
		}
		owningSkillInstructions = append(owningSkillInstructions, skillInstruction)
		instructionBundle.SkillDecisions = append(instructionBundle.SkillDecisions, selectedSkillDecision(skillInstruction, normalizedAgentProfileName(request.ProfileName), "owns_suggested_tool "+owningToolName))
		instructionBundle.Sources = append(instructionBundle.Sources, skillInstruction.Source)
		selectedSkillName[skillInstruction.Name] = true
	}
	if len(owningSkillInstructions) == 0 {
		return instructionBundle
	}
	instructionBundle.Prompt = strings.Join(nonEmptyStrings([]string{
		instructionBundle.Prompt,
		buildSelectedSkillInstructionPrompt(owningSkillInstructions),
	}), "\n\n")
	return instructionBundle
}

func firstOwnedSuggestedToolName(skillInstruction SkillInstruction, suggestedToolNames []string) string {
	for _, toolName := range SkillToolNames(skillInstruction) {
		if stringSliceContains(suggestedToolNames, toolName) {
			return toolName
		}
	}
	return ""
}

func shouldSkipArtifactSkillForNonArtifactRequest(skillInstruction SkillInstruction, skillCandidate SkillCandidate, request AgentRequest) bool {
	if skillCandidate.Reason == "direct_skill_name" || strings.TrimSpace(skillCandidate.Name) == "" {
		return false
	}
	if strings.TrimSpace(request.ActiveGoal.OutcomeContract.ArtifactRequirement) != ArtifactRequirementNone {
		return false
	}
	return skillSupportsFileDelivery(skillInstruction)
}

func appendSkillInstructions(left []SkillInstruction, right ...SkillInstruction) []SkillInstruction {
	seenSkillNames := map[string]bool{}
	result := []SkillInstruction{}
	for _, skillInstruction := range left {
		if strings.TrimSpace(skillInstruction.Name) == "" || seenSkillNames[skillInstruction.Name] {
			continue
		}
		seenSkillNames[skillInstruction.Name] = true
		result = append(result, skillInstruction)
	}
	for _, skillInstruction := range right {
		if strings.TrimSpace(skillInstruction.Name) == "" || seenSkillNames[skillInstruction.Name] {
			continue
		}
		seenSkillNames[skillInstruction.Name] = true
		result = append(result, skillInstruction)
	}
	return result
}

func normalizedAgentProfileName(profileName string) string {
	trimmedProfileName := strings.TrimSpace(profileName)
	if trimmedProfileName == "" {
		return "default"
	}
	return trimmedProfileName
}
