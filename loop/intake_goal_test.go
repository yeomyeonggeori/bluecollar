package loop

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestIntakeGoalKeepsExpectedOutcomeForContinuation(t *testing.T) {
	decision := IntakeDecision{Reason: "Revise the task labels", ExpectedResults: []ExpectedResult{{
		ID: "result-1", Type: "message", Description: "Report updated task labels", Required: true,
	}}}
	goal := activeGoalFromIntakeOnly("task-1", AgentRequest{Prompt: "Review task labels"}, decision, agentcontract.TaskStatusBlocked)
	if len(goal.OutcomeContract.ExpectedResults) != 1 || goal.OutcomeContract.ExpectedResults[0].Description != decision.ExpectedResults[0].Description {
		t.Fatalf("intake outcome must survive a pause: %+v", goal)
	}
}

func TestIntakeGoalPreservesExistingRunEvidence(t *testing.T) {
	request := AgentRequest{
		Prompt: "Continue",
		ActiveGoal: ActiveGoal{
			TaskRunID: "task-1", OriginalInstruction: "Create the requested document",
			KnownContext:    []string{"The draft is saved"},
			OutcomeContract: OutcomeContract{RequiredAttachmentSuffixes: []string{".docx"}},
		},
	}
	goal := activeGoalFromIntakeOnly("task-1", request, IntakeDecision{Reason: "Deliver the draft"}, agentcontract.TaskStatusBlocked)
	if goal.OriginalInstruction != request.ActiveGoal.OriginalInstruction || len(goal.KnownContext) != 1 || len(goal.OutcomeContract.RequiredAttachmentSuffixes) != 1 {
		t.Fatalf("a paused continuation must retain recorded context: %+v", goal)
	}
	newGoal := activeGoalFromIntakeOnly("task-2", request, IntakeDecision{Reason: "A new request"}, agentcontract.TaskStatusBlocked)
	if len(newGoal.KnownContext) != 0 || len(newGoal.OutcomeContract.RequiredAttachmentSuffixes) != 0 {
		t.Fatalf("new task must not inherit an unrelated goal: %+v", newGoal)
	}
	request.PrecomputedTurnDecision = &TurnDecision{Route: TurnRouteReviseTask}
	revisedGoal := activeGoalFromIntakeOnly("task-1", request, IntakeDecision{Reason: "Answer in chat instead"}, agentcontract.TaskStatusBlocked)
	if len(revisedGoal.OutcomeContract.RequiredAttachmentSuffixes) != 0 {
		t.Fatalf("user correction must replace the old output requirement: %+v", revisedGoal)
	}
}
