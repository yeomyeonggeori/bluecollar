package intake

import (
	"context"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

// AttachmentDescriber turns image parts into short factual sentences. The
// decision model reads text, so a message whose whole content is a picture has
// nothing to decide on without one.
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
	decisionRequest := planner.buildDecisionRequest(describedRequest)
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

func (planner DecisionPlanner) buildDecisionRequest(request agentcontract.IntakeDecisionRequest) model.DecisionRequest {
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
	target, errorValue := reader.choice(questionNameTarget)
	if errorValue != nil {
		return agentcontract.IntakeMessageDecision{}, errorValue
	}
	reactionProbability := reader.answer(questionNameReaction).ChoiceProbability(reactionOptionReact)
	reactionDraw := planner.randomSource()
	decision := agentcontract.IntakeMessageDecision{
		MessageID:           strings.TrimSpace(message.MessageID),
		Addressing:          readAddressingDecision(reader, target, reactionDraw < reactionProbability),
		ReactionProbability: reactionProbability,
		ReactionDraw:        reactionDraw,
		TurnFields:          readTurnFields(request, reader),
		Attachments:         message.Attachments,
	}
	if _, hasAnswer := reader.answers[reader.messageKey+"."+questionNameRelatesToActiveTask]; hasAnswer {
		decision.HasRelatesToActiveTask = true
		decision.RelatesToActiveTask = reader.answer(questionNameRelatesToActiveTask).IsYes()
	}
	return decision, nil
}

func readAddressingDecision(reader answerReader, target string, isReacting bool) agentcontract.AddressingDecision {
	decision := agentcontract.AddressingDecision{
		Target:        agentcontract.AddressingTarget(target),
		ShouldRespond: reader.answer(questionNameShouldRespond).IsYes(),
	}
	if isReacting {
		decision.ReactionEmoji = normalizeAddressingReactionEmoji(reader.answer(questionNameReactionEmoji).Choice)
	}
	dutyAnswer := reader.answer(questionNameDuty)
	if duty, isDuty := agentcontract.StandingDutyByName(dutyAnswer.Choice); isDuty {
		decision.DutyMatch = true
		decision.DutyName = duty.Name
		decision.DutyConfidence = normalizedDutyConfidence(dutyAnswer.Confidence)
	}
	if decision.Target == agentcontract.AddressingTargetHuman {
		decision.ShouldRespond = false
	}
	return decision
}

func readTurnFields(request agentcontract.IntakeDecisionRequest, reader answerReader) agentcontract.TurnDecision {
	turnFields := agentcontract.TurnDecision{
		Route:                  agentcontract.TurnRoute(reader.answer(questionNameRoute).Choice),
		Classification:         agentcontract.IntakeClassification(reader.answer(questionNameClassification).Choice),
		TaskShape:              agentcontract.TaskShape(reader.answer(questionNameTaskShape).Choice),
		TaskLevel:              agentcontract.TaskLevel(reader.answer(questionNameLevel).Choice),
		DeliverableKind:        agentcontract.DeliverableKind(reader.answer(questionNameDeliverableKind).Choice),
		ResponseLanguage:       reader.answer(questionNameResponseLanguage).Choice,
		PriorTaskReference:     agentcontract.PriorTaskReference(reader.answer(questionNamePriorTaskReference).Choice),
		RequestedOutputFormats: reader.yesMembers(questionPrefixFormat, requestedOutputFormatNames),
		InitialToolNames:       reader.yesMembers(questionPrefixTool, resolveCallableToolNames(request)),
	}
	if strings.TrimSpace(request.PendingConfirmation.TaskRunID) != "" {
		approval := agentcontract.ApprovalSignal(reader.answer(questionNameApproval).Choice)
		turnFields.Approval = &approval
	}
	if strings.TrimSpace(request.ActiveTask.TaskRunID) != "" {
		turnFields.BusyRoute = agentcontract.BusyRoute(reader.answer(questionNameBusyRoute).Choice)
	}
	turnFields.Choices = readChoiceSelections(request, reader)
	return turnFields
}

func readChoiceSelections(request agentcontract.IntakeDecisionRequest, reader answerReader) []string {
	choiceKeys := decisionChoiceKeys(request.PendingChoice)
	selections := []string{}
	for index, choiceKey := range choiceKeys {
		if reader.answer(questionPrefixChoice + strconv.Itoa(index+1)).IsYes() {
			selections = append(selections, choiceKey)
		}
	}
	if len(selections) == 0 {
		return nil
	}
	return selections
}

type answerReader struct {
	answers    map[string]model.DecisionAnswer
	messageKey string
}

func (reader answerReader) answer(questionName string) model.DecisionAnswer {
	return reader.answers[reader.messageKey+"."+questionName]
}

func (reader answerReader) choice(questionName string) (string, error) {
	answer, isAnswered := reader.answers[reader.messageKey+"."+questionName]
	if !isAnswered {
		return "", errors.New("intake decision is missing an answer for " + reader.messageKey + "." + questionName)
	}
	choice := strings.TrimSpace(answer.Choice)
	if choice == "" {
		return "", errors.New("intake decision answered " + reader.messageKey + "." + questionName + " with no choice")
	}
	return choice, nil
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
