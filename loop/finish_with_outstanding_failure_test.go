package loop

import (
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func outstandingFailure() turnObservation {
	return turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "desktop_act",
		ToolInputKey:  "desktop_act\x00{}",
		Failure:       &toolcontract.ToolFailure{Kind: toolcontract.FailureExternalService, Code: "operation_failed"},
	}
}

func finishResolving(failureResolution string) turnActionDocument {
	finish := satisfiedFinishDocument("could not open the page")
	finish.FailureResolution = failureResolution
	return finish
}

func TestFinishWhileAFailureIsOutstandingMustSayHowItWasResolved(t *testing.T) {
	result := validateCompletionFacts(AgentTurnRequest{}, []turnObservation{outstandingFailure()}, finishResolving(""))
	if result.IsSatisfied {
		t.Fatal("a satisfied finish with an outstanding failure and no failureResolution completed the task")
	}
}

func TestFinishWhileAFailureIsOutstandingCompletesWhenItNamesARecovery(t *testing.T) {
	for _, failureResolution := range []string{failureResolutionRecoveredWithSuccess, failureResolutionNoToolFallback} {
		result := validateCompletionFacts(AgentTurnRequest{}, []turnObservation{outstandingFailure()}, finishResolving(failureResolution))
		if !result.IsSatisfied {
			t.Fatalf("failureResolution=%s was refused: %s", failureResolution, result.Message)
		}
	}
}

func TestFinishWithNoOutstandingFailureNeedsNoResolution(t *testing.T) {
	result := validateCompletionFacts(AgentTurnRequest{}, nil, finishResolving(""))
	if !result.IsSatisfied {
		t.Fatalf("a finish with nothing failed was refused: %s", result.Message)
	}
}
