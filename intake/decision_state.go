package intake

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const visibleContextMessageBudget = 8

type decisionState struct {
	Agent            decisionAgent            `json:"agent"`
	Company          decisionCompany          `json:"company"`
	ConversationType string                   `json:"conversationType,omitempty"`
	Now              string                   `json:"now,omitempty"`
	Context          []decisionContextMessage `json:"context,omitempty"`
	Messages         []decisionMessage        `json:"messages"`
	AvailableTools   []decisionTool           `json:"availableTools,omitempty"`
	ToolGuidance     string                   `json:"toolLikelihoodGuidance,omitempty"`
	PriorTask        *decisionPriorTask       `json:"priorTask,omitempty"`
	ScheduledRun     *decisionScheduledRun    `json:"scheduledRun,omitempty"`
	ActiveGoal       *decisionActiveGoal      `json:"activeGoal,omitempty"`
	ResponseLanguage string                   `json:"runtimeResponseLanguage,omitempty"`
}

type decisionAgent struct {
	Name    string `json:"name"`
	Mention string `json:"mention,omitempty"`
}

type decisionCompany struct {
	Name     string `json:"name,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type decisionContextMessage struct {
	At      string `json:"at,omitempty"`
	Speaker string `json:"speaker,omitempty"`
	Text    string `json:"text,omitempty"`
}

type decisionMessage struct {
	ID                string                               `json:"id"`
	Sender            string                               `json:"sender,omitempty"`
	Handle            string                               `json:"handle,omitempty"`
	At                string                               `json:"at,omitempty"`
	Text              string                               `json:"text"`
	Attachments       []agentcontract.IntakeAttachmentFact `json:"attachments,omitempty"`
	IsAttachmentsOnly bool                                 `json:"isAttachmentsOnly,omitempty"`
}

type decisionTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	providerID string
}

type decisionPriorTask struct {
	Prompt string `json:"prompt,omitempty"`
	Result string `json:"result,omitempty"`
}

type decisionScheduledRun struct {
	Name         string `json:"name,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Cadence      string `json:"cadence,omitempty"`
	OccurrenceAt string `json:"occurrenceAt,omitempty"`
}

type decisionActiveGoal struct {
	OriginalInstruction string   `json:"originalInstruction,omitempty"`
	CurrentObjective    string   `json:"currentObjective,omitempty"`
	Status              string   `json:"status,omitempty"`
	MissingInformation  []string `json:"missingInformation,omitempty"`
}

func buildDecisionState(request turnclassification.IntakeDecisionRequest, toolDescriptions []decisionTool) decisionState {
	state := decisionState{
		Agent:            decisionAgent{Name: request.AgentIdentity.DisplayName(), Mention: request.AgentIdentity.MentionExample()},
		Company:          decisionCompany{Name: request.Company.Name, TimeZone: request.Company.TimeZone},
		ConversationType: strings.TrimSpace(request.ConversationType),
		Now:              agentcontract.FormatContextTimestamp(request.EnvironmentNow, request.Company.TimeZone),
		Context:          decisionContextMessages(request),
		Messages:         decisionMessages(request),
		AvailableTools:   toolDescriptions,
		ToolGuidance:     toolLikelihoodGuidanceFor(toolDescriptions),
		ResponseLanguage: strings.TrimSpace(request.ResponseLanguage),
	}
	if hasPriorTask(request) {
		state.PriorTask = &decisionPriorTask{Prompt: request.PriorTask.Prompt, Result: request.PriorTask.Result}
	}
	if !request.ScheduledRun.IsEmpty() {
		state.ScheduledRun = &decisionScheduledRun{
			Name:         strings.TrimSpace(request.ScheduledRun.Name),
			Kind:         strings.TrimSpace(request.ScheduledRun.Kind),
			Cadence:      strings.TrimSpace(request.ScheduledRun.Cadence),
			OccurrenceAt: strings.TrimSpace(request.ScheduledRun.OccurrenceAt),
		}
	}
	if activeGoal, hasActiveGoal := decisionActiveGoalOf(request.ActiveGoal); hasActiveGoal {
		state.ActiveGoal = &activeGoal
	}
	return state
}

