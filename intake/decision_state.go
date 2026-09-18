package intake

import (
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const visibleContextMessageBudget = 8

type decisionState struct {
	Agent               decisionAgent            `json:"agent"`
	Company             decisionCompany          `json:"company"`
	ConversationType    string                   `json:"conversationType,omitempty"`
	Now                 string                   `json:"now,omitempty"`
	Context             []decisionContextMessage `json:"context,omitempty"`
	Messages            []decisionMessage        `json:"messages"`
	StandingDuties      []decisionDuty           `json:"standingDuties,omitempty"`
	AvailableTools      []decisionTool           `json:"availableTools,omitempty"`
	ActiveTask          *decisionActiveTask      `json:"activeTask,omitempty"`
	RecentlyFinished    *decisionActiveTask      `json:"recentlyFinishedTask,omitempty"`
	PendingConfirmation *decisionPending         `json:"pendingConfirmation,omitempty"`
	PendingChoice       *decisionPendingChoice   `json:"pendingChoice,omitempty"`
	PriorTask           *decisionPriorTask       `json:"priorTask,omitempty"`
	GiveUpAllowance     string                   `json:"giveUpAllowedBecause,omitempty"`
	ResponseLanguage    string                   `json:"runtimeResponseLanguage,omitempty"`
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
	BotMentioned      bool                                 `json:"botMentioned"`
	At                string                               `json:"at,omitempty"`
	Text              string                               `json:"text"`
	Attachments       []agentcontract.IntakeAttachmentFact `json:"attachments,omitempty"`
	IsAttachmentsOnly bool                                 `json:"isAttachmentsOnly,omitempty"`
}

type decisionDuty struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type decisionTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type decisionActiveTask struct {
	Prompt  string `json:"prompt,omitempty"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type decisionPending struct {
	Prompt         string `json:"prompt,omitempty"`
	Question       string `json:"question,omitempty"`
	AskedAgo       string `json:"askedAgo,omitempty"`
	ExchangesSince int    `json:"exchangesSince"`
}

type decisionPendingChoice struct {
	Question       string                 `json:"question,omitempty"`
	SelectionMode  string                 `json:"selectionMode,omitempty"`
	Options        []decisionChoiceOption `json:"options"`
	AskedAgo       string                 `json:"askedAgo,omitempty"`
	ExchangesSince int                    `json:"exchangesSince"`
}

type decisionChoiceOption struct {
	Key   string `json:"key"`
	Index int    `json:"index"`
	Label string `json:"label,omitempty"`
}

type decisionPriorTask struct {
	Prompt string `json:"prompt,omitempty"`
	Result string `json:"result,omitempty"`
}

func buildDecisionState(request agentcontract.IntakeDecisionRequest, toolDescriptions []decisionTool) decisionState {
	state := decisionState{
		Agent:            decisionAgent{Name: request.AgentIdentity.DisplayName(), Mention: request.AgentIdentity.MentionExample()},
		Company:          decisionCompany{Name: request.Company.Name, TimeZone: request.Company.TimeZone},
		ConversationType: strings.TrimSpace(request.ConversationType),
		Now:              agentcontract.FormatContextTimestamp(request.EnvironmentNow, request.Company.TimeZone),
		Context:          decisionContextMessages(request),
		Messages:         decisionMessages(request),
		StandingDuties:   decisionStandingDuties(),
		AvailableTools:   toolDescriptions,
		ResponseLanguage: strings.TrimSpace(request.ResponseLanguage),
	}
	if strings.TrimSpace(request.ActiveTask.TaskRunID) != "" {
		state.ActiveTask = &decisionActiveTask{Prompt: request.ActiveTask.Prompt, Status: request.ActiveTask.Status, Summary: request.ActiveTask.Summary}
	}
	if request.IsTaskRecentlyFinished && state.ActiveTask == nil {
		state.RecentlyFinished = &decisionActiveTask{Prompt: request.ActiveTask.Prompt, Status: request.ActiveTask.Status, Summary: request.ActiveTask.Summary}
	}
	if strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" {
		state.PendingConfirmation = &decisionPending{
			Prompt:         request.PendingConfirmation.Prompt,
			Question:       request.PendingConfirmation.Question,
			AskedAgo:       openInteractionAge(request.PendingConfirmation.AskedAt, request.EnvironmentNow),
			ExchangesSince: request.PendingConfirmation.ExchangesSince,
		}
	}
	if strings.TrimSpace(request.PendingChoice.TaskRunID) != "" {
		state.PendingChoice = &decisionPendingChoice{
			Question:       request.PendingChoice.Question,
			SelectionMode:  request.PendingChoice.SelectionMode,
			Options:        decisionChoiceOptions(request.PendingChoice.Options),
			AskedAgo:       openInteractionAge(request.PendingChoice.AskedAt, request.EnvironmentNow),
			ExchangesSince: request.PendingChoice.ExchangesSince,
		}
	}
	if strings.TrimSpace(request.PriorTask.Prompt) != "" {
		state.PriorTask = &decisionPriorTask{Prompt: request.PriorTask.Prompt, Result: request.PriorTask.Result}
	}
	if request.AllowGiveUp {
		state.GiveUpAllowance = strings.TrimSpace(request.AllowGiveUpReason)
	}
	return state
}

func decisionContextMessages(request agentcontract.IntakeDecisionRequest) []decisionContextMessage {
	messages := recentVisibleMessages(request.VisibleContext.Messages, visibleContextMessageBudget)
	contextMessages := make([]decisionContextMessage, 0, len(messages))
	for _, message := range messages {
		contextMessages = append(contextMessages, decisionContextMessage{
			At:      agentcontract.FormatContextTimestamp(message.SentAt, request.Company.TimeZone),
			Speaker: firstNonEmptyAddressingText(message.SpeakerCallingName, message.Speaker, message.SpeakerHandle, "unknown"),
			Text:    strings.TrimSpace(message.Text),
		})
	}
	return contextMessages
}

func decisionMessages(request agentcontract.IntakeDecisionRequest) []decisionMessage {
	messages := make([]decisionMessage, 0, len(request.Messages))
	for index, message := range request.Messages {
		messages = append(messages, decisionMessage{
			ID:                decisionMessageKey(index),
			Sender:            strings.TrimSpace(message.SenderName),
			Handle:            strings.TrimSpace(message.SenderHandle),
			BotMentioned:      message.BotMentioned,
			At:                agentcontract.FormatContextTimestamp(message.SentAt, request.Company.TimeZone),
			Text:              strings.TrimSpace(message.Prompt),
			Attachments:       message.Attachments,
			IsAttachmentsOnly: message.IsAttachmentsOnly,
		})
	}
	return messages
}

func decisionStandingDuties() []decisionDuty {
	duties := []decisionDuty{}
	for _, duty := range agentcontract.StandingDuties() {
		duties = append(duties, decisionDuty{Name: duty.Name, Description: duty.Description})
	}
	return duties
}

func decisionChoiceOptions(options []agentcontract.ChoiceReplyOption) []decisionChoiceOption {
	choiceOptions := make([]decisionChoiceOption, 0, len(options))
	for index, option := range options {
		choiceOptions = append(choiceOptions, decisionChoiceOption{Key: strings.TrimSpace(option.Key), Index: index + 1, Label: strings.TrimSpace(option.Label)})
	}
	return choiceOptions
}

func decisionToolDescriptions(toolSet *toolcontract.ToolSet, callableToolNames []string) []decisionTool {
	descriptionByName := map[string]string{}
	if toolSet != nil {
		for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
			descriptionByName[strings.TrimSpace(toolDefinition.Name)] = strings.TrimSpace(toolDefinition.Description)
		}
	}
	tools := make([]decisionTool, 0, len(callableToolNames))
	for _, toolName := range callableToolNames {
		tools = append(tools, decisionTool{Name: toolName, Description: descriptionByName[toolName]})
	}
	return tools
}

func decisionMessageKey(index int) string {
	return "m" + strconv.Itoa(index+1)
}

func openInteractionAge(askedAt time.Time, now time.Time) string {
	if askedAt.IsZero() || now.IsZero() || !now.After(askedAt) {
		return "just now"
	}
	return now.Sub(askedAt).Round(time.Minute).String() + " ago"
}
