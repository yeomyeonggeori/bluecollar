package approval

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type questionContext struct {
	ResponseLanguage string                     `json:"responseLanguage,omitempty"`
	OriginalRequest  string                     `json:"originalRequest,omitempty"`
	ModelDraft       string                     `json:"modelDraft,omitempty"`
	Operation        string                     `json:"operation,omitempty"`
	ActionDetails    map[string]json.RawMessage `json:"actionDetails,omitempty"`
	Choices          []holdrecord.Choice        `json:"choices,omitempty"`
}

const questionSystemPrompt = `Write exactly one user-facing approval question.
The question asks whether to perform the pending action.
Beneath your question the runtime prints the action's details exactly as they will be carried out: the resolved target, a target preview when there is one, and each input the action takes. Never quote, paraphrase, or repeat those details; the requester reads them there, verbatim.
Write one short asking sentence that names the action and whom or what it reaches in plain words, from the original request and the action details. When the action details name a resolved target, name it that way and never repeat a search phrase the caller typed.
When the action changes or removes something that already exists, say what it affects, and never describe a whole-item replacement as if it only touched a part of it.
Do not mention internal tool names, operation identifiers, JSON, schemas, approval gates, runtime, or implementation details.
Never show a record's identifier (an ID, a UUID, a key). Name the record by what a person recognizes in it: its date and time, its title, or whose it is.
Do not answer the question, report status, or explain the policy.
The question covers this one action and nothing after it. The original request is there to name what the action touches, never to describe the work it is a step toward.
Never promise a later step this action does not perform. Approving it must not read as approving anything that has to happen afterwards.
State each consequence the action details list plainly; a requester approving it must know them.
When choices are given, the question offers exactly those choices, in the order given, followed by cancelling, as a short numbered list the requester can answer by number. A choice with startsAt runs the action at that moment, written as a local date and time; a choice without startsAt runs it now. Offer no choice that is not given.`

const questionSchemaName = "approval_question"
const questionSchemaDocument = `{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`

type Worder struct {
	languageModel model.LanguageModelProvider
}

func NewWorder(languageModel model.LanguageModelProvider) Worder {
	return Worder{languageModel: languageModel}
}

func (worder Worder) WordQuestion(ctx context.Context, facts holdrecord.QuestionFacts) holdrecord.QuestionWording {
	text, errorValue := worder.generateQuestion(ctx, facts)
	if errorValue != nil {
		return holdrecord.QuestionWording{Text: withHeldCallDetails(rawApprovalSummary(facts), facts), Failure: errorValue}
	}
	return holdrecord.QuestionWording{Text: withHeldCallDetails(text, facts)}
}

func withHeldCallDetails(sentence string, facts holdrecord.QuestionFacts) string {
	details := heldCallDetails(facts)
	if details == "" {
		return sentence
	}
	return strings.TrimSpace(sentence) + "\n\n" + details
}

func (gate *Gate) wordQuestion(ctx context.Context, request approvalRequest) string {
	wording := NewWorder(gate.languageModel).WordQuestion(ctx, request.questionFacts())
	if wording.Failure != nil {
		gate.recordWordingFailure(request, wording.Failure)
	}
	return wording.Text
}

func (request approvalRequest) questionFacts() holdrecord.QuestionFacts {
	return holdrecord.QuestionFacts{
		ResponseLanguage: request.turn.ResponseLanguage,
		OriginalRequest:  request.turn.Prompt,
		Tool:             request.toolDefinition,
		Input:            request.toolInput,
	}
}

func (gate *Gate) recordWordingFailure(request approvalRequest, errorValue error) {
	taskRunID := strings.TrimSpace(request.taskRunID)
	slog.Warn("approval.wording_failed", "taskRunID", taskRunID, "toolName", request.toolName(), "error", errorValue.Error())
	if taskRunID == "" {
		return
	}
	gate.taskRuns.AppendTaskEvent(taskRunID, agentcontract.TaskEventAgentApprovalWordingFailed, marshalEventBody(map[string]string{
		"toolName": request.toolName(),
		"error":    errorValue.Error(),
	}))
}

