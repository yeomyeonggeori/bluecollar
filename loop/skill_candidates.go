package loop

import (
	"context"
	"strings"
)

func visibleCandidateSkillInstructions(skillInstructions []SkillInstruction, candidateByName map[string]SkillCandidate, requesterCircles []string) []SkillInstruction {
	return append([]SkillInstruction{}, skillInstructions...)
}

func blockedSkillSelectionDecisions(skillInstructions []SkillInstruction, existingSkillDecisions []SkillSelectionDecision, request AgentRequest, profileName string) []SkillSelectionDecision {
	existingDecisionByName := map[string]bool{}
	for _, skillDecision := range existingSkillDecisions {
		existingDecisionByName[skillDecision.Name] = true
	}
	blockedDecisions := []SkillSelectionDecision{}
	for _, skillInstruction := range skillInstructions {
		if existingDecisionByName[skillInstruction.Name] {
			continue
		}
		skillDecision := skillAvailabilityDecision(skillInstruction, request, profileName)
		if skillDecision.Status == "skipped" && skillDecision.Reason != "no_trigger_matched" {
			blockedDecisions = append(blockedDecisions, skillDecision)
		}
	}
	return blockedDecisions
}

func retrieveSkillCandidates(ctx context.Context, request AgentRequest, skillInstructions []SkillInstruction, skillRetriever SkillRetriever, querySet SkillSearchQuerySet, hasStructuredQueries bool) SkillRetrievalResult {
	if hasStructuredQueries {
		querySet = skillRetrievalQuerySet(request, querySet)
	}
	var retrievalResult SkillRetrievalResult
	if skillRetriever != nil {
		if hasStructuredQueries {
			retrievalResult = skillRetriever.Search(ctx, request, skillInstructions, querySet, maxSkillIndexCandidateCount)
		} else {
			retrievalResult = skillRetriever.Retrieve(ctx, request, skillInstructions, maxSkillIndexCandidateCount)
		}
	} else if hasStructuredQueries {
		retrievalResult = retrieveSkillsWithBM25QuerySet(request, skillInstructions, querySet, maxSkillIndexCandidateCount, "embedding_unconfigured")
	} else {
		retrievalResult = retrieveSkillsWithBM25(request, skillInstructions, skillSelectionPrompt(request), maxSkillIndexCandidateCount, "embedding_unconfigured")
	}
	return addRequiredEvidenceSkillCandidates(retrievalResult, request, skillInstructions, maxSkillIndexCandidateCount)
}

func addRequiredEvidenceSkillCandidates(result SkillRetrievalResult, request AgentRequest, skillInstructions []SkillInstruction, limit int) SkillRetrievalResult {
	requiredToolNames := stringSet(outcomeContractRequiredToolNames(request.ActiveGoal.OutcomeContract))
	existingCandidateNames := map[string]bool{}
	for _, candidate := range result.SelectedCandidates {
		existingCandidateNames[candidate.Name] = true
	}
	requiredCandidates := []SkillCandidate{}
	for _, skillInstruction := range skillInstructions {
		if existingCandidateNames[skillInstruction.Name] || allToolReferencesMissing(skillInstruction, request) {
			continue
		}
		if !skillOwnsAnyTool(skillInstruction, requiredToolNames) {
			continue
		}
		requiredCandidates = append(requiredCandidates, SkillCandidate{
			Name:   skillInstruction.Name,
			Score:  1,
			Reason: "required_evidence_tool",
			Source: skillInstruction.Source,
		})
	}
	result.SelectedCandidates = limitSkillCandidates(append(requiredCandidates, result.SelectedCandidates...), limit)
	result.CandidateCount = len(result.SelectedCandidates)
	return result
}

func skillOwnsAnyTool(skillInstruction SkillInstruction, toolNames map[string]bool) bool {
	for _, toolName := range SkillToolNames(skillInstruction) {
		if toolNames[toolName] {
			return true
		}
	}
	return false
}

func skillRetrievalQuerySet(request AgentRequest, supplementalQueries SkillSearchQuerySet) SkillSearchQuerySet {
	queries := []SkillSearchQuery{{Description: strings.TrimSpace(request.Prompt)}}
	queries = append(queries, supplementalQueries.Queries...)
	return normalizeSkillSearchQuerySet(SkillSearchQuerySet{Queries: queries})
}

func candidateSkillInstructions(skillInstructions []SkillInstruction, skillCandidates []SkillCandidate) []SkillInstruction {
	skillInstructionByName := skillInstructionByName(skillInstructions)
	candidateInstructions := []SkillInstruction{}
	for _, skillCandidate := range skillCandidates {
		if skillInstruction, isFound := skillInstructionByName[skillCandidate.Name]; isFound {
			candidateInstructions = append(candidateInstructions, skillInstruction)
		}
	}
	return candidateInstructions
}

func skillCandidateByName(skillCandidates []SkillCandidate) map[string]SkillCandidate {
	candidateByName := map[string]SkillCandidate{}
	for _, skillCandidate := range skillCandidates {
		candidateByName[skillCandidate.Name] = skillCandidate
	}
	return candidateByName
}

func skillDecisionForCandidate(skillInstruction SkillInstruction, skillCandidate SkillCandidate, profileName string) SkillSelectionDecision {
	if skillCandidate.Score >= minimumSelectionScoreForCandidate(skillCandidate) {
		return SkillSelectionDecision{
			Name:        skillInstruction.Name,
			Status:      "selected",
			Reason:      skillCandidate.Reason,
			ProfileName: profileName,
			Score:       skillCandidate.Score,
			Source:      skillInstruction.Source,
		}
	}
	return SkillSelectionDecision{
		Name:        skillInstruction.Name,
		Status:      "skipped",
		Reason:      "candidate_below_selection_threshold",
		ProfileName: profileName,
		Score:       skillCandidate.Score,
		Source:      skillInstruction.Source,
	}
}

func skillDecisionForArbitratedCandidate(skillInstruction SkillInstruction, skillCandidate SkillCandidate, selectedSkillNames map[string]bool, profileName string) SkillSelectionDecision {
	if selectedSkillNames[skillInstruction.Name] {
		skillDecision := selectedSkillDecision(skillInstruction, profileName, "contract_arbitration")
		skillDecision.Score = skillCandidate.Score
		return skillDecision
	}
	skillDecision := skippedSkillDecision(skillInstruction, profileName, "not_selected_by_contract_arbitration", nil)
	skillDecision.Score = skillCandidate.Score
	return skillDecision
}

func minimumSelectionScoreForCandidate(skillCandidate SkillCandidate) float64 {
	if skillCandidate.Reason == "bm25_fallback" {
		return minimumBM25SelectionScore
	}
	return 0
}
