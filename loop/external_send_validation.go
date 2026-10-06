package loop

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func previousSuccessfulExternalSend(toolSet *toolcontract.ToolSet, observations []turnObservation, toolName string, toolInput json.RawMessage) (turnObservation, bool) {
	if !isSendEvidenceTool(toolSet, toolName) {
		return turnObservation{}, false
	}
	currentRecipient := sendRecipientKey(toolInput)
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "continue" || observation.Failed() {
			continue
		}
		if strings.TrimSpace(observation.Tool) != strings.TrimSpace(toolName) {
			continue
		}
		if currentRecipient == "" || currentRecipient == observationSendRecipientKey(observation) {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func sendRecipientKey(toolInput json.RawMessage) string {
	var document struct {
		TargetType     string `json:"targetType"`
		PersonHint     string `json:"personHint"`
		ChannelName    string `json:"channelName"`
		ConversationID string `json:"conversationID"`
	}
	if len(toolInput) == 0 || json.Unmarshal(toolInput, &document) != nil {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(strings.Join([]string{document.TargetType, document.PersonHint, document.ChannelName, document.ConversationID}, "|")))
	if strings.Trim(key, "|") == "" {
		return ""
	}
	return key
}

func observationSendRecipientKey(observation turnObservation) string {
	_, canonicalInput, found := strings.Cut(observation.ToolInputKey, "\x00")
	if !found {
		return ""
	}
	return sendRecipientKey(json.RawMessage(canonicalInput))
}

func requiredEvidenceContains(requiredEvidenceTools []string, expectedToolName string) bool {
	for _, toolName := range requiredEvidenceTools {
		if toolexposure.ToolNamesMatch(toolName, expectedToolName) {
			return true
		}
	}
	return false
}

func unrequestedPlatformMessageSendObservation(request AgentTurnRequest, actionDocument turnActionDocument, observationID string) (turnObservation, bool) {
	toolName := strings.TrimSpace(actionDocument.ToolName)
	if !isSendEvidenceTool(request.ToolSet, toolName) {
		return turnObservation{}, false
	}
	if sendTargetsCurrentConversation(actionDocument.ToolInput) {
		return turnObservation{}, false
	}
	if requestRequiresExternalSendTool(request, toolName) {
		return turnObservation{}, false
	}
	message := toolName + " requires an exact external-send outcome contract. Answer in the current conversation with a reply instead."
	return newFailureObservation(observationID, "policy", toolName, message, toolcontract.FailurePolicyBlocked, toolcontract.FailureCodes.PolicyBlocked, "policy"), true
}

func sendTargetsCurrentConversation(toolInput json.RawMessage) bool {
	var document struct {
		TargetType string `json:"targetType"`
	}
	if len(toolInput) == 0 || json.Unmarshal(toolInput, &document) != nil {
		return false
	}
	switch strings.TrimSpace(document.TargetType) {
	case "currentThread", "currentChannel":
		return true
	default:
		return false
	}
}

func requestRequiresExternalSendTool(request AgentTurnRequest, toolName string) bool {
	if requiredEvidenceContains(request.RequiredEvidenceTools, toolName) {
		return true
	}
	for _, requiredToolName := range outcomeContractRequiredToolNames(request.OutcomeContract) {
		if toolexposure.ToolNamesMatch(requiredToolName, toolName) {
			return true
		}
	}
	for _, requiredToolName := range outcomeContractRequiredToolNames(request.ActiveGoal.OutcomeContract) {
		if toolexposure.ToolNamesMatch(requiredToolName, toolName) {
			return true
		}
	}
	return false
}
