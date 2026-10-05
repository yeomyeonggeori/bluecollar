package intaketest

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type Outcome struct {
	Addressing                           agentcontract.AddressingDecision
	ReactionProbability                  float64
	TurnDecision                         agentcontract.TurnDecision
	ToolProbabilities                    map[string]float64
	RelatesToActiveTask                  bool
	PendingChoiceKeys                    []string
	AnswersPendingChoiceWithoutSelecting bool
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
	if strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixChoice) {
		return noulAnswer(containsValue(outcome.TurnDecision.Choices, strings.TrimPrefix(shortName, agentcontract.IntakeQuestionPrefixChoice))), true
	}
	if answer, isKnown := namedAnswer(shortName, outcome); isKnown {
		return answer, true
	}
	return model.DecisionAnswer{Type: question.Type}, false
}

func namedAnswer(shortName string, outcome Outcome) (model.DecisionAnswer, bool) {
	switch shortName {
	case agentcontract.IntakeQuestionTarget:
		return choiceAnswer(addressingTargetName(outcome)), true
	case agentcontract.IntakeQuestionShouldRespond:
		return noulAnswer(outcome.Addressing.ShouldRespond), true
	case agentcontract.IntakeQuestionReaction:
		return reactionAnswer(outcome), true
	case agentcontract.IntakeQuestionReactionEmoji:
		return choiceAnswer(orDefault(outcome.Addressing.ReactionEmoji, agentcontract.DefaultReactionEmojiName)), true
	case agentcontract.IntakeQuestionDuty:
		return dutyAnswer(outcome), true
	case agentcontract.IntakeQuestionRelatesToActiveTask:
		return noulAnswer(outcome.RelatesToActiveTask), true
	case agentcontract.IntakeQuestionRoute:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.Route), string(agentcontract.TurnRouteAnswerQuestion))), true
	case agentcontract.IntakeQuestionExpectedToolCount:
		return choiceAnswer(string(scriptedExpectedToolCount(outcome.TurnDecision))), true
	case agentcontract.IntakeQuestionSingleToolChoice:
		return choiceAnswer(firstScriptedToolName(outcome.TurnDecision)), true
	case agentcontract.IntakeQuestionHasIndependentWork:
		return noulAnswer(outcome.TurnDecision.HasIndependentWork), true
	case agentcontract.IntakeQuestionIsExternalSendRequested:
		return noulAnswer(outcome.TurnDecision.IsExternalSendRequested), true
	case agentcontract.IntakeQuestionTaskShape:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.TaskShape), string(agentcontract.TaskShapeImmediateReply))), true
	case agentcontract.IntakeQuestionLevel:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.TaskLevel), string(agentcontract.TaskLevelLow))), true
	case agentcontract.IntakeQuestionDeliverableKind:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.DeliverableKind), string(agentcontract.DeliverableKindNone))), true
	case agentcontract.IntakeQuestionResponseLanguage:
		return choiceAnswer(orDefault(outcome.TurnDecision.ResponseLanguage, "other")), true
	case agentcontract.IntakeQuestionPriorTaskReference:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.PriorTaskReference), string(agentcontract.PriorTaskReferenceNone))), true
	case agentcontract.IntakeQuestionApproval:
		return choiceAnswer(approvalName(outcome)), true
	case agentcontract.IntakeQuestionBusyRoute:
		return choiceAnswer(string(outcome.TurnDecision.BusyRoute)), true
	case agentcontract.IntakeQuestionPendingAnswer:
		return choiceAnswer(pendingAnswerName(outcome)), true
	case agentcontract.IntakeQuestionChoice:
		return choiceAnswer(selectedChoiceKey(outcome)), true
	}
	return model.DecisionAnswer{}, false
}

func addressingTargetName(outcome Outcome) string {
	return orDefault(string(outcome.Addressing.Target), string(agentcontract.AddressingTargetBot))
}

func orDefault(value string, defaultValue string) string {
	if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
		return trimmedValue
	}
	return defaultValue
}

func approvalName(outcome Outcome) string {
	if outcome.TurnDecision.Approval == nil {
		return agentcontract.IntakePendingOptionOther
	}
	return string(*outcome.TurnDecision.Approval)
}

func reactionAnswer(outcome Outcome) model.DecisionAnswer {
	probability := outcome.ReactionProbability
	if probability == 0 && strings.TrimSpace(outcome.Addressing.ReactionEmoji) != "" {
		probability = 1
	}
	return model.DecisionAnswer{
		Type:          model.DecisionQuestionTypeChoice,
		Choice:        reactionChoice(probability),
		Probabilities: map[string]float64{agentcontract.IntakeReactionOptionNone: 1 - probability, agentcontract.IntakeReactionOptionReact: probability},
		Confidence:    1,
	}
}

func reactionChoice(probability float64) string {
	if probability >= 0.5 {
		return agentcontract.IntakeReactionOptionReact
	}
	return agentcontract.IntakeReactionOptionNone
}

func dutyAnswer(outcome Outcome) model.DecisionAnswer {
	dutyName := agentcontract.IntakeDutyOptionNone
	confidence := float64(0)
	if outcome.Addressing.DutyMatch {
		dutyName = outcome.Addressing.DutyName
		confidence = outcome.Addressing.DutyConfidence
	}
	return model.DecisionAnswer{
		Type:          model.DecisionQuestionTypeChoice,
		Choice:        dutyName,
		Probabilities: map[string]float64{dutyName: 1},
		Confidence:    confidence,
	}
}

func selectedChoiceKey(outcome Outcome) string {
	for _, choiceKey := range outcome.TurnDecision.Choices {
		if containsValue(outcome.PendingChoiceKeys, choiceKey) {
			return choiceKey
		}
	}
	return agentcontract.IntakePendingOptionOther
}

func pendingAnswerName(outcome Outcome) string {
	if len(outcome.TurnDecision.Choices) > 0 || outcome.AnswersPendingChoiceWithoutSelecting {
		return agentcontract.IntakePendingOptionAnswer
	}
	return agentcontract.IntakePendingOptionOther
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

func PendingChoiceKeys(state any) []string {
	document, errorValue := json.Marshal(state)
	if errorValue != nil {
		return nil
	}
	var decisionState struct {
		PendingChoice struct {
			Options []struct {
				Key string `json:"key"`
			} `json:"options"`
		} `json:"pendingChoice"`
	}
	if errorValue := json.Unmarshal(document, &decisionState); errorValue != nil {
		return nil
	}
	choiceKeys := []string{}
	for _, option := range decisionState.PendingChoice.Options {
		choiceKeys = append(choiceKeys, option.Key)
	}
	return choiceKeys
}

func firstScriptedToolName(decision agentcontract.TurnDecision) string {
	if len(decision.InitialToolNames) == 0 {
		return agentcontract.IntakeChoiceOptionNone
	}
	return decision.InitialToolNames[0]
}

func scriptedExpectedToolCount(decision agentcontract.TurnDecision) agentcontract.ExpectedToolCount {
	if decision.ExpectedToolCount != "" {
		return decision.ExpectedToolCount
	}
	if decision.Classification == agentcontract.IntakeClassificationBoundedTask {
		return agentcontract.ExpectedToolCountSeveral
	}
	return agentcontract.ExpectedToolCountNone
}
