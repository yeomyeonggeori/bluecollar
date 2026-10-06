package turnclassification

import (
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type IntakeDecisionMessage struct {
	MessageID         string
	Prompt            string
	SenderName        string
	SenderHandle      string
	SentAt            time.Time
	InputParts        []agentcontract.AgentPart
	Attachments       []agentcontract.IntakeAttachmentFact
	IsAttachmentsOnly bool
}

type IntakeDecisionRequest struct {
	Messages          []IntakeDecisionMessage
	ConversationType  string
	VisibleContext    agentcontract.VisibleContext
	AgentIdentity     agentcontract.AgentIdentity
	Company           agentcontract.CompanyContext
	PriorTask         agentcontract.PriorTaskContext
	ScheduledRun      agentcontract.ScheduledRunContext
	ActiveGoal        agentcontract.ActiveGoal
	ToolSet           *toolcontract.ToolSet
	CallableToolNames []string
	ResponseLanguage  string
	AllowGiveUp       bool
	AllowGiveUpReason string
	EnvironmentNow    time.Time
}

type IntakeMessageDecision struct {
	MessageID   string                               `json:"messageID,omitempty"`
	TurnFields  TurnDecision                         `json:"turnFields"`
	Attachments []agentcontract.IntakeAttachmentFact `json:"attachments,omitempty"`
}

func (decision IntakeMessageDecision) AttachmentDescriptions() []string {
	descriptions := []string{}
	for _, attachment := range decision.Attachments {
		if trimmedDescription := strings.TrimSpace(attachment.Description); trimmedDescription != "" {
			descriptions = append(descriptions, trimmedDescription)
		}
	}
	return descriptions
}

type IntakeDecisions struct {
	Messages []IntakeMessageDecision
}

func (decisions IntakeDecisions) ForMessage(messageID string) (IntakeMessageDecision, bool) {
	trimmedMessageID := strings.TrimSpace(messageID)
	for _, decision := range decisions.Messages {
		if decision.MessageID == trimmedMessageID {
			return decision, true
		}
	}
	return IntakeMessageDecision{}, false
}
