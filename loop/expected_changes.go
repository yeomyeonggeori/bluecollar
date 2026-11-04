package loop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	expectedChangesSchemaName        = "bluecollar_expected_changes"
	expectedChangesMaximumCount      = 8
	conversationBeforeRequestLimit   = 8
	conversationMessageMaximumLength = 600
)

type pendingExpectedChanges struct {
	done        chan struct{}
	changes     []string
	isDefined   bool
	mutex       sync.Mutex
	latestCheck *recordedChangeCheck
}

type recordedChangeCheck struct {
	observationCount int
	check            changeCheck
}

func (agentTurnRunner *AgentTurnRunner) beginExpectedChanges(ctx context.Context, taskRunID string, request AgentTurnRequest) {
	pending := &pendingExpectedChanges{done: make(chan struct{})}
	agentTurnRunner.expectedChanges.Store(taskRunID, pending)
	go func() {
		defer close(pending.done)
		pending.changes, pending.isDefined = agentTurnRunner.defineExpectedChanges(ctx, taskRunID, request)
	}()
}

func (agentTurnRunner *AgentTurnRunner) latestChangeCheck(taskRunID string, observationCount int) (changeCheck, bool) {
	pending, isPending := agentTurnRunner.pendingExpectedChangesOf(taskRunID)
	if !isPending {
		return changeCheck{}, false
	}
	pending.mutex.Lock()
	defer pending.mutex.Unlock()
	if pending.latestCheck == nil || pending.latestCheck.observationCount != observationCount {
		return changeCheck{}, false
	}
	return pending.latestCheck.check, true
}

func (agentTurnRunner *AgentTurnRunner) rememberChangeCheck(taskRunID string, observationCount int, check changeCheck) {
	pending, isPending := agentTurnRunner.pendingExpectedChangesOf(taskRunID)
	if !isPending {
		return
	}
	pending.mutex.Lock()
	defer pending.mutex.Unlock()
	pending.latestCheck = &recordedChangeCheck{observationCount: observationCount, check: check}
}

func (agentTurnRunner *AgentTurnRunner) pendingExpectedChangesOf(taskRunID string) (*pendingExpectedChanges, bool) {
	stored, isStarted := agentTurnRunner.expectedChanges.Load(taskRunID)
	pending, isPending := stored.(*pendingExpectedChanges)
	return pending, isStarted && isPending
}

func (agentTurnRunner *AgentTurnRunner) forgetExpectedChanges(taskRunID string) {
	agentTurnRunner.expectedChanges.Delete(taskRunID)
}

func (agentTurnRunner *AgentTurnRunner) expectedChangesFor(ctx context.Context, taskRunID string, request AgentTurnRequest) ([]string, bool) {
	pending, isPending := agentTurnRunner.pendingExpectedChangesOf(taskRunID)
	if !isPending {
		return agentTurnRunner.defineExpectedChanges(ctx, taskRunID, request)
	}
	select {
	case <-pending.done:
		return pending.changes, pending.isDefined
	case <-ctx.Done():
		return nil, false
	}
}

func (agentTurnRunner *AgentTurnRunner) defineExpectedChanges(ctx context.Context, taskRunID string, request AgentTurnRequest) ([]string, bool) {
	if !canRecordAChange(request.ToolSet) {
		return nil, true
	}
	changes, errorValue := agentTurnRunner.askForExpectedChanges(ctx, request)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionCheckDegraded, marshalEventBody(map[string]string{"stage": "expected_changes", "error": errorValue.Error()}))
		return nil, false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionExpectedChanges, marshalEventBody(map[string]any{"expectedChanges": changes}))
	return changes, true
}

func (agentTurnRunner *AgentTurnRunner) askForExpectedChanges(ctx context.Context, request AgentTurnRequest) ([]string, error) {
	response, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: expectedChangesInstruction},
			{Role: "user", Content: expectedChangesRequestText(request)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: expectedChangesSchemaName, Document: expectedChangesSchema(), IsStrictlyEnforced: true},
	})
	if errorValue != nil {
		return nil, errorValue
	}
	var document struct {
		ExpectedChanges []string `json:"expectedChanges"`
	}
	if errorValue := json.Unmarshal([]byte(response.Content), &document); errorValue != nil {
		return nil, errors.New("expected changes answer is not the requested shape: " + truncateForLedger(response.Content, 200))
	}
	return appendUniqueStrings(nil, nonEmptyStrings(document.ExpectedChanges)...), nil
}

