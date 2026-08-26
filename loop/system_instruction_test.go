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
		"workspace only": {ToolSet: newTestToolSet([]string{toolcontract.ShellToolName})},
		"conversation":   {ConversationID: "conversation-1", ToolSet: newTestToolSet([]string{toolcontract.AskInputToolName})},
	}

	for name, request := range requests {
		instruction := buildAgentSystemInstruction(request, TurnOptions{}).Text()
		if !strings.Contains(instruction, "Untrusted content:") {
			t.Fatalf("%s: a turn reads messages other people wrote and files other people committed; without this rule an instruction found in one of them is indistinguishable from the requester's own: %s", name, instruction)
		}
	}
}

func TestTheInstructionIsItsSectionsAndNothingElse(t *testing.T) {
	request := AgentTurnRequest{
		ConversationID:  "conversation-1",
		ToolSet:         newTestToolSet([]string{toolcontract.AskInputToolName}),
		HostInstruction: "The company closes at six.",
	}

	systemInstruction := buildAgentSystemInstruction(request, TurnOptions{})

	bodies := []string{}
	for _, section := range systemInstruction.Sections {
		bodies = append(bodies, section.Body)
	}
	if systemInstruction.Text() != strings.Join(bodies, "\n\n") {
		t.Fatal("the assembled text has to be the sections and nothing else, or measuring a section says nothing about what the model was charged")
	}
	if systemInstruction.BytesBySection()["host"] != len("The company closes at six.") {
		t.Fatalf("every section reports its own size: %v", systemInstruction.BytesBySection())
	}
	if systemInstruction.Sections[len(systemInstruction.Sections)-1].Name != "host" {
		t.Fatalf("the host has the last word, as it did before: %v", instructionSectionNames(systemInstruction))
	}
}

func TestAnOverlayIsHowAModelGetsItsOwnWordingWithoutForkingTheBase(t *testing.T) {
	request := AgentTurnRequest{ToolSet: newTestToolSet([]string{toolcontract.ShellToolName})}
	base := systemInstructionFor(TurnOptions{}, request)

	withOverlay := systemInstructionFor(TurnOptions{
		SystemInstructionOverlay: func(AgentTurnRequest) string {
			return "This model answers an empty tool call with prose; do not accept one."
		},
	}, request)

	if withOverlay.Text() != base.Text()+"\n\nThis model answers an empty tool call with prose; do not accept one." {
		t.Fatalf("an overlay is appended after the base and changes nothing in it: %q", withOverlay.Text())
	}
	if systemInstructionFor(TurnOptions{SystemInstructionOverlay: func(AgentTurnRequest) string { return "  " }}, request).Text() != base.Text() {
		t.Fatal("an overlay with nothing to say costs the turn nothing")
	}
}