func (worder Worder) generateQuestion(ctx context.Context, facts holdrecord.QuestionFacts) (string, error) {
	if worder.languageModel == nil {
		return "", errors.New("approval wording needs a language model provider and none is configured")
	}
	encodedContext, errorValue := json.Marshal(newQuestionContext(facts))
	if errorValue != nil {
		return "", errorValue
	}
	structuredResponse, errorValue := worder.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: questionSystemPrompt},
			{Role: "system", Content: responseLanguageInstruction(facts.ResponseLanguage)},
			{Role: "user", Content: string(encodedContext)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: questionSchemaName, Document: questionSchemaDocument, IsStrictlyEnforced: true},
	})
	if errorValue != nil {
		return "", errorValue
	}
	return readQuestion(structuredResponse.Content)
}

func readQuestion(content string) (string, error) {
	answer := struct {
		Question string `json:"question"`
	}{}
	if errorValue := json.Unmarshal([]byte(content), &answer); errorValue != nil {
		return "", errorValue
	}
	question := strings.TrimSpace(answer.Question)
	if question == "" {
		return "", errors.New("the model returned an empty approval question")
	}
	return question, nil
}

func newQuestionContext(facts holdrecord.QuestionFacts) questionContext {
	return questionContext{
		ResponseLanguage: strings.TrimSpace(facts.ResponseLanguage),
		OriginalRequest:  strings.TrimSpace(facts.OriginalRequest),
		ModelDraft:       strings.TrimSpace(facts.ModelDraft),
		Operation:        strings.TrimSpace(facts.Tool.Name),
		ActionDetails:    actionDetails(facts),
		Choices:          facts.Choices,
	}
}

func actionDetails(facts holdrecord.QuestionFacts) map[string]json.RawMessage {
	described := describingInputs(facts.Tool.ApprovalInputFields, facts.Input)
	if facts.Target.IsResolved() {
		delete(described, strings.TrimSpace(facts.Target.InputField))
		addDetail(described, "resolvedTarget", facts.Target.Title)
		addDetail(described, "resolvedTargetStartsAt", facts.Target.StartsAt)
		addDetail(described, "targetPreview", facts.Target.Preview)
	}
	if len(described) == 0 {
		return nil
	}
	return described
}

func addDetail(details map[string]json.RawMessage, name string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	encoded, errorValue := json.Marshal(strings.TrimSpace(value))
	if errorValue == nil {
		details[name] = encoded
	}
}

func describingInputs(fieldNames []string, toolInput json.RawMessage) map[string]json.RawMessage {
	document := map[string]json.RawMessage{}
	if json.Unmarshal(toolInput, &document) != nil {
		return map[string]json.RawMessage{}
	}
	if len(fieldNames) == 0 {
		return document
	}
	described := map[string]json.RawMessage{}
	for _, fieldName := range fieldNames {
		if value, isPresent := document[fieldName]; isPresent {
			described[fieldName] = value
		}
	}
	return described
}

func rawApprovalSummary(facts holdrecord.QuestionFacts) string {
	summary := strings.TrimSpace(facts.Tool.Name)
	if facts.Target.IsResolved() {
		return strings.TrimSpace(summary + " " + firstNonEmpty(facts.Target.Title, facts.Target.ID))
	}
	if toolInput := strings.TrimSpace(string(facts.Input)); toolInput != "" && toolInput != "{}" {
		summary += " " + toolInput
	}
	return summary
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func responseLanguageInstruction(responseLanguage string) string {
	if toolcontract.ResolveResponseLanguage(responseLanguage) == toolcontract.ResponseLanguageEnglish {
		return "Write in English."
	}
	return "Write in Korean."
}