var expectedChangesInstruction = strings.Join([]string{
	"Before any work starts, list the changes this request asks to be made. The finished task is checked against this list.",
	"A request that asks only for words - an answer, an explanation, a summary, a briefing, or a question about whether something was done - asks for no change: return an empty list. Looking records up is not a change.",
	"List only changes the person asks the agent to make. Work the person reports having done, or says they will do, is not a change to list; when the request asks to record that work, the change is the record, such as adding it as a completed task.",
	"Write one entry per thing the request changes, never one per step of doing it, as one short sentence in the language of the request: what it makes, changes, removes or sends, and what is true of it once done.",
	"A latest message may answer a question about the request, correct it or narrow it; what it says wins. The conversation before the request only helps you understand what the request refers to; what it asked for earlier is already handled and is not part of this request.",
	"Write only what the request and the latest message state. Do not add changes they do not ask for, and do not add values, names or details they do not give.",
}, "\n")

func expectedChangesSchema() string {
	document := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"expectedChanges"},
		"properties": map[string]any{
			"expectedChanges": map[string]any{
				"type":     "array",
				"maxItems": expectedChangesMaximumCount,
				"items":    map[string]any{"type": "string"},
			},
		},
	}
	encodedDocument, _ := json.Marshal(document)
	return string(encodedDocument)
}

func expectedChangesRequestText(request AgentTurnRequest) string {
	wordings := requestWordings(request)
	text := "Request" + sentAtLabel(request.TurnStartedAt, request.Company.TimeZone) + ":\n" + wordings[0]
	if len(wordings) > 1 {
		text += "\n\nLatest message about it" + sentAtLabel(request.TurnStartedAt, request.Company.TimeZone) + ":\n" + wordings[1]
	}
	if conversation := conversationBeforeRequest(request); len(conversation) > 0 {
		text = "Conversation before the request:\n" + strings.Join(conversation, "\n") + "\n\n" + text
	}
	return text
}

func requestWordings(request AgentTurnRequest) []string {
	return appendUniqueStrings(nil, nonEmptyStrings([]string{request.ActiveGoal.OriginalInstruction, request.Prompt})...)
}

func conversationBeforeRequest(request AgentTurnRequest) []string {
	messages := request.VisibleContext.Messages
	lines := []string{}
	for _, message := range messages[max(0, len(messages)-conversationBeforeRequestLimit):] {
		if text := strings.TrimSpace(message.Text); text != "" {
			lines = append(lines, contextTimestampPrefix(message.SentAt, request.Company.TimeZone)+firstNonEmptyString(message.SpeakerCallingName, message.Speaker)+": "+truncateForLedger(text, conversationMessageMaximumLength))
		}
	}
	return lines
}

func contextTimestampPrefix(sentAt time.Time, timeZone string) string {
	if stamp := agentcontract.FormatContextTimestamp(sentAt, timeZone); stamp != "" {
		return "[" + stamp + "] "
	}
	return ""
}

func sentAtLabel(sentAt time.Time, timeZone string) string {
	if stamp := agentcontract.FormatContextTimestamp(sentAt, timeZone); stamp != "" {
		return " (sent " + stamp + ")"
	}
	return ""
}

func canRecordAChange(toolSet *toolcontract.ToolSet) bool {
	if toolSet == nil {
		return false
	}
	for _, definition := range toolSet.ListRegisteredToolDefinitions() {
		if definition.ResultContract != nil && len(definition.ResultContract.Effects) > 0 {
			return true
		}
	}
	return false
}

func changeKind(objectType string, effect string) string {
	return strings.TrimSpace(objectType) + " " + strings.TrimSpace(effect)
}

func firstSentence(text string) string {
	trimmedText := strings.TrimSpace(text)
	if end := strings.Index(trimmedText, ". "); end >= 0 {
		return trimmedText[:end+1]
	}
	return trimmedText
}
