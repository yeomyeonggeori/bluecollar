package agentcontract

import "testing"

func TestExternalSendIntentSurvivesIntakeDecisionConversionAndRestoration(t *testing.T) {
	turnDecision := TurnDecision{
		Classification:          IntakeClassificationBoundedTask,
		TaskShape:               TaskShapeMaintenanceTask,
		TaskLevel:               TaskLevelLow,
		IsExternalSendRequested: true,
	}

	intakeDecision := turnDecision.IntakeDecision()
	if !intakeDecision.IsExternalSendRequested {
		t.Fatal("expected intake conversion to preserve explicit external-send intent")
	}

	restoredDecision := TurnDecision{}.WithRestoredIntakeState(intakeDecision)
	if !restoredDecision.IsExternalSendRequested {
		t.Fatal("expected restored intake state to preserve explicit external-send intent")
	}
}
