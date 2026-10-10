package turnclassification

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestExternalSendIntentSurvivesIntakeDecisionConversionAndRestoration(t *testing.T) {
	turnDecision := TurnDecision{
		Classification:          agentcontract.IntakeClassificationBoundedTask,
		TaskShape:               agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:               agentcontract.TaskLevelLow,
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
		Route:              agentcontract.TurnRouteClarify,
		TaskLevel:          agentcontract.TaskLevelLow,
		HasIndependentWork: true,
	}

	intakeDecision := turnDecision.IntakeDecision()
	if !intakeDecision.HasIndependentWork {
		t.Fatalf("expected intake conversion to preserve independent-work facts, got %+v", intakeDecision)
	}

	restoredDecision := TurnDecision{}.WithRestoredIntakeState(intakeDecision)
	if !restoredDecision.HasIndependentWork {
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
