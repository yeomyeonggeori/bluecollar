package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type questionContext struct {
	ResponseLanguage string                     `json:"responseLanguage,omitempty"`
	OriginalRequest  string                     `json:"originalRequest,omitempty"`
	ModelDraft       string                     `json:"modelDraft,omitempty"`
	Operation        string                     `json:"operation,omitempty"`
	ActionDetails    map[string]json.RawMessage `json:"actionDetails,omitempty"`
	PrintedBelow     []string                   `json:"printedBelow"`
	Choices          []holdrecord.Choice        `json:"choices,omitempty"`
}

const questionSystemPrompt = `Write exactly one user-facing approval question.
The question asks whether to perform the pending action.
Beneath your question the runtime prints the values listed in printedBelow exactly as they will be carried out, each under the label you give it in labels. A label is a few plain words in the reply language for what that value is to the requester, such as who receives it or what is sent; never the detail's own name. Never quote, paraphrase, or repeat those values in the question, even where the original request or the model draft does. Every other action detail is shown nowhere else, so the question states each one in plain words: a setting by what it does, never by its field name, a bare true or false, or a code.
Write one short asking sentence that names the action and whom or what it reaches in plain words, from the original request and the action details. When the action details name a resolved target, name it that way and never repeat a search phrase the caller typed.
When the action changes or removes something that already exists, say what it affects, and never describe a whole-item replacement as if it only touched a part of it.
` + agentcontract.PeopleReadWhatTheyRecognize + `
Do not mention schemas, approval gates, the runtime, or implementation details.
Do not answer the question, report status, or explain the policy.
The question covers this one action and nothing after it. The original request is there to name what the action touches, never to describe the work it is a step toward.
Never promise a later step this action does not perform. Approving it must not read as approving anything that has to happen afterwards.
State each consequence the action details list plainly; a requester approving it must know them.
Unless choices are given, write no options, no numbered list and no instructions for how to answer: the conversation shows the requester how to approve or decline. A list in the model draft or the original request is not a choice; never carry it over.
When choices are given, the question offers exactly those choices, in the order given, followed by cancelling, as a short numbered list the requester can answer by number. A choice with startsAt runs the action at that moment, written as a local date and time; a choice without startsAt runs it now. Offer no choice that is not given.`

const handoffSystemPrompt = `Write exactly one short user-facing message that hands the requester the agent's browser so they can do one step in it themselves.
On the line after your message the runtime prints a link. Opening it shows the agent's browser live; the requester works in it and presses the button that hands it back when they are done.
Say what they need to do in that browser in plain words, from the step in the action details and the site the resolved target names, and ask them to open the link below and hand the browser back when they finish.
The agent never sees the screen or what they type there; you may say so. Never ask for a password, a code or any other secret in the conversation.
Do not add anything the step does not state, do not write the link yourself, and do not mention the runtime or implementation details.
` + agentcontract.PeopleReadWhatTheyRecognize

const questionSchemaName = "approval_question"

type wordedQuestion struct {
	Question string            `json:"question"`
	Labels   map[string]string `json:"labels,omitempty"`
}

type Worder struct {
	languageModel model.LanguageModelProvider
}

func NewWorder(languageModel model.LanguageModelProvider) Worder {
	return Worder{languageModel: languageModel}
}

func (worder Worder) WordQuestion(ctx context.Context, facts holdrecord.QuestionFacts) holdrecord.QuestionWording {
	worded, errorValue := worder.generateQuestion(ctx, facts)
	if errorValue != nil {
		return holdrecord.QuestionWording{Text: withDetailsBeneath(rawApprovalSummary(facts), rawCallDetails(facts)), Failure: errorValue}
	}
	return holdrecord.QuestionWording{Text: withDetailsBeneath(worded.Question, heldCallDetails(facts, worded.Labels))}
}

