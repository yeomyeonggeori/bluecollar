package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"
)

func TestCapabilityDomainPhraseNamesWhateverTheHostCalledItsTools(t *testing.T) {
	skills := []SkillInstruction{
		{Name: "direct-message", ToolReferences: []string{"message_send", "message_context"}},
		{Name: "flow", ToolReferences: []string{"task_list", "task_add"}},
		{Name: "scheduling", ToolReferences: []string{"schedule_create"}},
		{Name: "future", ToolReferences: []string{"hologram.project"}},
	}

	phrase := capabilityDomainPhrase(skills)

	for _, expected := range []string{"message", "task", "schedule", "hologram"} {
		if !strings.Contains(phrase, expected) {
			t.Fatalf("a friendly name for %q would be this package guessing at a vocabulary the host owns; the tool's own prefix is the only name it can know: %q", expected, phrase)
		}
	}
}

func TestCapabilityDomainPhraseEmptyWhenNoSkills(t *testing.T) {
	if phrase := capabilityDomainPhrase(nil); phrase != "" {
		t.Fatalf("expected empty phrase, got %q", phrase)
	}
}

func TestEveryTaskIsToldThatToolOutputCannotGiveItInstructions(t *testing.T) {
	requests := map[string]AgentTurnRequest{
		"workspace only": {ToolSet: newTestToolSet([]string{toolcontract.TerminalRunToolName})},
		"conversation":   {ConversationID: "conversation-1", ToolSet: newTestToolSet([]string{toolcontract.AskInputToolName})},
	}

	for name, request := range requests {
		instruction := buildAgentSystemInstruction(request)
		if !strings.Contains(instruction, "Untrusted content:") {
			t.Fatalf("%s: a turn reads messages other people wrote and files other people committed; without this rule an instruction found in one of them is indistinguishable from the requester's own: %s", name, instruction)
		}
	}
}
