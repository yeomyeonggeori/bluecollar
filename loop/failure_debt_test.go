package loop

import (
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestActiveFailureDebtKeepsDebtAfterInspectionToolWithoutRecoveryStep(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-001", Failure: &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String()}},
			Action:         "continue",
			Tool:           "task_update",
			ToolInputKey:   "task_update\x00{\"taskReference\":\"task-1\"}",
		},
		{
			ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-002"},
			Action:         "continue",
			Tool:           "task_list",
			ToolIsReadOnly: true,
			Output:         toolcontract.ToolOutput{Content: `{"taskID":"task-1","status":"failed"}`},
		},
	})

	if !hasFailureDebt {
		t.Fatal("expected inspection status result to keep failure debt active")
	}
}

func failedCalendarDeleteObservation(observationID string, eventHint string) turnObservation {
	return turnObservation{
		ToolCallResult: agentcontract.ToolCallResult{ObservationID: observationID, Failure: &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.NotFound.String(), Stage: "calendar_lookup"}},
		Action:         "continue",
		Tool:           "calendar_delete",
		ToolInputKey:   "calendar_delete\x00{\"eventHint\":\"" + eventHint + "\"}",
	}
}

func TestIndependentWorkThatSucceedsDoesNotClearTheDebtOfTheFailedCall(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		failedCalendarDeleteObservation("obs-001", "지난 워크숍 회고"),
		{
			ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-002"},
			Action:         "continue",
			Tool:           "calendar_update",
			RecoveryStep:   recoveryStepIndependentWork,
			Output:         toolcontract.ToolOutput{Content: `{"eventID":"event-2"}`},
		},
	})

	if !hasFailureDebt {
		t.Fatal("work on another object must not settle the debt of the call that failed")
	}
}

func TestAnAlternateRouteThatSucceedsStillClearsTheDebt(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		failedCalendarDeleteObservation("obs-001", "지난 워크숍 회고"),
		{
			ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-002"},
			Action:         "continue",
			Tool:           "calendar_update",
			RecoveryStep:   recoveryStepAlternateRoute,
			Output:         toolcontract.ToolOutput{Content: `{"eventID":"event-1"}`},
		},
	})

	if hasFailureDebt {
		t.Fatal("a recovery route that reached the failed call's target settles its debt")
	}
}

func TestBudgetSpentRecoveringAnEarlierFailureDoesNotRefuseTheNextOne(t *testing.T) {
	observations := []turnObservation{
		failedCalendarDeleteObservation("obs-001", "지난 워크숍 회고"),
		{
			ToolCallResult:       agentcontract.ToolCallResult{ObservationID: "obs-002"},
			Action:               "continue",
			Tool:                 "calendar_update",
			RecoveryStep:         recoveryStepAlternateRoute,
			RecoveryAttemptSpent: true,
			Output:               toolcontract.ToolOutput{Content: `{"eventID":"event-1"}`},
		},
		{
			ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-003", Failure: &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String(), Stage: "message_send"}},
			Action:         "continue",
			Tool:           "message_send",
			ToolInputKey:   "message_send\x00{\"personHint\":\"이샘플\"}",
		},
	}

	if !recoveryBudgetAllowsStep(observations, defaultRecoveryBudget(), recoveryStepAlternateRoute, "") {
		t.Fatal("a budget spent recovering an earlier failure must not refuse the first recovery of the next one")
	}
}
