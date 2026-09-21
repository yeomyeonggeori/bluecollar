package agentcontract

import (
	"encoding/json"
	"testing"
)

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

func TestIndependentWorkFactSurvivesIntakeConversionAndRestoration(t *testing.T) {
	turnDecision := TurnDecision{
		Route:              TurnRouteClarify,
		TaskLevel:          TaskLevelLow,
		HasIndependentWork: true,
		RawDecisionRoute:   TurnRouteClarify,
	}

	intakeDecision := turnDecision.IntakeDecision()
	if !intakeDecision.HasIndependentWork || intakeDecision.RawDecisionRoute != TurnRouteClarify {
		t.Fatalf("expected intake conversion to preserve independent-work facts, got %+v", intakeDecision)
	}

	restoredDecision := TurnDecision{}.WithRestoredIntakeState(intakeDecision)
	if !restoredDecision.HasIndependentWork || restoredDecision.RawDecisionRoute != TurnRouteClarify {
		t.Fatalf("expected restored intake state to preserve independent-work facts, got %+v", restoredDecision)
	}
}

func TestAgentIntakeSerializationKeepsFalseIndependentWorkFact(t *testing.T) {
	serializedDecision, errorValue := json.Marshal(TurnDecision{}.IntakeDecision())
	if errorValue != nil {
		t.Fatalf("expected intake decision to serialize: %v", errorValue)
	}
	var serializedFields map[string]json.RawMessage
	if errorValue = json.Unmarshal(serializedDecision, &serializedFields); errorValue != nil {
		t.Fatalf("expected intake decision to deserialize: %v", errorValue)
	}
	if string(serializedFields["hasIndependentWork"]) != "false" {
		t.Fatalf("expected false independent-work fact to be emitted, got %s", serializedFields["hasIndependentWork"])
	}
}

func TestOlderIntakeStateWithoutIndependentWorkDefaultsFalse(t *testing.T) {
	var intakeDecision IntakeDecision
	if errorValue := json.Unmarshal([]byte(`{"classification":"needs_confirmation","level":"low"}`), &intakeDecision); errorValue != nil {
		t.Fatalf("expected older intake state to deserialize: %v", errorValue)
	}
	if intakeDecision.HasIndependentWork {
		t.Fatal("expected a missing independent-work fact to default to false")
	}
}
