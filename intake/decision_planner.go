package intake

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type AttachmentDescriber interface {
	DescribeAttachments(context.Context, []agentcontract.AgentPart) ([]string, error)
}

type DecisionPlanner struct {
	decisionModel       model.DecisionModel
	attachmentDescriber AttachmentDescriber
	randomSource        func() float64
}

var ErrDecisionModelUnavailable = errors.New("intake decision model unavailable")

func NewDecisionPlanner(decisionModel model.DecisionModel, attachmentDescriber AttachmentDescriber, randomSource func() float64) DecisionPlanner {
	if randomSource == nil {
		randomSource = rand.Float64
	}
	return DecisionPlanner{decisionModel: decisionModel, attachmentDescriber: attachmentDescriber, randomSource: randomSource}
}

func (planner DecisionPlanner) Decide(ctx context.Context, request agentcontract.IntakeDecisionRequest, callLedger *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	if planner.decisionModel == nil {
		return agentcontract.IntakeDecisions{}, ErrDecisionModelUnavailable
	}
	if len(request.Messages) == 0 {
		return agentcontract.IntakeDecisions{}, errors.New("intake decision request carries no message")
	}
	describedRequest, hasDescribedAttachments := planner.describeAttachmentsOnlyMessages(ctx, request)
	decisionRequest := buildDecisionRequest(describedRequest)
	startedAt := time.Now()
	response, errorValue := planner.decisionModel.Decide(ctx, decisionRequest)
	latency := time.Since(startedAt)
	decisions, readError := planner.readDecisions(describedRequest, response, errorValue)
	recordDecisionCall(callLedger, decisionCallRecord{
		request:              decisionRequest,
		response:             response,
		latency:              latency,
		errorValue:           firstError(errorValue, readError),
		decisions:            decisions,
		messageCount:         len(describedRequest.Messages),
		attachmentsDescribed: hasDescribedAttachments,
	})
	if errorValue != nil {
		return agentcontract.IntakeDecisions{}, errorValue
	}
	if readError != nil {
		return agentcontract.IntakeDecisions{}, readError
	}
	return decisions, nil
}

