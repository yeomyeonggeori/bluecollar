package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"
)

func TestSystemInstructionRequiresConcreteReadResults(t *testing.T) {
	instruction := buildAgentSystemInstruction(AgentTurnRequest{ConversationID: "conversation-1", ToolSet: newTestToolSet([]string{toolcontract.AskInputToolName})}, TurnOptions{}).Text()
	for _, expected := range []string{"final reply must state the concrete result facts", "status-only reply"} {
		if !strings.Contains(instruction, expected) {
			t.Fatalf("expected system instruction to contain %q, got %s", expected, instruction)
		}
	}
}

func TestAWorkspaceTaskIsNotToldAboutMessengersItHasNone(t *testing.T) {
	workspaceOnly := buildAgentSystemInstruction(AgentTurnRequest{
		ToolSet: newTestToolSet([]string{toolcontract.BashToolName}),
	}, TurnOptions{}).Text()

	for _, absent := range []string{"Bare mentions and banter", "Recipients:", "Delivery and artifacts", "Approvals and user input", "Skills:"} {
		if strings.Contains(workspaceOnly, absent) {
			t.Fatalf("a container with a shell and no conversation was carrying %q: the instruction ran to 12,753 bytes against a 136 byte task, and every byte of it competes with the work", absent)
		}
	}
	if strings.Contains(workspaceOnly, "Failure recovery:") {
		t.Fatal("the failure loop is carried by recovery guidance at the moment a call fails and by the gate that refuses a finish over unresolved failures, not by standing prose")
	}
}

func TestTheApprovalRuleIsStatedOnceAndOnlyWhereAGatedToolIsExposed(t *testing.T) {
	gated := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "event_delete", RequiresApproval: true},
		{Name: "task_delete", RequiresApproval: true},
	})
	ungated := newTestToolSet([]string{"event_list"})

	withGate := buildAgentSystemInstruction(AgentTurnRequest{ToolSet: gated}, TurnOptions{}).Text()
	withoutGate := buildAgentSystemInstruction(AgentTurnRequest{ToolSet: ungated}, TurnOptions{}).Text()

	if strings.Count(withGate, "Do not ask for that approval yourself") != 1 || !strings.Contains(withGate, toolcontract.ApprovalMarker) {
		t.Fatalf("expected the rule once, naming the marker the descriptions carry, got %s", withGate)
	}
	if strings.Contains(withoutGate, "approval yourself") {
		t.Fatalf("a task with no gated tool must not carry the approval rule, got %s", withoutGate)
	}
}
