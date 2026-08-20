package loop

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"testing"
)

func failureDebtForFailedCall(toolName string, inputDocument string) FailureDebt {
	return FailureDebt{LatestFailure: turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          toolName,
		ToolInputKey:  canonicalToolCallKey(toolName, json.RawMessage(inputDocument)),
		Failure:       &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.NotFound.String()},
	}}
}

func TestWorkOnADifferentEventIsNotChargedToTheFailedDeletesBudget(t *testing.T) {
	failedDelete := failureDebtForFailedCall("calendar_delete", `{"eventHint":"지난 워크숍 회고"}`)
	recoveryStep := classifyRecoveryStep(calendarRecoveryToolSet(), failedDelete, "calendar_update", json.RawMessage(`{"eventHint":"상하이 생산 미팅","people":["이샘플","박예시"]}`))
	if recoveryStep != recoveryStepIndependentWork {
		t.Fatalf("a call naming a different event is independent work, got %q", recoveryStep)
	}
	if recoveryStepSpendsBudget(recoveryStep) {
		t.Fatal("independent work must not be charged to the failed call's recovery budget")
	}
}

func TestASiblingMutationOnTheSameEventIsStillAnAlternateRoute(t *testing.T) {
	failedDelete := failureDebtForFailedCall("calendar_delete", `{"eventHint":"지난 워크숍 회고"}`)
	recoveryStep := classifyRecoveryStep(calendarRecoveryToolSet(), failedDelete, "calendar_update", json.RawMessage(`{"eventHint":"지난 워크숍 회고","title":"취소됨"}`))
	if recoveryStep != recoveryStepAlternateRoute {
		t.Fatalf("a sibling mutation naming the same event is an alternate route, got %q", recoveryStep)
	}
}

func TestARouteWithNoInputFieldsInCommonIsStillCharged(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		namespacedToolDefinition("file_edit", "file", toolcontract.ToolSideEffectStateChange),
		namespacedToolDefinition("terminal_run", "terminal", toolcontract.ToolSideEffectStateChange),
	})
	failedEdit := failureDebtForFailedCall("file_edit", `{"path":"home/notes/a.md"}`)
	recoveryStep := classifyRecoveryStep(toolSet, failedEdit, "terminal_run", json.RawMessage(`{"command":"sed -i s/a/b/ home/notes/a.md"}`))
	if recoveryStep != recoveryStepAdjacentTool {
		t.Fatalf("calls with no comparable field in common keep their old classification, got %q", recoveryStep)
	}
	if !recoveryStepSpendsBudget(recoveryStep) {
		t.Fatal("an unknown target relation must stay charged")
	}
}

func TestTwoCallsSharingOnlyTheirCalendarAreNotTreatedAsTheSameEvent(t *testing.T) {
	failedDelete := failureDebtForFailedCall("calendar_delete", `{"calendarID":"primary","eventHint":"지난 워크숍 회고"}`)
	recoveryStep := classifyRecoveryStep(calendarRecoveryToolSet(), failedDelete, "calendar_update", json.RawMessage(`{"calendarID":"primary","eventHint":"상하이 생산 미팅"}`))
	if recoveryStep != recoveryStepIndependentWork {
		t.Fatalf("an agreeing container field must not outweigh a disagreeing event field, got %q", recoveryStep)
	}
}

func TestRetryingTheSameToolOnADifferentTargetIsStillACorrectedRetry(t *testing.T) {
	failedDelete := failureDebtForFailedCall("calendar_delete", `{"eventHint":"지난 워크숍 회고"}`)
	recoveryStep := classifyRecoveryStep(calendarRecoveryToolSet(), failedDelete, "calendar_delete", json.RawMessage(`{"eventHint":"상하이 생산 미팅"}`))
	if recoveryStep != recoveryStepCorrectedRetry {
		t.Fatalf("the same tool again is a corrected retry whatever it names, got %q", recoveryStep)
	}
}

func TestIndependentWorkIsNeverRefused(t *testing.T) {
	observations := []turnObservation{
		{ObservationID: "obs-002", Action: "continue", Tool: "calendar_delete", Failure: &toolcontract.ToolFailure{Code: toolcontract.FailureCodes.NotFound.String()}, ToolInputKey: "calendar_delete\x00{\"eventHint\":\"A\"}"},
		{ObservationID: "obs-003", RecoveryStep: recoveryStepIndependentWork},
		{ObservationID: "obs-004", RecoveryStep: recoveryStepIndependentWork},
	}
	if !recoveryBudgetAllowsStep(observations, exhaustedRecoveryBudgetForTest(), recoveryStepIndependentWork) {
		t.Fatal("work on another object must stay available after the failed call's budget is gone")
	}
}
