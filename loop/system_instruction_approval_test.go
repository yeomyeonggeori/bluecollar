package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"
)

func TestSystemInstructionRequiresConcreteReadResults(t *testing.T) {
	instruction := buildAgentSystemInstruction(AgentTurnRequest{ConversationID: "conversation-1", ToolSet: newTestToolSet([]string{toolcontract.AskInputToolName})})
	for _, expected := range []string{"final reply must state the concrete result facts", "status-only reply"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("expected system instruction to contain %q, got %s", expected, instruction)
		}
	}
}

func TestAWorkspaceTaskIsNotToldAboutMessengersItHasNone(t *testing.T) {
	workspaceOnly := buildAgentSystemInstruction(AgentTurnRequest{
		ToolSet: newTestToolSet([]string{toolcontract.TerminalRunToolName}),
	})

	for _, absent := range []string{"Bare mentions and banter", "Recipients:", "Delivery and artifacts", "Approvals and user input", "Skills:"} {
		if strings.Contains(workspaceOnly, absent) {
			t.Fatalf("a container with a shell and no conversation was carrying %q: the instruction ran to 12,753 bytes against a 136 byte task, and every byte of it competes with the work", absent)
		}
	}
	if !strings.Contains(workspaceOnly, "Failure recovery:") {
		t.Fatal("what applies to every task stays")
	}
}
