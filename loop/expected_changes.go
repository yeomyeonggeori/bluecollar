package loop

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	expectedChangesSchemaName        = "bluecollar_expected_changes"
	expectedChangesMaximumCount      = 8
	conversationBeforeRequestLimit   = 8
	conversationMessageMaximumLength = 600
)

type expectedChange struct {
	Change string `json:"change"`
	Asked  string `json:"asked"`
}

type changeVocabulary struct {
	Kinds []string
	Text  string
}

type pendingExpectedChanges struct {
	done      chan struct{}
	changes   []expectedChange
	isDefined bool
}

func (agentTurnRunner *AgentTurnRunner) beginExpectedChanges(ctx context.Context, taskRunID string, request AgentTurnRequest) {
	pending := &pendingExpectedChanges{done: make(chan struct{})}
	agentTurnRunner.expectedChanges.Store(taskRunID, pending)
	go func() {
		defer close(pending.done)
		pending.changes, pending.isDefined = agentTurnRunner.defineExpectedChanges(ctx, taskRunID, request)
	}()
}

func (agentTurnRunner *AgentTurnRunner) forgetExpectedChanges(taskRunID string) {
	agentTurnRunner.expectedChanges.Delete(taskRunID)
}

func (agentTurnRunner *AgentTurnRunner) expectedChangesFor(ctx context.Context, taskRunID string, request AgentTurnRequest) ([]expectedChange, bool) {
	stored, isStarted := agentTurnRunner.expectedChanges.Load(taskRunID)
	pending, isPending := stored.(*pendingExpectedChanges)
	if !isStarted || !isPending {
		return agentTurnRunner.defineExpectedChanges(ctx, taskRunID, request)
	}
	select {
	case <-pending.done:
		return pending.changes, pending.isDefined
	case <-ctx.Done():
		return nil, false
	}
}

func (agentTurnRunner *AgentTurnRunner) defineExpectedChanges(ctx context.Context, taskRunID string, request AgentTurnRequest) ([]expectedChange, bool) {
	vocabulary := changeVocabularyOf(request.ToolSet)
	if len(vocabulary.Kinds) == 0 {
		return nil, true
	}
	changes, errorValue := agentTurnRunner.askForExpectedChanges(ctx, request, vocabulary, nil)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionCheckDegraded, marshalEventBody(map[string]string{"stage": "expected_changes", "error": errorValue.Error()}))
		return nil, false
	}
	misquoted := misquotedChanges(changes, requestWordings(request))
	if len(misquoted) > 0 {
		if requoted, errorValue := agentTurnRunner.askForExpectedChanges(ctx, request, vocabulary, misquoted); errorValue == nil {
			changes = requoted
			misquoted = misquotedChanges(changes, requestWordings(request))
		}
	}
	changes = withoutMisquotedChanges(changes, misquoted)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventCompletionExpectedChanges, marshalEventBody(map[string]any{"expectedChanges": changes, "misquoted": misquoted}))
	return changes, true
}

func (agentTurnRunner *AgentTurnRunner) askForExpectedChanges(ctx context.Context, request AgentTurnRequest, vocabulary changeVocabulary, misquoted []string) ([]expectedChange, error) {
	instruction := expectedChangesInstruction(vocabulary)
	if len(misquoted) > 0 {
		instruction += "\n\nAn earlier answer wrote these as asked, but they are not words that appear in the request. Copy the words exactly:\n" + strings.Join(misquoted, "\n")
	}
	response, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: instruction},
			{Role: "user", Content: expectedChangesRequestText(request)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: expectedChangesSchemaName, Document: expectedChangesSchema(vocabulary), IsStrictlyEnforced: true},
	})
	if errorValue != nil {
		return nil, errorValue
	}
	var document struct {
		ExpectedChanges []expectedChange `json:"expectedChanges"`
	}
	if errorValue := json.Unmarshal([]byte(response.Content), &document); errorValue != nil {
		return nil, errors.New("expected changes answer is not the requested shape: " + truncateForLedger(response.Content, 200))
	}
	return knownChanges(document.ExpectedChanges, vocabulary), nil
}

