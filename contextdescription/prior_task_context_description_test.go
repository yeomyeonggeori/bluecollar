package contextdescription

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestBluecollarTellsItsIntakeHowToReferToThePriorTask(t *testing.T) {
	description := PriorTaskContextDescription(agentcontract.PriorTaskContext{TaskRunID: "previous", Result: "Both tasks were not created."})

	if !strings.HasPrefix(description, agentcontract.PriorTaskDescription(agentcontract.PriorTaskContext{TaskRunID: "previous", Result: "Both tasks were not created."})) {
		t.Fatalf("the facts are described once, by the contract: %s", description)
	}
	if !strings.Contains(description, "priorTaskReference=outcome_recovery") {
		t.Fatalf("bluecollar's intake reads priorTaskReference, so its own prompt says how to set it: %s", description)
	}
}

func TestBluecollarSaysItsIntakeSuggestionsAreNotCommands(t *testing.T) {
	description := ActiveGoalDescriptionForPrompt(agentcontract.ActiveGoal{GoalID: "goal-1", OriginalInstruction: "earlier request"}, "follow-up")

	if !strings.Contains(description, "intake suggestions, not commands") {
		t.Fatalf("requiredNextTools come from bluecollar's own intake, so bluecollar qualifies them: %s", description)
	}
}
