package loop

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func skillSelectionPrompt(request AgentRequest) string {
	return strings.TrimSpace(request.Prompt)
}

func buildCompactSkillIndexPrompt(skillInstructions []SkillInstruction) string {
	if len(skillInstructions) == 0 {
		return ""
	}
	lines := []string{"Available skill index. These are capability references, not mandatory workflows:"}
	for _, skillInstruction := range skillInstructions {
		lines = append(lines, "- "+compactSkillIndexLine(skillInstruction))
	}
	return strings.Join(lines, "\n")
}

func compactSkillIndexLine(skillInstruction SkillInstruction) string {
	parts := []string{skillInstruction.Name}
	if text := strings.TrimSpace(skillInstruction.Description); text != "" {
		parts = append(parts, strings.TrimSpace(text))
	}
	return strings.Join(parts, ": ")
}

func skillInstructionsWhoseToolsAreCallable(toolSet *toolcontract.ToolSet, skillInstructions []SkillInstruction) []SkillInstruction {
	callable := []SkillInstruction{}
	for _, skillInstruction := range skillInstructions {
		if len(skillInstruction.ToolReferences) == 0 || anyToolIsRegistered(toolSet, skillInstruction.ToolReferences) {
			callable = append(callable, skillInstruction)
		}
	}
	return callable
}

func anyToolIsRegistered(toolSet *toolcontract.ToolSet, toolNames []string) bool {
	if toolSet == nil {
		return false
	}
	for _, toolName := range toolNames {
		if _, isRegistered := toolSet.ToolDefinition(strings.TrimSpace(toolName)); isRegistered {
			return true
		}
	}
	return false
}

func buildSelectedSkillInstructionPrompt(skillInstructions []SkillInstruction) string {
	skills := []string{}
	for _, skillInstruction := range skillInstructions {
		if strings.TrimSpace(skillInstruction.Prompt) != "" {
			skills = append(skills, selectedSkillInstructionPrompt(skillInstruction))
		}
	}
	if len(skills) == 0 {
		return ""
	}
	parts := []string{
		"Available skill references:",
		"These skills/tools are available if they fit the user's current goal. They are not mandatory. Do not change the requested output type to match a skill.",
		"Multiple skills may be selected at once, but only use the ones this specific request actually needs. Mentioning a topic (e.g. email, calendar, browsing) is not the same as being asked to act on it — ignore skills whose subject matter is not the actual task.",
	}
	return strings.Join(append(parts, skills...), "\n\n")
}

func selectedSkillInstructionPrompt(skillInstruction SkillInstruction) string {
	return strings.Join([]string{
		"Skill: " + strings.TrimSpace(skillInstruction.Name),
		"Source: " + selectedSkillSourcePath(skillInstruction),
		"Resolve relative scripts, references, and assets from the source directory.",
		strings.TrimSpace(skillInstruction.Prompt),
	}, "\n")
}

func selectedSkillSourcePath(skillInstruction SkillInstruction) string {
	sourcePath := strings.TrimSpace(strings.ReplaceAll(skillInstruction.Source.Path, "\\", "/"))
	if sourcePath == "" {
		return ""
	}
	if strings.HasSuffix(sourcePath, "/SKILL.md") {
		return sourcePath
	}
	return strings.TrimSuffix(sourcePath, "/") + "/SKILL.md"
}

func nonEmptyStrings(values []string) []string {
	result := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}
