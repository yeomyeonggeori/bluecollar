package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

import "testing"

func TestActiveFailureDebtKeepsDebtAfterInspectionToolWithoutRecoveryStep(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "site_serve",
			Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String()},
			ToolInputKey:  "site_serve\x00{\"siteReference\":\"site-1\"}",
		},
		{
			ObservationID:  "obs-002",
			Action:         "continue",
			Tool:           "site_list",
			ToolIsReadOnly: true,
			Output:         toolcontract.ToolOutput{Content: `{"siteID":"site-1","status":"failed","publishedURL":"https://portfolio.example"}`},
		},
	})

	if !hasFailureDebt {
		t.Fatal("expected inspection status result to keep failure debt active")
	}
}

func TestActiveFailureDebtIgnoresMissingOptionalSiteControlFile(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		{
			ObservationID: "obs-001",
			Action:        "continue",
			Tool:          "file_read",
			Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.NotFound.String()},
			ToolInputKey:  "file_read\x00{\"path\":\"home/sites/site-1/.internkim/artifact-brief.md\"}",
		},
	})

	if hasFailureDebt {
		t.Fatal("expected missing optional site control file not to create failure debt")
	}
}

func failedCalendarDeleteObservation(observationID string, eventHint string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          "calendar_delete",
		Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.NotFound.String(), Stage: "calendar_lookup"},
		ToolInputKey:  "calendar_delete\x00{\"eventHint\":\"" + eventHint + "\"}",
	}
}

func TestIndependentWorkThatSucceedsDoesNotClearTheDebtOfTheFailedCall(t *testing.T) {
	_, hasFailureDebt := activeFailureDebt([]turnObservation{
		failedCalendarDeleteObservation("obs-001", "지난 워크숍 회고"),
		{
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "calendar_update",
			RecoveryStep:  recoveryStepIndependentWork,
			Output:        toolcontract.ToolOutput{Content: `{"eventID":"event-2"}`},
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
			ObservationID: "obs-002",
			Action:        "continue",
			Tool:          "calendar_update",
			RecoveryStep:  recoveryStepAlternateRoute,
			Output:        toolcontract.ToolOutput{Content: `{"eventID":"event-1"}`},
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
			ObservationID:        "obs-002",
			Action:               "continue",
			Tool:                 "calendar_update",
			RecoveryStep:         recoveryStepAlternateRoute,
			RecoveryAttemptSpent: true,
			Output:               toolcontract.ToolOutput{Content: `{"eventID":"event-1"}`},
		},
		{
			ObservationID: "obs-003",
			Action:        "continue",
			Tool:          "message_send",
			Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.OperationFailed.String(), Stage: "message_send"},
			ToolInputKey:  "message_send\x00{\"personHint\":\"이샘플\"}",
		},
	}

	if !recoveryBudgetAllowsStep(observations, defaultRecoveryBudget(), recoveryStepAlternateRoute, "") {
		t.Fatal("a budget spent recovering an earlier failure must not refuse the first recovery of the next one")
	}
}
