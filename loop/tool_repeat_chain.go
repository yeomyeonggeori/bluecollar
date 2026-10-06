package loop

import (
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

var toolRepeatReminderRunLengths = []int{3, 5, 8}

const toolRepeatArgumentsPreviewLimit = 500

func chainTransparentToolName(toolName string) bool {
	return toolexposure.ToolNamesMatch(toolName, toolcontract.PlanToolName)
}

func consecutiveIdenticalToolCallCount(observations []turnObservation, toolInputKey string) int {
	trimmedKey := strings.TrimSpace(toolInputKey)
	if trimmedKey == "" {
		return 0
	}
	count := 0
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		observedKey := strings.TrimSpace(observation.ToolInputKey)
		if observedKey == "" || chainTransparentToolName(observation.Tool) {
			continue
		}
		if observedKey != trimmedKey {
			break
		}
		count++
	}
	return count
}

func isToolRepeatReminderRunLength(count int) bool {
	for _, runLength := range toolRepeatReminderRunLengths {
		if count == runLength {
			return true
		}
	}
	return false
}

func toolRepeatReminderMessage(toolName string, toolInputKey string, count int) string {
	if count == toolRepeatReminderRunLengths[0] {
		return "You are repeating the same call with the same input. Read the last result again before calling it once more: if the task is not done, change the approach or the input rather than repeating the call."
	}
	return strings.Join([]string{
		"Repeated call: " + strings.TrimSpace(toolName) + ", " + strconv.Itoa(count) + " times in a row with the same input.",
		"Input: " + repeatedInputPreview(toolInputKey),
		"Repeating it is not making progress. Do not call it with this input again — read the latest result and choose a different action, a different input, or a final reply if there is already enough to answer with.",
	}, "\n")
}

func repeatedInputPreview(toolInputKey string) string {
	canonicalInput := toolInputKey
	if separatorIndex := strings.IndexByte(canonicalInput, 0); separatorIndex >= 0 {
		canonicalInput = canonicalInput[separatorIndex+1:]
	}
	if len(canonicalInput) <= toolRepeatArgumentsPreviewLimit {
		return canonicalInput
	}
	kept := strings.ToValidUTF8(canonicalInput[:toolRepeatArgumentsPreviewLimit], "")
	return kept + " … (+" + strconv.Itoa(len(canonicalInput)-len(kept)) + " more characters)"
}

func toolRepeatReminderObservation(observations []turnObservation, observation turnObservation) (turnObservation, int, bool) {
	if strings.TrimSpace(observation.ToolInputKey) == "" || chainTransparentToolName(observation.Tool) {
		return turnObservation{}, 0, false
	}
	count := consecutiveIdenticalToolCallCount(observations, observation.ToolInputKey)
	if !isToolRepeatReminderRunLength(count) {
		return turnObservation{}, count, false
	}
	return newContentObservation(
		nextObservationIDForObservations(observations),
		"policy",
		strings.TrimSpace(observation.Tool),
		toolRepeatReminderMessage(observation.Tool, observation.ToolInputKey, count),
	), count, true
}

type reminderEventBody struct {
	Observation         turnObservation `json:"observation"`
	RepeatedObservation string          `json:"repeatedObservationID"`
	ConsecutiveCalls    int             `json:"consecutiveCalls,omitempty"`
}

func unchangedResultReminderObservation(toolSet *toolcontract.ToolSet, observations []turnObservation, observation turnObservation) (turnObservation, bool) {
	if observation.RepeatsObservationID == "" {
		return turnObservation{}, false
	}
	if !toolChangesNothingOfItsOwn(toolSet, observation.Tool) && !isToolRepeatReminderRunLength(identicalResultCount(observations, observation)) {
		return turnObservation{}, false
	}
	return newContentObservation(
		nextObservationIDForObservations(observations),
		"policy",
		strings.TrimSpace(observation.Tool),
		unchangedResultReminderMessage(observation.Tool, observation.RepeatsObservationID),
	), true
}

func identicalResultCount(observations []turnObservation, observation turnObservation) int {
	count := 0
	for _, earlier := range observations {
		if earlier.ObservationID == observation.RepeatsObservationID || earlier.RepeatsObservationID == observation.RepeatsObservationID {
			count++
		}
	}
	return count
}

func toolChangesNothingOfItsOwn(toolSet *toolcontract.ToolSet, toolName string) bool {
	if toolSet == nil {
		return false
	}
	toolDefinition, isKnown := toolSet.ToolDefinition(strings.TrimSpace(toolName))
	return isKnown && toolDefinition.SideEffectClass == toolcontract.ToolSideEffectNone
}

func unchangedResultReminderMessage(toolName string, repeatedObservationID string) string {
	return strings.Join([]string{
		strings.TrimSpace(toolName) + " returned exactly what it returned in " + repeatedObservationID + ", so this call changed nothing.",
		"Spend the next step on the work itself, or call it with something different from what is already recorded.",
	}, "\n")
}
