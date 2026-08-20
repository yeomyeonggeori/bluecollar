package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"testing"
)

func namespacedToolDefinition(toolName string, namespace string, sideEffectClass string) toolcontract.ToolDefinition {
	definition := testToolDescriptor(toolName)
	definition.Namespace = namespace
	definition.SideEffectClass = sideEffectClass
	return definition
}

func calendarRecoveryToolSet() *toolcontract.ToolSet {
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		namespacedToolDefinition("calendar_delete", "calendar", toolcontract.ToolSideEffectStateChange),
		namespacedToolDefinition("calendar_update", "calendar", toolcontract.ToolSideEffectStateChange),
		namespacedToolDefinition("calendar_list", "calendar", toolcontract.ToolSideEffectRead),
	})
}

func failureDebtForTool(toolName string) FailureDebt {
	return FailureDebt{LatestFailure: turnObservation{ObservationID: "obs-001", Action: "continue", Tool: toolName}}
}

func TestLookingUpTheTargetOfAFailedMutationCountsAsInspection(t *testing.T) {
	recoveryStep := classifyRecoveryStep(calendarRecoveryToolSet(), failureDebtForTool("calendar_delete"), "calendar_list")
	if recoveryStep != recoveryStepInspection {
		t.Fatalf("a read-only lookup after a failed mutation is inspection, got %q", recoveryStep)
	}
}

func TestInspectionAfterAFailedMutationIsNotRationed(t *testing.T) {
	budget := defaultRecoveryBudget()
	observations := []turnObservation{
		{ObservationID: "obs-002", RecoveryStep: recoveryStepInspection, RecoveryAttemptSpent: true},
		{ObservationID: "obs-003", RecoveryStep: recoveryStepInspection, RecoveryAttemptSpent: true},
	}
	if !recoveryBudgetAllowsStep(observations, budget, recoveryStepInspection) {
		t.Fatal("inspection must stay available so the agent can keep searching for the target it could not resolve")
	}
}

func TestAnotherMutationInTheSameNamespaceIsStillAnAlternateRoute(t *testing.T) {
	recoveryStep := classifyRecoveryStep(calendarRecoveryToolSet(), failureDebtForTool("calendar_delete"), "calendar_update")
	if recoveryStep != recoveryStepAlternateRoute {
		t.Fatalf("a sibling mutation is an alternate route, got %q", recoveryStep)
	}
}

func TestRetryingTheSameToolIsStillACorrectedRetry(t *testing.T) {
	recoveryStep := classifyRecoveryStep(calendarRecoveryToolSet(), failureDebtForTool("calendar_list"), "calendar_list")
	if recoveryStep != recoveryStepCorrectedRetry {
		t.Fatalf("the same tool again is a corrected retry, got %q", recoveryStep)
	}
}
