package toolexposure

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const MaxExtensionCallableToolCount = 15

const ToolExposureGroupsRankedBelowTheLikelyTools = 3

const MaxLikelyToolCount = MaxExtensionCallableToolCount - ToolExposureGroupsRankedBelowTheLikelyTools

const ToolNamesOnePlanStepIsExpectedToNeed = 5

const MaxLikelyToolCountForOnePlanStep = min(ToolNamesOnePlanStepIsExpectedToNeed, MaxLikelyToolCount)

func ToolNamesMatch(leftToolName string, rightToolName string) bool {
	return strings.TrimSpace(leftToolName) == strings.TrimSpace(rightToolName)
}

func IsArtifactDeliveryTool(toolName string) bool {
	return strings.TrimSpace(toolName) == toolcontract.FileDeliverToolName
}
