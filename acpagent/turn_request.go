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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
