package loop

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func failedShellObservation(observationID string, command string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          "terminal_run",
		ToolInputKey:  "terminal_run\x00" + command,
		Failure: &toolcontract.ToolFailure{
			Kind:  toolcontract.FailureUnknown,
			Code:  toolcontract.FailureCodes.OperationFailed.String(),
			Stage: "terminal_run",
		},
	}
}

func succeededShellObservation(observationID string) turnObservation {
	return turnObservation{ObservationID: observationID, Action: "continue", Tool: "terminal_run"}
}

func TestARouteThatKeepsFailingTheSameWayStopsBeingRetried(t *testing.T) {
	observations := []turnObservation{
		failedShellObservation("obs-001", "cli venmo login --email a"),
		failedShellObservation("obs-002", "cli venmo login --email b"),
		failedShellObservation("obs-003", "cli venmo login --email c"),
	}
	failureDebt, hasDebt := activeFailureDebt(observations)
	if !hasDebt {
		t.Fatal("three failed commands with nothing between them owe a debt")
	}

	if repeatedFailureSignature(observations, failureDebt) == "" {
		t.Fatal("banging on the same route is what this guard is for")
	}
}

func TestFailuresASuccessAlreadyClearedAreNotStillRecurring(t *testing.T) {
	observations := []turnObservation{
		failedShellObservation("obs-001", "cli venmo login --email a"),
		succeededShellObservation("obs-002"),
		failedShellObservation("obs-003", "python3 -c 'import requests'"),
		succeededShellObservation("obs-004"),
		failedShellObservation("obs-005", "python3 -c 'import apis'"),
	}
	failureDebt, hasDebt := activeFailureDebt(observations)
	if !hasDebt {
		t.Fatal("the latest failure still owes a debt")
	}

	if signature := repeatedFailureSignature(observations, failureDebt); signature != "" {
		t.Fatalf("terminal_run gives every shell failure one signature, so counting across the turn calls three unrelated errors a structural repeat: %s", signature)
	}
}

func failedCorrectedRetry(observationID string, command string, attemptKey string) turnObservation {
	observation := failedShellObservation(observationID, command)
	observation.RecoveryStep = recoveryStepCorrectedRetry
	observation.RecoveryAttemptSpent = true
	observation.RecoveryAttemptKey = attemptKey
	return observation
}

func TestCorrectingTheSameBrokenCallTwiceSpendsTheBudget(t *testing.T) {
	observations := []turnObservation{
		failedShellObservation("obs-001", "cli venmo login --email a"),
		failedCorrectedRetry("obs-002", "cli venmo login --email b", "terminal_run|login"),
	}

	if recoveryBudgetAllowsStep(observations, defaultRecoveryBudget(), recoveryStepCorrectedRetry, "terminal_run|login") {
		t.Fatal("correcting the same call again is the retry loop this budget exists to stop")
	}
}

func TestCorrectingADifferentBrokenCallHasItsOwnBudget(t *testing.T) {
	observations := []turnObservation{
		failedShellObservation("obs-001", "cli venmo login --email a"),
		failedCorrectedRetry("obs-002", "cli venmo login --email b", "terminal_run|login"),
	}

	if !recoveryBudgetAllowsStep(observations, defaultRecoveryBudget(), recoveryStepCorrectedRetry, "terminal_run|import") {
		t.Fatal("a different broken command is a different problem, and the last correction did not spend its budget")
	}
	if recoveryBudgetAllowsStep(observations, defaultRecoveryBudget(), recoveryStepCorrectedRetry, "") {
		t.Fatal("counting every correction together is what refused the second problem its first attempt")
	}
}
