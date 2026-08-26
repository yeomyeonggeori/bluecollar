package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func longToolObservation(observationID string, characterCount int) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          toolcontract.ShellToolName,
		Output:        toolcontract.ToolOutput{Content: strings.Repeat("x", characterCount)},
	}
}

func TestAnOldOversizedResultLosesItsMiddleBeforeAnyModelIsAsked(t *testing.T) {
	observations := []turnObservation{
		longToolObservation("obs-1", taskContextPruneThresholdCharacters*2),
		longToolObservation("obs-2", 100),
	}

	pruned, didPrune := observationsWithLongToolResultsPruned(observations, map[string]bool{})

	if !didPrune {
		t.Fatal("a prompt that no longer fits has an oversized result to give up before it needs a summary")
	}
	if len(pruned[0].ContentText()) >= len(observations[0].ContentText()) {
		t.Fatalf("pruning that does not shrink anything is wasted work, got %d against %d", len(pruned[0].ContentText()), len(observations[0].ContentText()))
	}
	if observations[0].ContentText() != strings.Repeat("x", taskContextPruneThresholdCharacters*2) {
		t.Fatal("the recorded observation must survive whole: the ledger is what a later reader replays")
	}
}

func TestTheResultTheAgentIsActingOnIsLeftWhole(t *testing.T) {
	observations := []turnObservation{
		longToolObservation("obs-1", 100),
		longToolObservation("obs-2", taskContextPruneThresholdCharacters*2),
	}

	pruned, _ := observationsWithLongToolResultsPruned(observations, map[string]bool{})

	if len(pruned[1].ContentText()) != len(observations[1].ContentText()) {
		t.Fatal("cutting up the result the agent just asked for is how it loses the thing it was about to use")
	}
}

func TestPinnedEvidenceIsNeverPruned(t *testing.T) {
	observations := []turnObservation{
		longToolObservation("obs-1", taskContextPruneThresholdCharacters*2),
		longToolObservation("obs-2", 100),
	}

	pruned, _ := observationsWithLongToolResultsPruned(observations, map[string]bool{"obs-1": true})

	if len(pruned[0].ContentText()) != len(observations[0].ContentText()) {
		t.Fatal("evidence something already cites cannot be shortened underneath it")
	}
}

func TestResultsThatAlreadyFitAreLeftExactlyAsTheyAre(t *testing.T) {
	observations := []turnObservation{longToolObservation("obs-1", 100), longToolObservation("obs-2", 100)}

	pruned, didPrune := observationsWithLongToolResultsPruned(observations, map[string]bool{})

	if didPrune {
		t.Fatal("reporting a prune that changed nothing would send the turn on to a summary it does not need")
	}
	if pruned[0].ContentText() != observations[0].ContentText() {
		t.Fatal("a result that fits arrives exactly as the tool wrote it")
	}
}
