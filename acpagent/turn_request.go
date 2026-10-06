package acpagent

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func turnRequestOfMeta(promptMeta map[string]any) (agentcontract.AgentTurnRequest, bool) {
	value, isPresent := promptMeta[TurnRequestMetaKey]
	if !isPresent {
		return agentcontract.AgentTurnRequest{}, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return agentcontract.AgentTurnRequest{}, false
	}
	turnRequest := agentcontract.AgentTurnRequest{}
	return turnRequest, json.Unmarshal(encoded, &turnRequest) == nil
}

func instructionBundleOfMeta(promptMeta map[string]any) (agentcontract.InstructionBundle, bool) {
	value, isPresent := promptMeta[InstructionBundleMetaKey]
	if !isPresent {
		return agentcontract.InstructionBundle{}, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return agentcontract.InstructionBundle{}, false
	}
	bundle := agentcontract.InstructionBundle{}
	return bundle, json.Unmarshal(encoded, &bundle) == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