func firstError(errorValues ...error) error {
	for _, errorValue := range errorValues {
		if errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func DecisionRequestByteCount(request agentcontract.IntakeDecisionRequest) int {
	document, errorValue := json.Marshal(buildDecisionRequest(request))
	if errorValue != nil {
		return math.MaxInt
	}
	return len(document)
}

func buildDecisionRequest(request agentcontract.IntakeDecisionRequest) model.DecisionRequest {
	toolNames := resolveCallableToolNames(request)
	builderRequest := request
	builderRequest.CallableToolNames = toolNames
	return model.DecisionRequest{
		State:     buildDecisionState(builderRequest, decisionToolDescriptions(request.ToolSet, toolNames)),
		Questions: newQuestionBuilder(builderRequest).questions(),
	}
}

func resolveCallableToolNames(request agentcontract.IntakeDecisionRequest) []string {
	if len(request.CallableToolNames) > 0 {
		return request.CallableToolNames
	}
	if request.ToolSet == nil {
		return []string{}
	}
	return turnRouterCallableToolNames(agentcontract.AgentRequest{ToolSet: request.ToolSet})
}

func (planner DecisionPlanner) describeAttachmentsOnlyMessages(ctx context.Context, request agentcontract.IntakeDecisionRequest) (agentcontract.IntakeDecisionRequest, bool) {
	if planner.attachmentDescriber == nil {
		return request, false
	}
	describedMessages := append([]agentcontract.IntakeDecisionMessage{}, request.Messages...)
	hasDescription := false
	for index, message := range describedMessages {
		if !messageNeedsAttachmentDescription(message) {
			continue
		}
		descriptions, errorValue := planner.attachmentDescriber.DescribeAttachments(ctx, agentcontract.ImagePartsOf(message.InputParts))
		if errorValue != nil || len(descriptions) == 0 {
			continue
		}
		describedMessages[index].Attachments = withAttachmentDescriptions(message.Attachments, descriptions)
		hasDescription = true
	}
	request.Messages = describedMessages
	return request, hasDescription
}

func messageNeedsAttachmentDescription(message agentcontract.IntakeDecisionMessage) bool {
	if strings.TrimSpace(message.Prompt) != "" {
		return false
	}
	return len(agentcontract.ImagePartsOf(message.InputParts)) > 0
}

func withAttachmentDescriptions(facts []agentcontract.IntakeAttachmentFact, descriptions []string) []agentcontract.IntakeAttachmentFact {
	describedFacts := append([]agentcontract.IntakeAttachmentFact{}, facts...)
	descriptionIndex := 0
	for index, fact := range describedFacts {
		if fact.Kind != "image" || descriptionIndex >= len(descriptions) {
			continue
		}
		describedFacts[index].Description = strings.TrimSpace(descriptions[descriptionIndex])
		descriptionIndex++
	}
	return describedFacts
}

func (planner DecisionPlanner) readDecisions(request agentcontract.IntakeDecisionRequest, response model.DecisionResponse, callError error) (agentcontract.IntakeDecisions, error) {
	if callError != nil {
		return agentcontract.IntakeDecisions{}, nil
	}
	decisions := agentcontract.IntakeDecisions{}
	for index, message := range request.Messages {
		reader := answerReader{answers: response.Answers, messageKey: decisionMessageKey(index)}
		decision, errorValue := planner.readMessageDecision(request, message, reader)
		if errorValue != nil {
			return agentcontract.IntakeDecisions{}, errorValue
		}
		decisions.Messages = append(decisions.Messages, decision)
	}
	return decisions, nil
}

func (planner DecisionPlanner) readMessageDecision(request agentcontract.IntakeDecisionRequest, message agentcontract.IntakeDecisionMessage, reader answerReader) (agentcontract.IntakeMessageDecision, error) {
	reactionAnswer, errorValue := reader.choiceAnswer(agentcontract.IntakeQuestionReaction)
	if errorValue != nil {
		return agentcontract.IntakeMessageDecision{}, errorValue
	}
	reactionProbability := reactionAnswer.ChoiceProbability(agentcontract.IntakeReactionOptionReact)
	reactionDraw := planner.randomSource()
	addressing, errorValue := readAddressingDecision(reader, reactionDraw < reactionProbability)
	if errorValue != nil {
		return agentcontract.IntakeMessageDecision{}, errorValue
	}
	turnFields, errorValue := readTurnFields(request, reader)
	if errorValue != nil {
		return agentcontract.IntakeMessageDecision{}, errorValue
	}
	decision := agentcontract.IntakeMessageDecision{
		MessageID:           strings.TrimSpace(message.MessageID),
		Addressing:          addressing,
		ReactionProbability: reactionProbability,
		ReactionDraw:        reactionDraw,
		TurnFields:          turnFields,
		Attachments:         message.Attachments,
	}
	if _, hasAnswer := reader.answers[reader.questionKey(agentcontract.IntakeQuestionRelatesToActiveTask)]; hasAnswer {
		decision.HasRelatesToActiveTask = true
		decision.RelatesToActiveTask = reader.answer(agentcontract.IntakeQuestionRelatesToActiveTask).IsYes()
	}
	return decision, nil
}

func readAddressingDecision(reader answerReader, isReacting bool) (agentcontract.AddressingDecision, error) {
	target, errorValue := reader.choice(agentcontract.IntakeQuestionTarget)
	if errorValue != nil {
		return agentcontract.AddressingDecision{}, errorValue
	}
	shouldRespond, errorValue := reader.noul(agentcontract.IntakeQuestionShouldRespond)
	if errorValue != nil {
		return agentcontract.AddressingDecision{}, errorValue
	}
	decision := agentcontract.AddressingDecision{
		Target:        agentcontract.AddressingTarget(target),
		ShouldRespond: shouldRespond,
	}
	if isReacting {
		reactionEmoji, errorValue := reader.choice(agentcontract.IntakeQuestionReactionEmoji)
		if errorValue != nil {
			return agentcontract.AddressingDecision{}, errorValue
		}
		decision.ReactionEmoji = normalizeAddressingReactionEmoji(reactionEmoji)
	}
	dutyAnswer, errorValue := reader.choiceAnswer(agentcontract.IntakeQuestionDuty)
	if errorValue != nil {
		return agentcontract.AddressingDecision{}, errorValue
	}
	if duty, isDuty := agentcontract.StandingDutyByName(dutyAnswer.Choice); isDuty {
		decision.DutyMatch = true
		decision.DutyName = duty.Name
		decision.DutyConfidence = normalizedDutyConfidence(dutyAnswer.Confidence)
	}
	if decision.Target == agentcontract.AddressingTargetHuman {
		decision.ShouldRespond = false
	}
	return decision, nil
}

func readTurnFields(request agentcontract.IntakeDecisionRequest, reader answerReader) (agentcontract.TurnDecision, error) {
	choiceNames := []string{agentcontract.IntakeQuestionRoute, agentcontract.IntakeQuestionClassification, agentcontract.IntakeQuestionTaskShape, agentcontract.IntakeQuestionLevel, agentcontract.IntakeQuestionDeliverableKind, agentcontract.IntakeQuestionResponseLanguage, agentcontract.IntakeQuestionPriorTaskReference}
	if strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" {
		choiceNames = append(choiceNames, agentcontract.IntakeQuestionApproval)
	}
	if strings.TrimSpace(request.ActiveTask.TaskRunID) != "" {
		choiceNames = append(choiceNames, agentcontract.IntakeQuestionBusyRoute)
	}
	choices, errorValue := reader.choices(choiceNames)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	turnFields := agentcontract.TurnDecision{
		Route:                  agentcontract.TurnRoute(choices[agentcontract.IntakeQuestionRoute]),
		Classification:         agentcontract.IntakeClassification(choices[agentcontract.IntakeQuestionClassification]),
		TaskShape:              agentcontract.TaskShape(choices[agentcontract.IntakeQuestionTaskShape]),
		TaskLevel:              agentcontract.TaskLevel(choices[agentcontract.IntakeQuestionLevel]),
		DeliverableKind:        agentcontract.DeliverableKind(choices[agentcontract.IntakeQuestionDeliverableKind]),
		ResponseLanguage:       choices[agentcontract.IntakeQuestionResponseLanguage],
		PriorTaskReference:     agentcontract.PriorTaskReference(choices[agentcontract.IntakeQuestionPriorTaskReference]),
		RequestedOutputFormats: reader.yesMembers(agentcontract.IntakeQuestionPrefixFormat, agentcontract.RequestedOutputFormatNames),
		InitialToolNames:       reader.yesMembers(agentcontract.IntakeQuestionPrefixTool, resolveCallableToolNames(request)),
	}
	if approvalChoice, isAsked := choices[agentcontract.IntakeQuestionApproval]; isAsked {
		approval := agentcontract.ApprovalSignal(approvalChoice)
		turnFields.Approval = &approval
	}
	if busyRouteChoice, isAsked := choices[agentcontract.IntakeQuestionBusyRoute]; isAsked {
		turnFields.BusyRoute = agentcontract.BusyRoute(busyRouteChoice)
	}
	selections, errorValue := readChoiceSelections(request, reader)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	turnFields.Choices = selections
	return turnFields, nil
}

func readChoiceSelections(request agentcontract.IntakeDecisionRequest, reader answerReader) ([]string, error) {
	choiceKeys := decisionChoiceKeys(request.PendingChoice)
	if len(choiceKeys) == 0 {
		return nil, nil
	}
	if isMultipleChoiceSelection(request.PendingChoice) {
		return reader.yesMembers(agentcontract.IntakeQuestionPrefixChoice, choiceKeys), nil
	}
	selectedKey, errorValue := reader.choice(agentcontract.IntakeQuestionChoice)
	if errorValue != nil {
		return nil, errorValue
	}
	for _, choiceKey := range choiceKeys {
		if choiceKey == selectedKey {
			return []string{choiceKey}, nil
		}
	}
	return nil, nil
}

type answerReader struct {
	answers    map[string]model.DecisionAnswer
	messageKey string
}

func (reader answerReader) questionKey(questionName string) string {
	return reader.messageKey + "." + questionName
}

func (reader answerReader) answer(questionName string) model.DecisionAnswer {
	return reader.answers[reader.questionKey(questionName)]
}

func (reader answerReader) choiceAnswer(questionName string) (model.DecisionAnswer, error) {
	answer, isAnswered := reader.answers[reader.questionKey(questionName)]
	if !isAnswered {
		return model.DecisionAnswer{}, errors.New("intake decision is missing an answer for " + reader.questionKey(questionName))
	}
	if strings.TrimSpace(answer.Choice) == "" {
		return model.DecisionAnswer{}, errors.New("intake decision answered " + reader.questionKey(questionName) + " with no choice")
	}
	return answer, nil
}

func (reader answerReader) choice(questionName string) (string, error) {
	answer, errorValue := reader.choiceAnswer(questionName)
	if errorValue != nil {
		return "", errorValue
	}
	return strings.TrimSpace(answer.Choice), nil
}

func (reader answerReader) choices(questionNames []string) (map[string]string, error) {
	choices := map[string]string{}
	for _, questionName := range questionNames {
		choice, errorValue := reader.choice(questionName)
		if errorValue != nil {
			return nil, errorValue
		}
		choices[questionName] = choice
	}
	return choices, nil
}

func (reader answerReader) noul(questionName string) (bool, error) {
	answer, isAnswered := reader.answers[reader.questionKey(questionName)]
	if !isAnswered {
		return false, errors.New("intake decision is missing an answer for " + reader.questionKey(questionName))
	}
	return answer.IsYes(), nil
}

func (reader answerReader) yesMembers(questionPrefix string, memberNames []string) []string {
	members := []string{}
	for _, memberName := range memberNames {
		if reader.answer(questionPrefix + memberName).IsYes() {
			members = append(members, memberName)
		}
	}
	if len(members) == 0 {
		return nil
	}
	return members
}