func withDetailsBeneath(sentence string, details string) string {
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

type answerToRedo struct {
	failure error
	again   string
}

func (redo answerToRedo) Error() string {
	return redo.failure.Error()
}

func (redo answerToRedo) Unwrap() error {
	return redo.failure
}

var errQuestionSpansLines = errors.New("the approval question spans more than one line, where only the asking sentence belongs")

var errLabelRepeatsItsValue = errors.New("the model labelled a printed detail with the value it labels")

const oneLineAgain = "Your question spans more than one line. Write it again as the one asking sentence alone: the conversation shows how to answer, and the runtime prints the values in printedBelow beneath it."

const labelsAgain = "A label you gave is the very value it labels, so the line would read as that value twice. Write the labels again: each says what its value is to the requester, such as who receives it or what is sent."

func (worder Worder) generateQuestion(ctx context.Context, facts holdrecord.QuestionFacts) (wordedQuestion, error) {
	if worder.languageModel == nil {
		return wordedQuestion{}, errors.New("approval wording needs a language model provider and none is configured")
	}
	questionContext := newQuestionContext(facts)
	encodedContext, errorValue := json.Marshal(questionContext)
	if errorValue != nil {
		return wordedQuestion{}, errorValue
	}
	schemaDocument, errorValue := questionSchemaFor(questionContext.PrintedBelow)
	if errorValue != nil {
		return wordedQuestion{}, errorValue
	}
	asking := questionAsking{facts: facts, details: questionContext.ActionDetails, printedBelow: questionContext.PrintedBelow, schema: model.StructuredOutputSchema{Name: questionSchemaName, Document: schemaDocument, IsStrictlyEnforced: true}}
	messages := []model.Message{
		{Role: "system", Content: systemPromptFor(facts)},
		{Role: "system", Content: agentcontract.ResponseLanguageInstruction(facts.ResponseLanguage)},
		{Role: "user", Content: string(encodedContext)},
	}
	worded, answered, errorValue := worder.askForQuestion(ctx, asking, messages)
	redo, isRedoable := errors.AsType[answerToRedo](errorValue)
	if !isRedoable {
		return worded, errorValue
	}
	worded, _, errorValue = worder.askForQuestion(ctx, asking, append(messages, model.Message{Role: "assistant", Content: answered}, model.Message{Role: "user", Content: redo.again}))
	return worded, errorValue
}

type questionAsking struct {
	facts        holdrecord.QuestionFacts
	details      map[string]json.RawMessage
	printedBelow []string
	schema       model.StructuredOutputSchema
}

func (worder Worder) askForQuestion(ctx context.Context, asking questionAsking, messages []model.Message) (wordedQuestion, string, error) {
	structuredResponse, errorValue := worder.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages:               messages,
		StructuredOutputSchema: asking.schema,
	})
	if errorValue != nil {
		return wordedQuestion{}, "", errorValue
	}
	worded, errorValue := readQuestion(structuredResponse.Content, asking.printedBelow)
	if errorValue != nil {
		return wordedQuestion{}, structuredResponse.Content, errorValue
	}
	if mustFitOneLine(asking.facts) && strings.Contains(worded.Question, "\n") {
		return wordedQuestion{}, structuredResponse.Content, answerToRedo{failure: errQuestionSpansLines, again: oneLineAgain}
	}
	if labelRepeatsItsValue(worded.Labels, asking.details) {
		return wordedQuestion{}, structuredResponse.Content, answerToRedo{failure: errLabelRepeatsItsValue, again: labelsAgain}
	}
	return worded, structuredResponse.Content, nil
}

func labelRepeatsItsValue(labels map[string]string, details map[string]json.RawMessage) bool {
	for name, label := range labels {
		if label == strings.TrimSpace(detailText(details[name])) {
			return true
		}
	}
	return false
}

func questionSchemaFor(printedBelow []string) (string, error) {
	properties := map[string]any{"question": map[string]string{"type": "string"}}
	required := []string{"question"}
	if len(printedBelow) > 0 {
		labelProperties := map[string]any{}
		for _, name := range printedBelow {
			labelProperties[name] = map[string]string{"type": "string"}
		}
		properties["labels"] = map[string]any{"type": "object", "properties": labelProperties, "required": printedBelow, "additionalProperties": false}
		required = append(required, "labels")
	}
	encoded, errorValue := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
	return string(encoded), errorValue
}

func mustFitOneLine(facts holdrecord.QuestionFacts) bool {
	return len(facts.Choices) == 0 && !isHandoff(facts)
}

func systemPromptFor(facts holdrecord.QuestionFacts) string {
	if isHandoff(facts) {
		return handoffSystemPrompt
	}
	return questionSystemPrompt
}

func isHandoff(facts holdrecord.QuestionFacts) bool {
	return strings.TrimSpace(facts.Target.HandoffID) != ""
}

func readQuestion(content string, printedBelow []string) (wordedQuestion, error) {
	answer := wordedQuestion{}
	if errorValue := json.Unmarshal([]byte(content), &answer); errorValue != nil {
		return wordedQuestion{}, errorValue
	}
	question := strings.TrimSpace(answer.Question)
	if question == "" {
		return wordedQuestion{}, errors.New("the model returned an empty approval question")
	}
	labels, errorValue := readLabels(answer.Labels, printedBelow)
	return wordedQuestion{Question: question, Labels: labels}, errorValue
}

func readLabels(answered map[string]string, printedBelow []string) (map[string]string, error) {
	labels := map[string]string{}
	for _, name := range printedBelow {
		label := strings.TrimSpace(answered[name])
		if label == "" {
			return nil, fmt.Errorf("the model gave no label for the printed detail %s", name)
		}
		if strings.Contains(label, "\n") {
			return nil, fmt.Errorf("the model's label for the printed detail %s spans more than one line", name)
		}
		labels[name] = label
	}
	return labels, nil
}

func newQuestionContext(facts holdrecord.QuestionFacts) questionContext {
	details := actionDetails(facts)
	return questionContext{
		ResponseLanguage: strings.TrimSpace(facts.ResponseLanguage),
		OriginalRequest:  strings.TrimSpace(facts.OriginalRequest),
		ModelDraft:       strings.TrimSpace(facts.ModelDraft),
		Operation:        strings.TrimSpace(facts.Tool.Name),
		ActionDetails:    details,
		PrintedBelow:     printedDetailNames(facts, details),
		Choices:          facts.Choices,
	}
}

func actionDetails(facts holdrecord.QuestionFacts) map[string]json.RawMessage {
	described := describingInputs(facts.Tool.ApprovalInputFields, facts.Input)
	if facts.Target.IsResolved() {
		delete(described, strings.TrimSpace(facts.Target.InputField))
		addDetail(described, resolvedTargetDetail, facts.Target.Title)
		addDetail(described, resolvedTargetAddressDetail, facts.Target.Address)
		addDetail(described, resolvedTargetStartsAtDetail, facts.Target.StartsAt)
		addDetail(described, targetPreviewDetail, facts.Target.Preview)
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
