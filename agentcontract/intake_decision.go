package agentcontract

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type IntakeAttachmentFact struct {
	Kind        string `json:"kind"`
	MimeType    string `json:"mimeType,omitempty"`
	FileName    string `json:"fileName,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	Description string `json:"description,omitempty"`
}

type IntakeDecisionMessage struct {
	MessageID         string
	Prompt            string
	SenderName        string
	SenderHandle      string
	BotMentioned      bool
	SentAt            time.Time
	InputParts        []AgentPart
	Attachments       []IntakeAttachmentFact
	IsAttachmentsOnly bool
}

type IntakeDecisionRequest struct {
	Messages               []IntakeDecisionMessage
	ConversationType       string
	VisibleContext         VisibleContext
	AgentIdentity          AgentIdentity
	Company                CompanyContext
	ActiveTask             ActiveTaskContext
	IsTaskRecentlyFinished bool
	PendingConfirmation    PendingConfirmationContext
	PendingChoice          PendingChoiceContext
	PriorTask              PriorTaskContext
	ScheduledRun           ScheduledRunContext
	ActiveGoal             ActiveGoal
	ToolSet                *toolcontract.ToolSet
	CallableToolNames      []string
	ResponseLanguage       string
	AllowGiveUp            bool
	AllowGiveUpReason      string
	EnvironmentNow         time.Time
}

type IntakeMessageDecision struct {
	MessageID              string                 `json:"messageID,omitempty"`
	Addressing             AddressingDecision     `json:"addressing"`
	ReactionProbability    float64                `json:"reactionProbability"`
	ReactionDraw           float64                `json:"reactionDraw"`
	RelatesToActiveTask    bool                   `json:"relatesToActiveTask,omitempty"`
	HasRelatesToActiveTask bool                   `json:"hasRelatesToActiveTask,omitempty"`
	TurnFields             TurnDecision           `json:"turnFields"`
	Attachments            []IntakeAttachmentFact `json:"attachments,omitempty"`
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

func (decision IntakeMessageDecision) AttachmentDescriptions() []string {
	descriptions := []string{}
	for _, attachment := range decision.Attachments {
		if trimmedDescription := strings.TrimSpace(attachment.Description); trimmedDescription != "" {
			descriptions = append(descriptions, trimmedDescription)
		}
	}
	return descriptions
}

func AttachmentFactsFromParts(parts []AgentPart) []IntakeAttachmentFact {
	facts := []IntakeAttachmentFact{}
	for _, part := range parts {
		switch part.Type {
		case AgentPartTypeImage:
			if part.Image == nil {
				continue
			}
			facts = append(facts, IntakeAttachmentFact{Kind: "image", MimeType: part.Image.MimeType, FileName: part.Image.Filename})
		case AgentPartTypeFile:
			if part.File == nil {
				continue
			}
			facts = append(facts, IntakeAttachmentFact{Kind: "file", MimeType: part.File.ContentType, FileName: part.File.Filename, SizeBytes: part.File.SizeBytes})
		}
	}
	return facts
}

func ImagePartsOf(parts []AgentPart) []AgentPart {
	imageParts := []AgentPart{}
	for _, part := range parts {
		if part.Type == AgentPartTypeImage && part.Image != nil && strings.TrimSpace(part.Image.DataBase64) != "" {
			imageParts = append(imageParts, part)
		}
	}
	return imageParts
}

func AgentImageToMessagePart(part AgentPart) model.MessagePart {
	if part.Image == nil {
		return model.MessagePart{}
	}
	dataBase64 := strings.TrimSpace(part.Image.DataBase64)
	if dataBase64 == "" {
		return model.MessagePart{}
	}
	if _, errorValue := base64.StdEncoding.DecodeString(dataBase64); errorValue != nil {
		return model.MessagePart{}
	}
	return model.MessagePart{
		Type:       "image",
		MimeType:   strings.TrimSpace(part.Image.MimeType),
		DataBase64: dataBase64,
		Text:       strings.TrimSpace(part.Image.Filename),
	}
}

func ImageMessageParts(parts []AgentPart) []model.MessagePart {
	messageParts := []model.MessagePart{}
	for _, part := range ImagePartsOf(parts) {
		messagePart := AgentImageToMessagePart(part)
		if strings.TrimSpace(messagePart.DataBase64) == "" || strings.TrimSpace(messagePart.MimeType) == "" {
			continue
		}
		messageParts = append(messageParts, messagePart)
	}
	return messageParts
}

const (
	IntakeQuestionTarget              = "target"
	IntakeQuestionShouldRespond       = "shouldRespond"
	IntakeQuestionReaction            = "reaction"
	IntakeQuestionReactionEmoji       = "reactionEmoji"
	IntakeQuestionDuty                = "duty"
	IntakeQuestionRelatesToActiveTask = "relatesToActiveTask"
	IntakeQuestionRoute               = "route"
	IntakeQuestionNeedsTool           = "needsTool"
	IntakeQuestionTaskShape           = "taskShape"
	IntakeQuestionLevel               = "level"
	IntakeQuestionDeliverableKind     = "deliverableKind"
	IntakeQuestionResponseLanguage    = "responseLanguage"
	IntakeQuestionPriorTaskReference  = "priorTaskReference"
	IntakeQuestionApproval            = "approval"
	IntakeQuestionBusyRoute           = "busyRoute"
	IntakeQuestionChoice              = "choice"

	IntakeQuestionPrefixFormat = "format."
	IntakeQuestionPrefixTool   = "tool."
	IntakeQuestionPrefixChoice = "choice."

	IntakeReactionOptionNone  = "none"
	IntakeReactionOptionReact = "react"
	IntakeDutyOptionNone      = "none"
	IntakeChoiceOptionNone    = "none_of_these"
)
