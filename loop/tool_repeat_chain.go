package loop

import (
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

// The first run length delivers a short nudge and every later one names the call, so an
// agent that keeps going gets louder evidence instead of the same sentence again.
var toolRepeatReminderRunLengths = []int{3, 5, 8}

// Detection always compares the whole canonical key, so a long payload cannot slip a loop
// past the chain. Only what the reminder quotes back is shortened.
const toolRepeatArgumentsPreviewLimit = 500

// Bookkeeping interleaved into a loop must not launder it: plan_update between two
// identical searches still leaves two consecutive identical searches.
func chainTransparentToolName(toolName string) bool {
	return toolcontract.ToolNamesMatch(toolName, toolcontract.PlanUpdateToolName)
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
		"Repeating it is not making progress. Do not call it with this input again — read the latest result and choose a different action, a different input, or finish if there is already enough to answer with.",
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

// Advisory only: the call already ran and its own result stands. Whether to retry
// differently, gather more, or finish stays with the model.
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
