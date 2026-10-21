package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestEveryWriterOfATasksLastMessageIsToldNothingRunsAfterIt(t *testing.T) {
	request := AgentTurnRequest{Prompt: "완료 업무로 추가해줘", ToolSet: newTestToolSet([]string{toolcontract.BashToolName})}
	unmetChange := expectedChange{Change: "task created", Asked: "완료 업무로 추가해줘"}
	writers := map[string]string{
		"the turn that chooses reply or fail":          systemInstructionFor(TurnOptions{}, request).Text(),
		"the rewrite of a reply that claimed too much": buildFinishReplyRewritePrompt(request, "추가했어요.", finishReplyRewrite{unmet: []expectedChange{unmetChange}}),
		"the last step after recovery ran out":         terminalNoToolsInstruction(nil, defaultRecoveryBudget(), ""),
	}
	for writer, prompt := range writers {
		if !strings.Contains(prompt, agentcontract.NothingRunsAfterTheLastWord) {
			t.Errorf("%s is not told that nothing runs after the task's last message", writer)
		}
	}
}
