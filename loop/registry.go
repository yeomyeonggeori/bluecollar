package loop

import (
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func ToolDefinitionRequiresSideEffectEvidence(toolDefinition toolcontract.ToolDefinition) bool {
	switch toolcontract.ToolDefinitionSideEffectClass(toolDefinition) {
	case "", toolcontract.ToolSideEffectNone, toolcontract.ToolSideEffectRead, toolcontract.ToolSideEffectComputation:
		return false
	default:
		return true
	}
}
