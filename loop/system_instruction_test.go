package loop

import (
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