func expectedChangesInstruction(vocabulary changeVocabulary) string {
	return strings.Join([]string{
		"Before any work starts, list the changes this request asks to be made. The finished task is checked against this list.",
		"A request that asks only for words - an answer, an explanation, a summary, a briefing, or a question about whether something was done - asks for no change: return an empty list. Looking records up is not a change.",
		"Write one entry per change the request asks for. change is one of the kinds below.",
		"asked is the exact words of the request, or of the latest message about it, that ask for this change, copied character for character. Never paraphrase, translate, complete or add to them. A latest message may correct or narrow the request; what it says wins. The conversation before the request only helps you understand what the request refers to; what it asked for earlier is already handled and is not part of this request.",
		"Do not add changes the request does not ask for.",
		"Kinds of change:\n" + vocabulary.Text,
	}, "\n")
}

func expectedChangesSchema(vocabulary changeVocabulary) string {
	document := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"expectedChanges"},
		"properties": map[string]any{
			"expectedChanges": map[string]any{
				"type":     "array",
				"maxItems": expectedChangesMaximumCount,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"change", "asked"},
					"properties": map[string]any{
						"change": map[string]any{"type": "string", "enum": vocabulary.Kinds},
						"asked":  map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	encodedDocument, _ := json.Marshal(document)
	return string(encodedDocument)
}

func expectedChangesRequestText(request AgentTurnRequest) string {
	wordings := requestWordings(request)
	text := "Request:\n" + wordings[0]
	if len(wordings) > 1 {
		text += "\n\nLatest message about it:\n" + wordings[1]
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
			lines = append(lines, firstNonEmptyString(message.SpeakerCallingName, message.Speaker)+": "+truncateForLedger(text, conversationMessageMaximumLength))
		}
	}
	return lines
}

func changeVocabularyOf(toolSet *toolcontract.ToolSet) changeVocabulary {
	if toolSet == nil {
		return changeVocabulary{}
	}
	toolsByKind := map[string][]string{}
	for _, toolName := range toolSet.ListToolNames() {
		definition, isFound := toolSet.ToolDefinition(toolName)
		if !isFound || definition.ResultContract == nil {
			continue
		}
		for _, effect := range definition.ResultContract.Effects {
			kind := changeKind(effect.ObjectType, effect.Effect)
			toolsByKind[kind] = append(toolsByKind[kind], toolName+": "+firstSentence(definition.Description))
		}
	}
	kinds := make([]string, 0, len(toolsByKind))
	for kind := range toolsByKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	lines := []string{}
	for _, kind := range kinds {
		lines = append(lines, kind)
		for _, tool := range toolsByKind[kind] {
			lines = append(lines, "  - "+tool)
		}
	}
	return changeVocabulary{Kinds: kinds, Text: strings.Join(lines, "\n")}
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

func knownChanges(changes []expectedChange, vocabulary changeVocabulary) []expectedChange {
	known := []expectedChange{}
	for _, change := range changes {
		for _, kind := range vocabulary.Kinds {
			if strings.TrimSpace(change.Change) == kind {
				known = append(known, expectedChange{Change: kind, Asked: strings.TrimSpace(change.Asked)})
				break
			}
		}
	}
	return known
}

func misquotedChanges(changes []expectedChange, wordings []string) []string {
	misquoted := []string{}
	for _, change := range changes {
		if !isQuotedFrom(change.Asked, wordings) {
			misquoted = appendUniqueStrings(misquoted, change.Asked)
		}
	}
	return misquoted
}

func isQuotedFrom(asked string, wordings []string) bool {
	quote := collapsedWhitespace(asked)
	if quote == "" {
		return false
	}
	for _, wording := range wordings {
		if strings.Contains(collapsedWhitespace(wording), quote) {
			return true
		}
	}
	return false
}

func withoutMisquotedChanges(changes []expectedChange, misquoted []string) []expectedChange {
	kept := []expectedChange{}
	for _, change := range changes {
		if !slices.Contains(misquoted, change.Asked) {
			kept = append(kept, change)
		}
	}
	return kept
}

func collapsedWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
