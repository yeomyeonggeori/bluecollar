package intaketest

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type Outcome struct {
	TurnDecision      turnclassification.TurnDecision
	ToolProbabilities map[string]float64
	WorkProbabilities map[string]float64
}

type DecisionModel struct {
	Outcome    Outcome
	OutcomeFor func(messageKey string) Outcome
	ModelName  string
	Error      error

	mutex    sync.Mutex
	requests []model.DecisionRequest
}

func NewDecisionModel(outcome Outcome) *DecisionModel {
	return &DecisionModel{Outcome: outcome}
}

func (decisionModel *DecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	decisionModel.requests = append(decisionModel.requests, request)
	decisionModel.mutex.Unlock()
	if decisionModel.Error != nil {
		return model.DecisionResponse{}, decisionModel.Error
	}
	return model.DecisionResponse{
		Answers:   Answers(request.Questions, decisionModel.outcomeFor),
		ModelName: decisionModel.ModelName,
	}, nil
}

func (decisionModel *DecisionModel) Requests() []model.DecisionRequest {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	return append([]model.DecisionRequest{}, decisionModel.requests...)
}

func (decisionModel *DecisionModel) outcomeFor(messageKey string) Outcome {
	if decisionModel.OutcomeFor == nil {
		return decisionModel.Outcome
	}
	return decisionModel.OutcomeFor(messageKey)
}

func Answers(questions map[string]model.DecisionQuestion, outcomeFor func(messageKey string) Outcome) map[string]model.DecisionAnswer {
	answers, _ := AnswersAndUnknownQuestions(questions, outcomeFor)
	return answers
}

func AnswersAndUnknownQuestions(questions map[string]model.DecisionQuestion, outcomeFor func(messageKey string) Outcome) (map[string]model.DecisionAnswer, []string) {
	answers := map[string]model.DecisionAnswer{}
	unknownQuestionNames := []string{}
	for questionName, question := range questions {
		messageKey, shortName := splitQuestionName(questionName)
		answer, isKnown := answerFor(shortName, question, outcomeFor(messageKey))
		answers[questionName] = answer
		if !isKnown {
			unknownQuestionNames = append(unknownQuestionNames, questionName)
		}
	}
	sort.Strings(unknownQuestionNames)
	return answers, unknownQuestionNames
}

func splitQuestionName(questionName string) (string, string) {
	separatorIndex := strings.Index(questionName, ".")
	if separatorIndex < 0 {
		return "", questionName
	}
	return questionName[:separatorIndex], questionName[separatorIndex+1:]
}

func answerFor(shortName string, question model.DecisionQuestion, outcome Outcome) (model.DecisionAnswer, bool) {
	if strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixTool) {
		return toolAnswer(strings.TrimPrefix(shortName, agentcontract.IntakeQuestionPrefixTool), outcome), true
	}
	if strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixFormat) {
		return noulAnswer(containsValue(outcome.TurnDecision.RequestedOutputFormats, strings.TrimPrefix(shortName, agentcontract.IntakeQuestionPrefixFormat))), true
	}
	if answer, isKnown := namedAnswer(shortName, outcome); isKnown {
		return answer, true
	}
	return model.DecisionAnswer{Type: question.Type}, false
}

func namedAnswer(shortName string, outcome Outcome) (model.DecisionAnswer, bool) {
	decision := outcome.TurnDecision
	switch shortName {
	case agentcontract.IntakeQuestionWork:
		return workAnswer(outcome), true
	case agentcontract.IntakeQuestionRelation:
		return choiceAnswer(scriptedRelation(decision)), true
	case agentcontract.IntakeQuestionClarify:
		return noulAnswer(scriptedClassification(decision) == agentcontract.IntakeClassificationNeedsConfirmation), true
	case agentcontract.IntakeQuestionExpectedToolCount:
		return choiceAnswer(string(scriptedWorkToolCount(decision))), true
	case agentcontract.IntakeQuestionSingleToolChoice:
		return choiceAnswer(firstScriptedToolName(decision)), true
	case agentcontract.IntakeQuestionIsExternalSendRequested:
		return noulAnswer(decision.IsExternalSendRequested), true
	case agentcontract.IntakeQuestionTaskShape:
		return choiceAnswer(string(scriptedWorkTaskShape(decision))), true
	case agentcontract.IntakeQuestionDeliverableKind:
		return choiceAnswer(orDefault(string(decision.DeliverableKind), string(agentcontract.DeliverableKindNone))), true
	case agentcontract.IntakeQuestionResponseLanguage:
		return choiceAnswer(orDefault(decision.ResponseLanguage, "other")), true
	case agentcontract.IntakeQuestionPriorTaskReference:
		return choiceAnswer(orDefault(string(decision.PriorTaskReference), string(agentcontract.PriorTaskReferenceNone))), true
	}
	return model.DecisionAnswer{}, false
}

