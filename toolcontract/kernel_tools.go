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

// A kernel tool is one the runtime always has, so intake never asks whether it
// is available. Which of them the model may name is a separate question, and the
// action schema answers it by enum-locking toolName to the exposed palette: a
// tool outside ModelFacingKernelToolNames cannot be called directly at all, and
// the model reaches one by describing the need to equip instead.
func ModelFacingKernelToolNames() []string {
	return []string{
		BashToolName,
		ReadToolName,
		WriteToolName,
		EditToolName,
		PlanToolName,
		EquipToolName,
	}
}

func RuntimeOnlyKernelToolNames() []string {
	return []string{
		FileDeliverToolName,
		SkillSearchToolName,
		FileReadToolName,
		FileDeleteToolName,
		FilePreviewToolName,
		ImageReadToolName,
		ConversationHistoryToolName,
	}
}

func KernelToolNames() []string {
	return append(ModelFacingKernelToolNames(), RuntimeOnlyKernelToolNames()...)
}

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
