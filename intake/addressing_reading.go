package intake

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func normalizeAddressingReactionEmoji(name string) string {
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	for _, allowedName := range agentcontract.ReactionEmojiNames {
		if allowedName == normalizedName {
			return allowedName
		}
	}
	return ""
}

func normalizedDutyConfidence(confidence float64) float64 {
	if confidence < 0 {
		return 0
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}

func recentVisibleMessages(messages []agentcontract.VisibleContextMessage, limit int) []agentcontract.VisibleContextMessage {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return messages[len(messages)-limit:]
}

func firstNonEmptyAddressingText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
