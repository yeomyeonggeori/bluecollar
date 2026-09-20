package toolcontract

import "strings"

const (
	ShellToolName               = "shell"
	ReadToolName                = "read"
	AskInputToolName            = "ask_input"
	AskConfirmToolName          = "ask_confirm"
	FileDeliverToolName         = "file_deliver"
	AskChoiceToolName           = "ask_choice"
	SkillSearchToolName         = "skill_search"
	FileReadToolName            = "file_read"
	FileWriteToolName           = "file_write"
	FileDeleteToolName          = "file_delete"
	FileEditToolName            = "file_edit"
	FilePreviewToolName         = "file_preview"
	ImageReadToolName           = "image_read"
	ConversationHistoryToolName = "conversation_history"
	PlanToolName                = "plan"
	FindToolsToolName           = "find_tools"
)

const MaxExtensionCallableToolCount = 15

const ToolExposureGroupsRankedBelowTheLikelyTools = 3

const MaxLikelyToolCount = MaxExtensionCallableToolCount - ToolExposureGroupsRankedBelowTheLikelyTools

const ToolNamesOnePlanStepIsExpectedToNeed = 5

const MaxLikelyToolCountForOnePlanStep = min(ToolNamesOnePlanStepIsExpectedToNeed, MaxLikelyToolCount)

func KernelToolNames() []string {
	return []string{
		ShellToolName,
		ReadToolName,
		FileDeliverToolName,
		SkillSearchToolName,
		FileReadToolName,
		FileWriteToolName,
		FileDeleteToolName,
		FileEditToolName,
		FilePreviewToolName,
		ImageReadToolName,
		ConversationHistoryToolName,
		PlanToolName,
		FindToolsToolName,
	}
}

func IsKernelToolName(toolName string) bool {
	for _, kernelToolName := range KernelToolNames() {
		if strings.TrimSpace(toolName) == kernelToolName {
			return true
		}
	}
	return false
}

func ToolNamesMatch(leftToolName string, rightToolName string) bool {
	return strings.TrimSpace(leftToolName) == strings.TrimSpace(rightToolName)
}

func IsArtifactDeliveryTool(toolName string) bool {
	return strings.TrimSpace(toolName) == FileDeliverToolName
}
