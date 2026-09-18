package intake

import "sort"

const likelyToolProbabilityThreshold = 0.3
const likelyToolCountLimit = 24

const recordedToolProbabilityFloor = 0.05

const toolLikelihoodGuidanceReference = "Judge it by toolLikelihoodGuidance in the state."

const toolLikelihoodGuidance = "Every question of the form \"Will the work call <tool>?\" names one tool from availableTools, whose description says what that tool does. " +
	"Answer with how likely the work this message asks for is to call that tool at any point before the work is done, not only as its first step: the tools the later steps need count too. " +
	"When the visible conversation shows an artifact already created for this sender, an edit to it uses that artifact's read and edit tools rather than its create tool. " +
	"Do not raise a tool because it exists, because its name shares a word with the message, or because it might conceivably help; raise it when the work plainly needs what the tool does. " +
	"Raise nothing when the message needs no tool at all."

func toolLikelihoodGuidanceFor(toolDescriptions []decisionTool) string {
	if len(toolDescriptions) == 0 {
		return ""
	}
	return toolLikelihoodGuidance
}

func selectLikelyToolNames(probabilityByToolName map[string]float64, candidateToolNames []string) []string {
	selectedToolNames := []string{}
	for _, toolName := range toolNamesRankedByProbability(probabilityByToolName, candidateToolNames) {
		if probabilityByToolName[toolName] < likelyToolProbabilityThreshold || len(selectedToolNames) >= likelyToolCountLimit {
			break
		}
		selectedToolNames = append(selectedToolNames, toolName)
	}
	if len(selectedToolNames) == 0 {
		return nil
	}
	return selectedToolNames
}

func toolNamesRankedByProbability(probabilityByToolName map[string]float64, candidateToolNames []string) []string {
	rankedToolNames := append([]string{}, candidateToolNames...)
	sort.Strings(rankedToolNames)
	sort.SliceStable(rankedToolNames, func(left int, right int) bool {
		return probabilityByToolName[rankedToolNames[left]] > probabilityByToolName[rankedToolNames[right]]
	})
	return rankedToolNames
}

func recordedToolProbabilities(probabilityByToolName map[string]float64) map[string]float64 {
	recordedProbabilities := map[string]float64{}
	for toolName, probability := range probabilityByToolName {
		if probability >= recordedToolProbabilityFloor {
			recordedProbabilities[toolName] = probability
		}
	}
	return recordedProbabilities
}