func decisionActiveGoalOf(activeGoal agentcontract.ActiveGoal) (decisionActiveGoal, bool) {
	goal := decisionActiveGoal{
		OriginalInstruction: strings.TrimSpace(activeGoal.OriginalInstruction),
		CurrentObjective:    strings.TrimSpace(activeGoal.CurrentObjective),
		Status:              strings.TrimSpace(string(activeGoal.Status)),
		MissingInformation:  activeGoal.MissingInformation,
	}
	if goal.OriginalInstruction == "" && goal.CurrentObjective == "" {
		return decisionActiveGoal{}, false
	}
	return goal, true
}

func decisionContextMessages(request turnclassification.IntakeDecisionRequest) []decisionContextMessage {
	messages := recentVisibleMessages(request.VisibleContext.Messages, visibleContextMessageBudget)
	contextMessages := make([]decisionContextMessage, 0, len(messages))
	for _, message := range messages {
		contextMessages = append(contextMessages, decisionContextMessage{
			At:      agentcontract.FormatContextTimestamp(message.SentAt, request.Company.TimeZone),
			Speaker: firstNonEmptyText(message.SpeakerCallingName, message.Speaker, message.SpeakerHandle, "unknown"),
			Text:    strings.TrimSpace(message.Text),
		})
	}
	return contextMessages
}

func decisionMessages(request turnclassification.IntakeDecisionRequest) []decisionMessage {
	messages := make([]decisionMessage, 0, len(request.Messages))
	for index, message := range request.Messages {
		messages = append(messages, decisionMessage{
			ID:                decisionMessageKey(index),
			Sender:            strings.TrimSpace(message.SenderName),
			Handle:            strings.TrimSpace(message.SenderHandle),
			At:                agentcontract.FormatContextTimestamp(message.SentAt, request.Company.TimeZone),
			Text:              strings.TrimSpace(message.Prompt),
			Attachments:       message.Attachments,
			IsAttachmentsOnly: message.IsAttachmentsOnly,
		})
	}
	return messages
}

const selectionToolDescriptionByteLimit = 1000

type describedDecisionTools struct {
	tools                   []decisionTool
	clippedDescriptionCount int
}

func decisionToolDescriptions(toolSet *toolcontract.ToolSet, callableToolNames []string) describedDecisionTools {
	descriptionByName := map[string]string{}
	providerByName := map[string]string{}
	if toolSet != nil {
		for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
			toolName := strings.TrimSpace(toolDefinition.Name)
			descriptionByName[toolName] = strings.TrimSpace(toolDefinition.Description)
			providerByName[toolName] = strings.TrimSpace(toolDefinition.ProviderID)
		}
	}
	described := describedDecisionTools{tools: make([]decisionTool, 0, len(callableToolNames))}
	for _, toolName := range callableToolNames {
		description := descriptionByName[toolName]
		clippedDescription := clipToolDescription(description)
		if clippedDescription != description {
			described.clippedDescriptionCount++
		}
		described.tools = append(described.tools, decisionTool{Name: toolName, Description: clippedDescription, providerID: providerByName[toolName]})
	}
	return described
}

func clipToolDescription(description string) string {
	if len(description) <= selectionToolDescriptionByteLimit {
		return description
	}
	clippedDescription := description[:selectionToolDescriptionByteLimit]
	for len(clippedDescription) > 0 && !utf8.ValidString(clippedDescription) {
		clippedDescription = clippedDescription[:len(clippedDescription)-1]
	}
	return clippedDescription
}

func decisionMessageKey(index int) string {
	return "m" + strconv.Itoa(index+1)
}

func hasActiveGoal(request turnclassification.IntakeDecisionRequest) bool {
	_, hasGoal := decisionActiveGoalOf(request.ActiveGoal)
	return hasGoal
}

func hasPriorTask(request turnclassification.IntakeDecisionRequest) bool {
	return strings.TrimSpace(request.PriorTask.Prompt) != ""
}

func recentVisibleMessages(messages []agentcontract.VisibleContextMessage, limit int) []agentcontract.VisibleContextMessage {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return messages[len(messages)-limit:]
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
