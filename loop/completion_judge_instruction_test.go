package loop

import (
	"strings"
	"testing"
)

func TestTheJudgeReadsABareConfirmationThroughTheIntakeReading(t *testing.T) {
	request := AgentTurnRequest{
		Prompt: "ㅇ",
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "ㅇ",
			CurrentObjective:    "9/15 창업대회 일정을 삭제하고 명함 제작 업무를 추가한다.",
			KnownContext:        []string{"The user approved the pending action in the latest message: ㅇ"},
		},
	}

	instruction := completionJudgeOriginalInstruction(request)

	for _, expected := range []string{"ㅇ", "Read at intake as: 9/15 창업대회 일정을 삭제하고", "Known: The user approved the pending action"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("the judge must see what the confirmation confirmed, got %q", instruction)
		}
	}
	if !strings.Contains(completionJudgeInstruction(), "bare confirmation") {
		t.Fatal("the judge is told that a bare confirmation is read through the intake reading")
	}
}
