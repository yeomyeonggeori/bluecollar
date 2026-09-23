package intake

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
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
	callCost            modelCallCost
}

var ErrDecisionModelUnavailable = errors.New("intake decision model unavailable")

func NewDecisionPlanner(decisionModel model.DecisionModel, attachmentDescriber AttachmentDescriber, randomSource func() float64) DecisionPlanner {
	if randomSource == nil {
		randomSource = rand.Float64
	}
	return DecisionPlanner{decisionModel: decisionModel, attachmentDescriber: attachmentDescriber, randomSource: randomSource, callCost: newModelCallCost()}
}

func (planner DecisionPlanner) Decide(ctx context.Context, request agentcontract.IntakeDecisionRequest, callLedger *agentcontract.IntakeCallLedger) (agentcontract.IntakeDecisions, error) {
	if planner.decisionModel == nil {
		return agentcontract.IntakeDecisions{}, ErrDecisionModelUnavailable
	}
	if len(request.Messages) == 0 {
		return agentcontract.IntakeDecisions{}, errors.New("intake decision request carries no message")
	}
	describedRequest, hasDescribedAttachments := planner.describeAttachmentsOnlyMessages(ctx, request)
	calls := planner.decideEveryRequest(ctx, []model.DecisionRequest{buildIntakeDecisionRequest(describedRequest)})
	callError := firstCallError(calls)
	answers := mergedDecisionAnswers(calls)
	decisions, readError := planner.readDecisions(describedRequest, answers, callError)
	recordDecisionCalls(callLedger, calls, decisionCallContext{
		errorValue:           firstError(callError, readError),
		decisions:            decisions,
		messageCount:         len(describedRequest.Messages),
		attachmentsDescribed: hasDescribedAttachments,
	})
	if callError != nil {
		return agentcontract.IntakeDecisions{}, callError
	}
	if readError != nil {
		return agentcontract.IntakeDecisions{}, readError
	}
	return planner.withLikelyTools(ctx, describedRequest, decisions, callLedger), nil
}

type decisionCall struct {
	request    model.DecisionRequest
	response   model.DecisionResponse
	latency    time.Duration
	wasCut     bool
	errorValue error
}

const maxConcurrentDecisionRequestCount = 4

func (planner DecisionPlanner) decideEveryRequest(ctx context.Context, requests []model.DecisionRequest) []decisionCall {
	callContext, cancelRemainingCalls := context.WithCancel(ctx)
	defer cancelRemainingCalls()
	calls := make([]decisionCall, len(requests))
	requestSlots := make(chan struct{}, maxConcurrentDecisionRequestCount)
	waitGroup := sync.WaitGroup{}
	for index, request := range requests {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			requestSlots <- struct{}{}
			defer func() { <-requestSlots }()
			if callContext.Err() != nil {
				calls[index] = decisionCall{request: request, errorValue: callContext.Err()}
				return
			}
			startedAt := time.Now()
			response, wasCut, errorValue := planner.decidePatiently(callContext, request)
			calls[index] = decisionCall{request: request, response: response, latency: time.Since(startedAt), wasCut: wasCut, errorValue: errorValue}
			if errorValue != nil {
				cancelRemainingCalls()
			}
		}()
	}
	waitGroup.Wait()
	return calls
}

func (planner DecisionPlanner) decidePatiently(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, bool, error) {
	return askPatiently(ctx, planner.callCost, func(callContext context.Context) (model.DecisionResponse, error) {
		startedAt := time.Now()
		response, errorValue := planner.decisionModel.Decide(callContext, request)
		if errorValue != nil {
			return model.DecisionResponse{}, errorValue
		}
		planner.callCost.record(response.ModelName, time.Since(startedAt))
		return response, nil
	})
}

func firstCallError(calls []decisionCall) error {
	for _, call := range calls {
		if call.errorValue != nil {
			return call.errorValue
		}
	}
	return nil
}

func mergedDecisionAnswers(calls []decisionCall) map[string]model.DecisionAnswer {
	answers := map[string]model.DecisionAnswer{}
	for _, call := range calls {
		for questionName, answer := range call.response.Answers {
			answers[questionName] = answer
		}
	}
	return answers
}

func firstError(errorValues ...error) error {
	for _, errorValue := range errorValues {
		if errorValue != nil {
			return errorValue
		}
	}
	return nil
}

const largestDecisionRequestByteCountTheModelAccepted = 198185
const decisionRequestByteBudgetTenthsOfThatCount = 9
const decisionRequestByteBudget = largestDecisionRequestByteCountTheModelAccepted * decisionRequestByteBudgetTenthsOfThatCount / 10

func DecisionRequestByteCount(request agentcontract.IntakeDecisionRequest) int {
	return decisionRequestByteCount(buildIntakeDecisionRequest(request))
}

func decisionRequestByteCount(request model.DecisionRequest) int {
	document, errorValue := json.Marshal(request)
	if errorValue != nil {
		return math.MaxInt
	}
	return len(document)
}

