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
)

type questionContext struct {
	ResponseLanguage string                     `json:"responseLanguage,omitempty"`
	Operation        string                     `json:"operation,omitempty"`
	ActionDetails    map[string]json.RawMessage `json:"actionDetails,omitempty"`
	QuotedBelow      string                     `json:"quotedBelow,omitempty"`
	Choices          []holdrecord.Choice        `json:"choices,omitempty"`
}

const questionSystemPrompt = `Write exactly one user-facing approval question that asks whether to perform the operation, naming the action and whom or what it reaches from actionDetails.
The values in actionDetails are shown nowhere else, so the question states each one in plain words: a setting by what it does, never by its field name, a bare true or false, or a code. A requester approving the action must know them. When a resolved target comes with an address, someone else shares its name, so give the address with it.
When the action changes or removes something that already exists, say what it affects, and never describe a whole-item replacement as if it only touched a part of it.
` + agentcontract.PeopleReadWhatTheyRecognize + `
Do not mention schemas, approval gates, the runtime, or implementation details.
Do not answer the question, report status, or explain the policy. The question covers this one action and nothing after it.
Unless choices are given, write no options, no numbered list and no instructions for how to answer: the conversation shows the requester how to approve or decline.
When choices are given, the question offers exactly those choices, in the order given, followed by cancelling, as a short numbered list the requester can answer by number. A choice with startsAt runs the action at that moment, written as a local date and time; a choice without startsAt runs it now. Offer no choice that is not given.`

const quotationRule = `
The runtime prints the input quotedBelow names beneath your question as a quotation, exactly as it will be carried out; you are not shown it. Ask in the shape "shall I do this, to whom or where, like this?", pointing at the quotation and never writing its content: for example "박예시님께 이렇게 보낼까요?" or "Shall I run this command?".`

const handoffSystemPrompt = `Write exactly one short user-facing message that hands the requester the agent's browser so they can do one step in it themselves.
On the line after your message the runtime prints a link. Opening it shows the agent's browser live; the requester works in it and presses the button that hands it back when they are done.
Say what they need to do in that browser in plain words, from the step in the action details and the site the resolved target names, and ask them to open the link below and hand the browser back when they finish.
The agent never sees the screen or what they type there; you may say so. Never ask for a password, a code or any other secret in the conversation.
Do not add anything the step does not state, do not write the link yourself, and do not mention the runtime or implementation details.
` + agentcontract.PeopleReadWhatTheyRecognize

const questionSchemaName = "approval_question"

type wordedQuestion struct {
	Question string `json:"question"`
}

const questionSchemaDocument = `{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`

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
	return holdrecord.QuestionWording{Text: withDetailsBeneath(worded.Question, quotation(facts))}
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

const oneLineAgain = "Your question spans more than one line. Write it again as the one asking sentence alone: the conversation shows how to answer, and the runtime prints any quotation beneath it."

func (worder Worder) generateQuestion(ctx context.Context, facts holdrecord.QuestionFacts) (wordedQuestion, error) {
	if worder.languageModel == nil {
		return wordedQuestion{}, errors.New("approval wording needs a language model provider and none is configured")
	}
	questionContext := newQuestionContext(facts)
	encodedContext, errorValue := json.Marshal(questionContext)
	if errorValue != nil {
		return wordedQuestion{}, errorValue
	}
	asking := questionAsking{facts: facts, schema: model.StructuredOutputSchema{Name: questionSchemaName, Document: questionSchemaDocument, IsStrictlyEnforced: true}}
	messages := questionMessages(facts, encodedContext)
	worded, answered, errorValue := worder.askForQuestion(ctx, asking, messages)
	redo, isRedoable := errors.AsType[answerToRedo](errorValue)
	if !isRedoable {
		return worded, errorValue
	}
	worded, _, errorValue = worder.askForQuestion(ctx, asking, append(messages, model.Message{Role: "assistant", Content: answered}, model.Message{Role: "user", Content: redo.again}))
	return worded, errorValue
}

const voiceInstruction = "You are the agent the lines below describe, asking the requester they describe. Ask in that tone and address the requester as they say. Do not introduce yourself, and add nothing these lines do not ask for:\n"

func questionMessages(facts holdrecord.QuestionFacts, encodedContext []byte) []model.Message {
	messages := []model.Message{
		{Role: "system", Content: systemPromptFor(facts)},
		{Role: "system", Content: agentcontract.ResponseLanguageInstruction(facts.ResponseLanguage)},
	}
	if voice := strings.TrimSpace(facts.Voice); voice != "" {
		messages = append(messages, model.Message{Role: "system", Content: voiceInstruction + voice})
	}
	return append(messages, model.Message{Role: "user", Content: string(encodedContext)})
}

type questionAsking struct {
	facts  holdrecord.QuestionFacts
	schema model.StructuredOutputSchema
}

func (worder Worder) askForQuestion(ctx context.Context, asking questionAsking, messages []model.Message) (wordedQuestion, string, error) {
	structuredResponse, errorValue := worder.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages:               messages,
		StructuredOutputSchema: asking.schema,
	})
	if errorValue != nil {
		return wordedQuestion{}, "", errorValue
	}
	worded, errorValue := readQuestion(structuredResponse.Content)
	if errorValue != nil {
		return wordedQuestion{}, structuredResponse.Content, errorValue
	}
	if mustFitOneLine(asking.facts) && strings.Contains(worded.Question, "\n") {
		return wordedQuestion{}, structuredResponse.Content, answerToRedo{failure: errQuestionSpansLines, again: oneLineAgain}
	}
	return worded, structuredResponse.Content, nil
}

func mustFitOneLine(facts holdrecord.QuestionFacts) bool {
	return len(facts.Choices) == 0 && !isHandoff(facts)
}

func systemPromptFor(facts holdrecord.QuestionFacts) string {
	if isHandoff(facts) {
		return handoffSystemPrompt
	}
	if quotedInputName(facts) != "" {
		return questionSystemPrompt + quotationRule
	}
	return questionSystemPrompt
}

func isHandoff(facts holdrecord.QuestionFacts) bool {
	return strings.TrimSpace(facts.Target.HandoffID) != ""
}

func readQuestion(content string) (wordedQuestion, error) {
	answer := wordedQuestion{}
	if errorValue := json.Unmarshal([]byte(content), &answer); errorValue != nil {
		return wordedQuestion{}, errorValue
	}
	question := strings.TrimSpace(answer.Question)
	if question == "" {
		return wordedQuestion{}, errors.New("the model returned an empty approval question")
	}
	return wordedQuestion{Question: question}, nil
}

func newQuestionContext(facts holdrecord.QuestionFacts) questionContext {
	return questionContext{
		ResponseLanguage: strings.TrimSpace(facts.ResponseLanguage),
		Operation:        strings.TrimSpace(facts.Tool.Name),
		ActionDetails:    detailsForTheQuestion(facts),
		QuotedBelow:      quotedInputName(facts),
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
