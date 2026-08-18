package loop

import "strings"

// Dropping the middle of a long result costs no model call and loses nothing the ledger
// did not keep, so it runs before the summarizer, which does cost one.
const taskContextPruneThresholdCharacters = 8192

const taskContextPruneKeepCharacters = 4096

// This shrinks a projection, never the recorded observations: the ledger keeps every result
// whole. The newest is left alone because it is the one the agent is acting on, and a pinned
// observation is evidence something else already cites.
func observationsWithLongToolResultsPruned(observations []turnObservation, pinnedObservationIDs map[string]bool) ([]turnObservation, bool) {
	prunedObservations := make([]turnObservation, len(observations))
	copy(prunedObservations, observations)
	didPrune := false
	for index := 0; index < len(prunedObservations)-1; index++ {
		observation := prunedObservations[index]
		if pinnedObservationIDs[strings.TrimSpace(observation.ObservationID)] {
			continue
		}
		content := observation.ContentText()
		if len(content) <= taskContextPruneThresholdCharacters {
			continue
		}
		prunedObservations[index].Output.Content = withMiddleElided(content, taskContextPruneKeepCharacters)
		didPrune = true
	}
	return prunedObservations, didPrune
}
