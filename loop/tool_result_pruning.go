package loop

import "strings"

const taskContextPruneThresholdCharacters = 8192

const taskContextPruneKeepCharacters = 4096

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
