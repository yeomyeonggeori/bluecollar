package intake

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/bluecollar/llmcalls"
	"github.com/yeomyeonggeori/bluecollar/messageimages"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type AttachmentDescriber interface {
	DescribeAttachments(context.Context, []agentcontract.AgentPart) ([]string, error)
}

type DecisionPlanner struct {
	decisionModel       model.DecisionModel
	attachmentDescriber AttachmentDescriber
	callCost            modelCallCost
}

var ErrDecisionModelUnavailable = errors.New("intake decision model unavailable")

func NewDecisionPlanner(decisionModel model.DecisionModel, attachmentDescriber AttachmentDescriber) DecisionPlanner {
	return DecisionPlanner{decisionModel: decisionModel, attachmentDescriber: attachmentDescriber, callCost: newModelCallCost()}
}

func (planner DecisionPlanner) Decide(ctx context.Context, request turnclassification.IntakeDecisionRequest, callLedger *llmcalls.IntakeCallLedger) (turnclassification.IntakeDecisions, error) {
	if planner.decisionModel == nil {
		return turnclassification.IntakeDecisions{}, ErrDecisionModelUnavailable
	}
	if len(request.Messages) == 0 {
		return turnclassification.IntakeDecisions{}, errors.New("intake decision request carries no message")
	}
	describedRequest, hasDescribedAttachments := planner.describeAttachmentsOnlyMessages(ctx, request)
	calls := planner.decideEveryRequest(ctx, requestsWithQuestions(buildIntakeDecisionRequest(describedRequest)))
	callError := firstCallError(calls)
	answers := mergedDecisionAnswers(calls)
	decisions, readError := planner.readDecisions(describedRequest, answers, callError)
	recordDecisionCalls(observerOf(callLedger), calls, decisionCallContext{
		errorValue:           firstError(callError, readError),
		decisions:            decisions,
		messageIDs:           decidedMessageIDs(describedRequest.Messages),
		attachmentsDescribed: hasDescribedAttachments,
		input:                decisionInput(request),
	})
	if callError != nil {
		return turnclassification.IntakeDecisions{}, callError
	}
	if readError != nil {
		return turnclassification.IntakeDecisions{}, readError
	}
	return planner.withLikelyTools(ctx, describedRequest, decisions, callLedger), nil
}

