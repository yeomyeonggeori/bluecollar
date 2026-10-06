package approval

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type questionContext struct {
	ResponseLanguage string                     `json:"responseLanguage,omitempty"`
	OriginalRequest  string                     `json:"originalRequest,omitempty"`
	Operation        string                     `json:"operation,omitempty"`
	ApprovalScope    *scopeContext              `json:"approvalScope,omitempty"`
	ActionDetails    map[string]json.RawMessage `json:"actionDetails,omitempty"`
}

type scopeContext struct {
	Name   string `json:"name"`
	Covers string `json:"covers,omitempty"`
}

const questionSystemPrompt = `Write exactly one user-facing approval question.
The question asks whether to perform the pending action.
The question is the user's only view of the action, so it must show exactly what will happen, never a category of thing that will happen: 'post this?' is worthless, 'post "…"?' is the question.
When the action sends or posts content, quote the content verbatim in the question (a blockquote under one asking sentence works). Never paraphrase, summarize, or shorten it — what the user approves is exactly what will appear.
When the action replaces a span of text, show the span being replaced and its replacement, both verbatim.
When the action removes something, quote what will be removed — its text or the given preview — so the user can tell it from everything it is not.
Use the original request and action details to phrase the target, content, file, or event naturally. The action details are the call's own inputs, named as the tool names them.
When the action changes or removes something that already exists, say what it affects, and never describe a whole-item replacement as if it only touched a part of it.
Keep the asking sentence short; the quoted content is as long as it is.
Do not mention internal tool names, operation identifiers, JSON, schemas, approval gates, runtime, or implementation details.
Do not answer the question, report status, or explain the policy.
The question covers this one action and nothing after it, unless approvalScope is given. The original request is there to name what the action touches, never to describe the work it is a step toward.
Never promise a later step this action does not perform. Approving it must not read as approving anything that has to happen afterwards.
When approvalScope is given, saying yes approves every action of that scope for the rest of this task, not only this one. The question says so in plain words and states what the scope covers, using approvalScope.covers; when covers is empty, describe the scope from its name and the operation.
State each consequence the action details list plainly; a requester approving it must know them.`

const questionSchemaName = "approval_question"
const questionSchemaDocument = `{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`

func (gate *Gate) wordQuestion(ctx context.Context, request approvalRequest) string {
	text, errorValue := gate.generateQuestion(ctx, request)
	if errorValue == nil {
		return text
	}
	gate.recordWordingFailure(request, errorValue)
	return rawApprovalSummary(request)
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

func (gate *Gate) generateQuestion(ctx context.Context, request approvalRequest) (string, error) {
	if gate.languageModel == nil {
		return "", errors.New("approval wording needs a language model provider and none is configured")
	}
	encodedContext, errorValue := json.Marshal(newQuestionContext(request))
	if errorValue != nil {
		return "", errorValue
	}
	structuredResponse, errorValue := gate.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: questionSystemPrompt},
			{Role: "system", Content: responseLanguageInstruction(request.turn.ResponseLanguage)},
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

func newQuestionContext(request approvalRequest) questionContext {
	return questionContext{
		ResponseLanguage: strings.TrimSpace(request.turn.ResponseLanguage),
		OriginalRequest:  strings.TrimSpace(request.turn.Prompt),
		Operation:        request.toolName(),
		ApprovalScope:    scopeCoverage(request.toolDefinition),
		ActionDetails:    actionDetails(request.toolDefinition, request.toolInput),
	}
}

func scopeCoverage(tool toolcontract.ToolDefinition) *scopeContext {
	name := strings.TrimSpace(tool.ApprovalScope)
	if name == "" {
		return nil
	}
	return &scopeContext{Name: name, Covers: strings.TrimSpace(tool.ApprovalScopeSummary)}
}

func actionDetails(tool toolcontract.ToolDefinition, toolInput json.RawMessage) map[string]json.RawMessage {
	described := describingInputs(tool.ApprovalInputFields, toolInput)
	if len(described) == 0 {
		return nil
	}
	return described
}

func describingInputs(fieldNames []string, toolInput json.RawMessage) map[string]json.RawMessage {
	document := map[string]json.RawMessage{}
	if json.Unmarshal(toolInput, &document) != nil {
		return nil
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

func rawApprovalSummary(request approvalRequest) string {
	summary := request.toolName()
	if toolInput := strings.TrimSpace(string(request.toolInput)); toolInput != "" && toolInput != "{}" {
		summary += " " + toolInput
	}
	return summary
}

func responseLanguageInstruction(responseLanguage string) string {
	if toolcontract.ResolveResponseLanguage(responseLanguage) == toolcontract.ResponseLanguageEnglish {
		return "Write in English."
	}
	return "Write in Korean."
}