func buildIntakeDecisionRequest(request agentcontract.IntakeDecisionRequest) model.DecisionRequest {
	return model.DecisionRequest{
		State:     buildDecisionState(request, nil),
		Questions: newQuestionBuilder(request).questionsWithoutTools(),
	}
}

func largestDecisionRequestByteCount(requests []model.DecisionRequest) int {
	largestByteCount := 0
	for _, request := range requests {
		if byteCount := decisionRequestByteCount(request); byteCount > largestByteCount {
			largestByteCount = byteCount
		}
	}
	return largestByteCount
}

func resolveCallableToolNames(request agentcontract.IntakeDecisionRequest) []string {
	if len(request.CallableToolNames) > 0 {
		return sortedToolNames(request.CallableToolNames)
	}
	if request.ToolSet == nil {
		return []string{}
	}
	return sortedToolNames(turnRouterCallableToolNames(agentcontract.AgentRequest{ToolSet: request.ToolSet}))
}

func sortedToolNames(toolNames []string) []string {
	sortedNames := append([]string{}, toolNames...)
	sort.Strings(sortedNames)
	return sortedNames
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

func (planner DecisionPlanner) readDecisions(request agentcontract.IntakeDecisionRequest, answers map[string]model.DecisionAnswer, callError error) (agentcontract.IntakeDecisions, error) {
	if callError != nil {
		return agentcontract.IntakeDecisions{}, nil
	}
	decisions := agentcontract.IntakeDecisions{}
	for index, message := range request.Messages {
		reader := answerReader{answers: answers, messageKey: decisionMessageKey(index)}
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
	choiceNames := []string{agentcontract.IntakeQuestionRoute, agentcontract.IntakeQuestionExpectedToolCount, agentcontract.IntakeQuestionTaskShape, agentcontract.IntakeQuestionLevel, agentcontract.IntakeQuestionDeliverableKind, agentcontract.IntakeQuestionResponseLanguage}
	if hasPriorTask(request) {
		choiceNames = append(choiceNames, agentcontract.IntakeQuestionPriorTaskReference)
	}
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
	expectedToolCount := agentcontract.ExpectedToolCount(choices[agentcontract.IntakeQuestionExpectedToolCount])
	needsTool := expectedToolCount != agentcontract.ExpectedToolCountNone
	hasIndependentWork, errorValue := reader.noul(agentcontract.IntakeQuestionHasIndependentWork)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	isExternalSendRequested, errorValue := reader.noul(agentcontract.IntakeQuestionIsExternalSendRequested)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
	}
	route := agentcontract.TurnRoute(choices[agentcontract.IntakeQuestionRoute])
	classification := classificationOf(route, needsTool)
	if route == agentcontract.TurnRouteClarify && needsTool && hasIndependentWork {
		classification = agentcontract.IntakeClassificationBoundedTask
	}
	turnFields := agentcontract.TurnDecision{
		Route:                   route,
		RawDecisionRoute:        route,
		Classification:          classification,
		HasIndependentWork:      hasIndependentWork,
		ExpectedToolCount:       expectedToolCount,
		TaskShape:               agentcontract.TaskShape(choices[agentcontract.IntakeQuestionTaskShape]),
		TaskLevel:               agentcontract.TaskLevel(choices[agentcontract.IntakeQuestionLevel]),
		DeliverableKind:         agentcontract.DeliverableKind(choices[agentcontract.IntakeQuestionDeliverableKind]),
		ResponseLanguage:        choices[agentcontract.IntakeQuestionResponseLanguage],
		IsExternalSendRequested: isExternalSendRequested,
		PriorTaskReference:      agentcontract.PriorTaskReferenceNone,
	}
	if priorTaskChoice, isAsked := choices[agentcontract.IntakeQuestionPriorTaskReference]; isAsked {
		turnFields.PriorTaskReference = agentcontract.PriorTaskReference(priorTaskChoice)
	}
	turnFields.RequestedOutputFormats, errorValue = reader.yesMembers(agentcontract.IntakeQuestionPrefixFormat, agentcontract.RequestedOutputFormatNames)
	if errorValue != nil {
		return agentcontract.TurnDecision{}, errorValue
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
		return reader.yesMembers(agentcontract.IntakeQuestionPrefixChoice, choiceKeys)
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

func (reader answerReader) toolProbabilities(toolNames []string) (map[string]float64, error) {
	probabilityByToolName := map[string]float64{}
	for _, toolName := range toolNames {
		questionName := agentcontract.IntakeQuestionPrefixTool + toolName
		answer, isAnswered := reader.answers[reader.questionKey(questionName)]
		if !isAnswered {
			return nil, errors.New("intake decision is missing an answer for " + reader.questionKey(questionName))
		}
		probabilityByToolName[toolName] = answer.Noul
	}
	return probabilityByToolName, nil
}

func (reader answerReader) yesMembers(questionPrefix string, memberNames []string) ([]string, error) {
	members := []string{}
	for _, memberName := range memberNames {
		isMember, errorValue := reader.noul(questionPrefix + memberName)
		if errorValue != nil {
			return nil, errorValue
		}
		if isMember {
			members = append(members, memberName)
		}
	}
	if len(members) == 0 {
		return nil, nil
	}
	return members, nil
}
