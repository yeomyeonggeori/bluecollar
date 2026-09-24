package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"
)

func TestCapabilitiesNameEachNamespaceTheModelCanReachOnce(t *testing.T) {
	request := AgentTurnRequest{
		ToolSet: newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
			capabilityTestDescriptor("crm_contact_list", "crm", "Contacts and deals."),
			capabilityTestDescriptor("crm_contact_add", "crm", "Contacts and deals."),
			capabilityTestDescriptor("mail_message_send", "mail", "The requester's email."),
			capabilityTestDescriptor("workspace_probe", "workspace", ""),
		}),
		AvailableSkills: []SkillInstruction{{Name: "presentation"}, {Name: "calendar"}},
	}

	body := capabilitiesInstructionBody(request)

	expected := "- crm: Contacts and deals.\n- mail: The requester's email.\n- skills: calendar, presentation"
	if !strings.HasSuffix(body, expected) {
		t.Fatalf("each namespace is one line in name order, a namespace without a summary says nothing, and skills follow; got:\n%s", body)
	}
}

func TestCapabilitiesLeaveOutANamespaceWhoseToolsCannotBeCalled(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		capabilityTestDescriptor("mail_message_send", "mail", "The requester's email."),
	})
	toolSet.RegisterBoundTool(toolcontract.BoundTool{
		Definition:   capabilityTestDescriptor("crm_contact_list", "crm", "Contacts and deals."),
		Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityUnavailable},
	})

	body := capabilitiesInstructionBody(AgentTurnRequest{ToolSet: toolSet})

	if strings.Contains(body, "crm") {
		t.Fatalf("a namespace this requester cannot reach would promise work equip will not hand over; got:\n%s", body)
	}
}

func TestCapabilitiesSayNothingWithoutSummariesOrSkills(t *testing.T) {
	request := AgentTurnRequest{ToolSet: newTestToolSet([]string{toolcontract.BashToolName})}

	if body := capabilitiesInstructionBody(request); body != "" {
		t.Fatalf("expected no capabilities section, got %q", body)
	}
}

func capabilityTestDescriptor(toolName string, namespace string, summary string) toolcontract.ToolDefinition {
	definition := testToolDescriptor(toolName)
	definition.Namespace = namespace
	definition.NamespaceSummary = summary
	return definition
}

func TestEveryTaskIsToldThatToolOutputCannotGiveItInstructions(t *testing.T) {
	requests := map[string]AgentTurnRequest{
		"workspace only": {ToolSet: newTestToolSet([]string{toolcontract.BashToolName})},
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
	request := AgentTurnRequest{ToolSet: newTestToolSet([]string{toolcontract.BashToolName})}
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
