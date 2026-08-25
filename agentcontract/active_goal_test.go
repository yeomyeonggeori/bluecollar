package agentcontract

import (
	"strings"
	"testing"
)

func TestAGoalThatIsTheCurrentMessageDoesNotRepeatItWithSystemAuthority(t *testing.T) {
	instruction := "use the date command to find today, then sum this month's likes"
	described := ActiveGoalDescriptionForPrompt(ActiveGoal{GoalID: "goal-1", OriginalInstruction: instruction}, instruction)

	if strings.Contains(described, "use the date command") {
		t.Fatal("the request already speaks once as the user, and a system-side copy re-issues its every instruction with authority it was never given")
	}
	if !strings.Contains(described, "the current user message, verbatim") {
		t.Fatalf("the goal still has to say where its instruction lives: %s", described)
	}
	standalone := ActiveGoalDescriptionForPrompt(ActiveGoal{GoalID: "goal-1", OriginalInstruction: instruction}, "different follow-up message")
	if !strings.Contains(standalone, "use the date command") {
		t.Fatal("a goal from an earlier turn is not in the current message, and dropping it would lose the task")
	}
}
