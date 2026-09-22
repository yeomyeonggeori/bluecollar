package agentcontract

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func NormalizeIntakeOptions(options IntakeOptions) IntakeOptions {
	if NormalizeTaskLevel(string(options.DefaultTaskLevel)) == "" {
		options.DefaultTaskLevel = TaskLevelLow
	}
	options.SkillTaskLevelFloor = NormalizeTaskLevel(string(options.SkillTaskLevelFloor))
	return options
}

func NormalizeReactionEmojiName(emojiName string) string {
	normalizedEmojiName := strings.Trim(strings.TrimSpace(emojiName), ":")
	if normalizedEmojiName == "" {
		return DefaultReactionEmojiName
	}
	normalizedEmojiName = strings.ToLower(normalizedEmojiName)
	for _, allowedEmojiName := range ReactionEmojiNames {
		if normalizedEmojiName == allowedEmojiName {
			return normalizedEmojiName
		}
	}
	return DefaultReactionEmojiName
}

var RequestedOutputFormatNames = []string{"html", "pptx", "pdf", "txt", "docx", "xlsx", "csv", "json"}

func IsRequestedOutputFormatName(format string) bool {
	for _, formatName := range RequestedOutputFormatNames {
		if formatName == format {
			return true
		}
	}
	return false
}

func NormalizeRequestedOutputFormats(formats []string) []string {
	normalizedFormats := []string{}
	seenFormat := map[string]bool{}
	for _, format := range formats {
		normalizedFormat := strings.ToLower(strings.TrimSpace(format))
		if !IsRequestedOutputFormatName(normalizedFormat) || seenFormat[normalizedFormat] {
			continue
		}
		seenFormat[normalizedFormat] = true
		normalizedFormats = append(normalizedFormats, normalizedFormat)
	}
	return normalizedFormats
}

func RegisteredToolNamesOnly(toolRegistry *toolcontract.ToolSet, toolNames []string) []string {
	if toolRegistry == nil || len(toolNames) == 0 {
		return nil
	}
	registeredToolNames := []string{}
	for _, toolName := range toolcontract.AppendUniqueStrings([]string{}, toolNames...) {
		trimmedToolName := strings.TrimSpace(toolName)
		if toolRegistry.IsAllowed(trimmedToolName) || RequiredEvidenceToolCanBeSatisfied(toolRegistry, trimmedToolName) {
			registeredToolNames = toolcontract.AppendUniqueStrings(registeredToolNames, trimmedToolName)
		}
	}
	return registeredToolNames
}

func HasAllTools(toolRegistry *toolcontract.ToolSet, toolNames []string) bool {
	if toolRegistry == nil {
		return false
	}
	availableToolNames := map[string]bool{}
	for _, toolName := range toolRegistry.ListToolNames() {
		availableToolNames[toolName] = true
	}
	for _, toolName := range toolNames {
		if !availableToolNames[toolName] {
			return false
		}
	}
	return true
}

func HasTool(toolRegistry *toolcontract.ToolSet, toolName string) bool {
	if toolRegistry == nil {
		return false
	}
	for _, availableToolName := range toolRegistry.ListToolNames() {
		if availableToolName == toolName {
			return true
		}
	}
	return false
}

func RequiredEvidenceToolCanBeSatisfied(toolSet *toolcontract.ToolSet, toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" || toolSet == nil || !toolSet.IsRegistered(trimmedToolName) {
		return false
	}
	if toolSet.IsAllowed(trimmedToolName) {
		return true
	}
	return !toolSet.IsBuiltInTool(trimmedToolName) && toolSet.CanExpose(trimmedToolName)
}
