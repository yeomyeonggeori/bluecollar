package toolcontract

import "strings"

const (
	BashToolName                = "bash"
	ReadToolName                = "read"
	AskInputToolName            = "ask_input"
	AskConfirmToolName          = "ask_confirm"
	FileDeliverToolName         = "file_deliver"
	AskChoiceToolName           = "ask_choice"
	SkillSearchToolName         = "skill_search"
	FileReadToolName            = "file_read"
	WriteToolName               = "write"
	FileDeleteToolName          = "file_delete"
	EditToolName                = "edit"
	FilePreviewToolName         = "file_preview"
	ImageReadToolName           = "image_read"
	ConversationHistoryToolName = "conversation_history"
	PlanToolName                = "plan"
	EquipToolName               = "equip"
)

const MaxExtensionCallableToolCount = 15

const ToolExposureGroupsRankedBelowTheLikelyTools = 3

const MaxLikelyToolCount = MaxExtensionCallableToolCount - ToolExposureGroupsRankedBelowTheLikelyTools

const ToolNamesOnePlanStepIsExpectedToNeed = 5

const MaxLikelyToolCountForOnePlanStep = min(ToolNamesOnePlanStepIsExpectedToNeed, MaxLikelyToolCount)

var currentNameByFormerKernelToolName = map[string]string{
	"shell":      BashToolName,
	"file_write": WriteToolName,
	"file_edit":  EditToolName,
	"find_tools": EquipToolName,
}

func CanonicalToolName(recordedToolName string) string {
	trimmedToolName := strings.TrimSpace(recordedToolName)
	if currentToolName, wasRenamed := currentNameByFormerKernelToolName[trimmedToolName]; wasRenamed {
		return currentToolName
	}
	return trimmedToolName
}

func ToolNamesMatch(leftToolName string, rightToolName string) bool {
	return strings.TrimSpace(leftToolName) == strings.TrimSpace(rightToolName)
}

func IsArtifactDeliveryTool(toolName string) bool {
	return strings.TrimSpace(toolName) == FileDeliverToolName
}
