package intake

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type pendingAnswerState struct {
	Messages            []pendingAnswerMessage     `json:"messages"`
	PendingConfirmation *pendingAnswerConfirmation `json:"pendingConfirmation,omitempty"`
	PendingChoice       *pendingAnswerChoice       `json:"pendingChoice,omitempty"`
	ResponseLanguage    string                     `json:"runtimeResponseLanguage,omitempty"`
}

type pendingAnswerMessage struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type pendingAnswerConfirmation struct {
	Prompt   string `json:"prompt,omitempty"`
	Question string `json:"question"`
}

type pendingAnswerChoice struct {
	Question      string                 `json:"question,omitempty"`
	SelectionMode string                 `json:"selectionMode,omitempty"`
	Options       []decisionChoiceOption `json:"options,omitempty"`
}

func buildPendingAnswerState(request agentcontract.IntakeDecisionRequest) pendingAnswerState {
	lastIndex := len(request.Messages) - 1
	state := pendingAnswerState{
		Messages:         []pendingAnswerMessage{{ID: decisionMessageKey(lastIndex), Text: strings.TrimSpace(request.Messages[lastIndex].Prompt)}},
		ResponseLanguage: strings.TrimSpace(request.ResponseLanguage),
	}
	if hasPendingConfirmation(request) {
		state.PendingConfirmation = &pendingAnswerConfirmation{Prompt: request.PendingConfirmation.Prompt, Question: request.PendingConfirmation.Question}
		return state
	}
	state.PendingChoice = &pendingAnswerChoice{
		Question:      request.PendingChoice.Question,
		SelectionMode: request.PendingChoice.SelectionMode,
		Options:       decisionChoiceOptions(request.PendingChoice.Options),
	}
	return state
}