type decisionCall struct {
	request      model.DecisionRequest
	response     model.DecisionResponse
	wireExchange *model.WireExchange
	latency      time.Duration
	wasCut       bool
	errorValue   error
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
			wireContext, wireCapture := model.WithWireCapture(callContext)
			response, wasCut, errorValue := planner.decidePatiently(wireContext, request)
			calls[index] = decisionCall{request: request, response: response, wireExchange: wireCapture.Exchange(), latency: time.Since(startedAt), wasCut: wasCut, errorValue: errorValue}
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

func decisionRequestByteCount(request model.DecisionRequest) int {
	document, errorValue := json.Marshal(request)
	if errorValue != nil {
		return math.MaxInt
	}
	return len(document)
}

func buildIntakeDecisionRequest(request turnclassification.IntakeDecisionRequest) model.DecisionRequest {
	return model.DecisionRequest{
		State:     buildDecisionState(request, nil),
		Questions: newQuestionBuilder(request).questionsWithoutTools(),
	}
}

func requestsWithQuestions(requests ...model.DecisionRequest) []model.DecisionRequest {
	asking := []model.DecisionRequest{}
	for _, request := range requests {
		if len(request.Questions) > 0 {
			asking = append(asking, request)
		}
	}
	return asking
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

func resolveCallableToolNames(request turnclassification.IntakeDecisionRequest) []string {
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

func (planner DecisionPlanner) describeAttachmentsOnlyMessages(ctx context.Context, request turnclassification.IntakeDecisionRequest) (turnclassification.IntakeDecisionRequest, bool) {
	if planner.attachmentDescriber == nil {
		return request, false
	}
	describedMessages := append([]turnclassification.IntakeDecisionMessage{}, request.Messages...)
	hasDescription := false
	for index, message := range describedMessages {
		if !messageNeedsAttachmentDescription(message) {
			continue
		}
		descriptions, errorValue := planner.attachmentDescriber.DescribeAttachments(ctx, messageimages.ImagePartsOf(message.InputParts))
		if errorValue != nil || len(descriptions) == 0 {
			continue
		}
		describedMessages[index].Attachments = withAttachmentDescriptions(message.Attachments, descriptions)
		hasDescription = true
	}
	request.Messages = describedMessages
	return request, hasDescription
}

func messageNeedsAttachmentDescription(message turnclassification.IntakeDecisionMessage) bool {
	if strings.TrimSpace(message.Prompt) != "" {
		return false
	}
	return len(messageimages.ImagePartsOf(message.InputParts)) > 0
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

func (planner DecisionPlanner) readDecisions(request turnclassification.IntakeDecisionRequest, answers map[string]model.DecisionAnswer, callError error) (turnclassification.IntakeDecisions, error) {
	if callError != nil {
		return turnclassification.IntakeDecisions{}, nil
	}
	decisions := turnclassification.IntakeDecisions{}
	for index, message := range request.Messages {
		reader := answerReader{answers: answers, messageKey: decisionMessageKey(index)}
		decision, errorValue := planner.readMessageDecision(request, message, reader)
		if errorValue != nil {
			return turnclassification.IntakeDecisions{}, errorValue
		}
		decisions.Messages = append(decisions.Messages, decision)
	}
	return decisions, nil
}

func (planner DecisionPlanner) readMessageDecision(request turnclassification.IntakeDecisionRequest, message turnclassification.IntakeDecisionMessage, reader answerReader) (turnclassification.IntakeMessageDecision, error) {
	turnFields, errorValue := readTurnFields(request, reader)
	if errorValue != nil {
		return turnclassification.IntakeMessageDecision{}, errorValue
	}
	return turnclassification.IntakeMessageDecision{
		MessageID:   strings.TrimSpace(message.MessageID),
		TurnFields:  turnFields,
		Attachments: message.Attachments,
	}, nil
}

func readTurnFields(request turnclassification.IntakeDecisionRequest, reader answerReader) (turnclassification.TurnDecision, error) {
	work, errorValue := readDecidedWork(request, reader)
	if errorValue != nil {
		return turnclassification.TurnDecision{}, errorValue
	}
	responseLanguage, errorValue := readResponseLanguage(request, reader)
	if errorValue != nil {
		return turnclassification.TurnDecision{}, errorValue
	}
	switch work {
	case agentcontract.WorkNone:
		return wordsTurn(agentcontract.TurnRouteAnswerQuestion, agentcontract.IntakeClassificationQuickReply, responseLanguage), nil
	case agentcontract.WorkImpossible:
		return wordsTurn(agentcontract.TurnRouteGiveUp, agentcontract.IntakeClassificationUnsupported, responseLanguage), nil
	}
	turnFields, errorValue := readWorkTurn(request, reader, work)
	turnFields.ResponseLanguage = responseLanguage
	return turnFields, errorValue
}

func readDecidedWork(request turnclassification.IntakeDecisionRequest, reader answerReader) (agentcontract.Work, error) {
	if request.DecidedWork != "" {
		return request.DecidedWork, nil
	}
	answer, errorValue := reader.choiceAnswer(agentcontract.IntakeQuestionWork)
	if errorValue != nil {
		return "", errorValue
	}
	if isWorkKnownToExist(request, reader) {
		return agentcontract.ReadDoableWork(answer), nil
	}
	if work := agentcontract.ReadWork(answer); work != "" {
		return work, nil
	}
	return "", errors.New("intake decision answered " + reader.questionKey(agentcontract.IntakeQuestionWork) + " with an unknown work")
}

func isWorkKnownToExist(request turnclassification.IntakeDecisionRequest, reader answerReader) bool {
	if !request.ScheduledRun.IsEmpty() {
		return true
	}
	if !hasActiveGoal(request) {
		return false
	}
	relation, errorValue := reader.choice(agentcontract.IntakeQuestionRelation)
	return errorValue == nil && agentcontract.TurnRoute(relation) != agentcontract.TurnRouteStartTask
}

func readResponseLanguage(request turnclassification.IntakeDecisionRequest, reader answerReader) (string, error) {
	if !needsResponseLanguage(request) {
		return "", nil
	}
	return reader.choice(agentcontract.IntakeQuestionResponseLanguage)
}

func wordsTurn(route agentcontract.TurnRoute, classification agentcontract.IntakeClassification, responseLanguage string) turnclassification.TurnDecision {
	return turnclassification.TurnDecision{
		Route:              route,
		Classification:     classification,
		ExpectedToolCount:  agentcontract.ExpectedToolCountNone,
		TaskShape:          agentcontract.TaskShapeImmediateReply,
		TaskLevel:          agentcontract.TaskLevelLow,
		DeliverableKind:    agentcontract.DeliverableKindNone,
		ResponseLanguage:   responseLanguage,
		PriorTaskReference: agentcontract.PriorTaskReferenceNone,
	}
}

func readWorkTurn(request turnclassification.IntakeDecisionRequest, reader answerReader, work agentcontract.Work) (turnclassification.TurnDecision, error) {
	choiceNames := []string{agentcontract.IntakeQuestionExpectedToolCount, agentcontract.IntakeQuestionTaskShape, agentcontract.IntakeQuestionDeliverableKind}
	if hasActiveGoal(request) {
		choiceNames = append(choiceNames, agentcontract.IntakeQuestionRelation)
	}
	if hasPriorTask(request) {
		choiceNames = append(choiceNames, agentcontract.IntakeQuestionPriorTaskReference)
	}
	choices, errorValue := reader.choices(choiceNames)
	if errorValue != nil {
		return turnclassification.TurnDecision{}, errorValue
	}
	needsClarification, errorValue := readNeedsClarification(request, reader)
	if errorValue != nil {
		return turnclassification.TurnDecision{}, errorValue
	}
	isExternalSendRequested, errorValue := reader.noul(agentcontract.IntakeQuestionIsExternalSendRequested)
	if errorValue != nil {
		return turnclassification.TurnDecision{}, errorValue
	}
	turnFields := turnclassification.TurnDecision{
		Route:                   workRoute(choices),
		Classification:          agentcontract.IntakeClassificationBoundedTask,
		HasIndependentWork:      !needsClarification,
		ExpectedToolCount:       agentcontract.ExpectedToolCount(choices[agentcontract.IntakeQuestionExpectedToolCount]),
		TaskShape:               agentcontract.TaskShape(choices[agentcontract.IntakeQuestionTaskShape]),
		TaskLevel:               work.TaskLevel(),
		DeliverableKind:         agentcontract.DeliverableKind(choices[agentcontract.IntakeQuestionDeliverableKind]),
		IsExternalSendRequested: isExternalSendRequested,
		PriorTaskReference:      agentcontract.PriorTaskReferenceNone,
	}
	if needsClarification {
		turnFields.Route = agentcontract.TurnRouteClarify
		turnFields.Classification = agentcontract.IntakeClassificationNeedsConfirmation
	}
	if priorTaskChoice, isAsked := choices[agentcontract.IntakeQuestionPriorTaskReference]; isAsked {
		turnFields.PriorTaskReference = agentcontract.PriorTaskReference(priorTaskChoice)
	}
	turnFields.RequestedOutputFormats, errorValue = reader.yesMembers(agentcontract.IntakeQuestionPrefixFormat, turnclassification.RequestedOutputFormatNames)
	return turnFields, errorValue
}

func readNeedsClarification(request turnclassification.IntakeDecisionRequest, reader answerReader) (bool, error) {
	if !canClarify(request) {
		return false, nil
	}
	return reader.noul(agentcontract.IntakeQuestionClarify)
}

func workRoute(choices map[string]string) agentcontract.TurnRoute {
	if relation, isAsked := choices[agentcontract.IntakeQuestionRelation]; isAsked {
		return agentcontract.TurnRoute(relation)
	}
	return agentcontract.TurnRouteStartTask
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