func workAnswer(outcome Outcome) model.DecisionAnswer {
	answer := choiceAnswer(string(ScriptedWork(outcome.TurnDecision)))
	if len(outcome.WorkProbabilities) > 0 {
		answer.Probabilities = outcome.WorkProbabilities
	}
	return answer
}

func ScriptedWork(decision turnclassification.TurnDecision) agentcontract.Work {
	switch scriptedClassification(decision) {
	case agentcontract.IntakeClassificationQuickReply:
		return agentcontract.WorkNone
	case agentcontract.IntakeClassificationUnsupported:
		return agentcontract.WorkImpossible
	}
	switch agentcontract.NormalizeTaskLevel(string(decision.TaskLevel)) {
	case agentcontract.TaskLevelMedium:
		return agentcontract.WorkNormal
	case agentcontract.TaskLevelHigh, agentcontract.TaskLevelXHigh, agentcontract.TaskLevelMax:
		return agentcontract.WorkHard
	default:
		return agentcontract.WorkEasy
	}
}

func scriptedClassification(decision turnclassification.TurnDecision) agentcontract.IntakeClassification {
	if classification := agentcontract.NormalizeIntakeClassification(decision.Classification); classification != "" {
		return classification
	}
	switch decision.Route {
	case agentcontract.TurnRouteClarify:
		return agentcontract.IntakeClassificationNeedsConfirmation
	case agentcontract.TurnRouteGiveUp:
		return agentcontract.IntakeClassificationUnsupported
	case agentcontract.TurnRouteAnswerQuestion, agentcontract.TurnRouteAnswerMeta, "":
		return agentcontract.IntakeClassificationQuickReply
	}
	if scriptedExpectedToolCount(decision) == agentcontract.ExpectedToolCountNone {
		return agentcontract.IntakeClassificationQuickReply
	}
	return agentcontract.IntakeClassificationBoundedTask
}

func scriptedRelation(decision turnclassification.TurnDecision) string {
	switch decision.Route {
	case agentcontract.TurnRouteContinueTask, agentcontract.TurnRouteReviseTask:
		return string(decision.Route)
	default:
		return string(agentcontract.TurnRouteStartTask)
	}
}

func scriptedWorkToolCount(decision turnclassification.TurnDecision) agentcontract.ExpectedToolCount {
	if toolCount := scriptedExpectedToolCount(decision); toolCount != agentcontract.ExpectedToolCountNone {
		return toolCount
	}
	return agentcontract.ExpectedToolCountOne
}

func scriptedWorkTaskShape(decision turnclassification.TurnDecision) agentcontract.TaskShape {
	if scriptedClassification(decision) == agentcontract.IntakeClassificationNeedsConfirmation {
		return agentcontract.TaskShapeApprovalGatedTask
	}
	switch decision.TaskShape {
	case "", agentcontract.TaskShapeImmediateReply:
		return agentcontract.TaskShapeMaintenanceTask
	default:
		return decision.TaskShape
	}
}

func orDefault(value string, defaultValue string) string {
	if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
		return trimmedValue
	}
	return defaultValue
}

func choiceAnswer(choice string) model.DecisionAnswer {
	trimmedChoice := strings.TrimSpace(choice)
	return model.DecisionAnswer{
		Type:          model.DecisionQuestionTypeChoice,
		Choice:        trimmedChoice,
		Probabilities: map[string]float64{trimmedChoice: 1},
		Confidence:    1,
	}
}

func toolAnswer(toolName string, outcome Outcome) model.DecisionAnswer {
	if probability, isGiven := outcome.ToolProbabilities[toolName]; isGiven {
		return model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Noul: probability}
	}
	return noulAnswer(containsValue(outcome.TurnDecision.InitialToolNames, toolName))
}

func noulAnswer(isYes bool) model.DecisionAnswer {
	if isYes {
		return model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Noul: 1}
	}
	return model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Noul: 0}
}

func containsValue(values []string, value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func firstScriptedToolName(decision turnclassification.TurnDecision) string {
	if len(decision.InitialToolNames) == 0 {
		return agentcontract.IntakeChoiceOptionNone
	}
	return decision.InitialToolNames[0]
}

func scriptedExpectedToolCount(decision turnclassification.TurnDecision) agentcontract.ExpectedToolCount {
	if decision.ExpectedToolCount != "" {
		return decision.ExpectedToolCount
	}
	if decision.Classification == agentcontract.IntakeClassificationBoundedTask {
		return agentcontract.ExpectedToolCountSeveral
	}
	return agentcontract.ExpectedToolCountNone
}
