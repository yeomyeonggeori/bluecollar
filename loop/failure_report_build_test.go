package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestTheReportTheUserSeesSaysWhatHappened(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-006",
		Action:        "continue",
		Tool:          "shell",
		Failure: &toolcontract.ToolFailure{
			Kind: toolcontract.FailureUnknown, Code: toolcontract.FailureCodes.OperationFailed.String(),
			Stage: "shell", UserSafeSummary: "the command exited 1",
		},
	}
	observation.Output.Content = "Your Venmo balance does not have $91.00 to make this transaction"

	line := latestSafeFailureSummary([]turnObservation{observation}, "something went wrong")

	if !strings.Contains(line, "does not have $91.00") {
		t.Fatalf("an exit status is not something a user can act on: %q", line)
	}
}

func TestAReportWhoseSummaryIsAlreadyTheReasonSaysItOnce(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-007",
		Action:        "continue",
		Tool:          "task_add",
		Failure: &toolcontract.ToolFailure{
			Kind: toolcontract.FailureInvalidInput, Code: toolcontract.FailureCodes.InvalidInput.String(),
			Stage: "task_add", UserSafeSummary: "a title is required",
		},
	}
	observation.Output.Content = "a title is required"

	if line := latestSafeFailureSummary([]turnObservation{observation}, "fallback"); line != "a title is required" {
		t.Fatalf("expected the reason once, got %q", line)
	}
}
