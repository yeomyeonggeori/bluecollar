package intaketest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type Outcome struct {
	Addressing          agentcontract.AddressingDecision
	ReactionProbability float64
	TurnDecision        agentcontract.TurnDecision
	ToolProbabilities   map[string]float64
	RelatesToActiveTask bool
	PendingChoiceKeys   []string
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
	answers := map[string]model.DecisionAnswer{}
	for questionName, question := range questions {
		messageKey, shortName := splitQuestionName(questionName)
		answers[questionName] = answerFor(shortName, question, outcomeFor(messageKey))
	}
	return answers
}

func splitQuestionName(questionName string) (string, string) {
	separatorIndex := strings.Index(questionName, ".")
	if separatorIndex < 0 {
		return "", questionName
	}
	return questionName[:separatorIndex], questionName[separatorIndex+1:]
}

func answerFor(shortName string, question model.DecisionQuestion, outcome Outcome) model.DecisionAnswer {
	if strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixTool) {
		return toolAnswer(strings.TrimPrefix(shortName, agentcontract.IntakeQuestionPrefixTool), outcome)
	}
	if strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixFormat) {
		return noulAnswer(containsValue(outcome.TurnDecision.RequestedOutputFormats, strings.TrimPrefix(shortName, agentcontract.IntakeQuestionPrefixFormat)))
	}
	if strings.HasPrefix(shortName, agentcontract.IntakeQuestionPrefixChoice) {
		return noulAnswer(containsValue(outcome.TurnDecision.Choices, strings.TrimPrefix(shortName, agentcontract.IntakeQuestionPrefixChoice)))
	}
	return namedAnswer(shortName, question, outcome)
}

func namedAnswer(shortName string, question model.DecisionQuestion, outcome Outcome) model.DecisionAnswer {
	switch shortName {
	case agentcontract.IntakeQuestionTarget:
		return choiceAnswer(addressingTargetName(outcome))
	case agentcontract.IntakeQuestionShouldRespond:
		return noulAnswer(outcome.Addressing.ShouldRespond)
	case agentcontract.IntakeQuestionReaction:
		return reactionAnswer(outcome)
	case agentcontract.IntakeQuestionReactionEmoji:
		return choiceAnswer(orDefault(outcome.Addressing.ReactionEmoji, agentcontract.DefaultReactionEmojiName))
	case agentcontract.IntakeQuestionDuty:
		return dutyAnswer(outcome)
	case agentcontract.IntakeQuestionRelatesToActiveTask:
		return noulAnswer(outcome.RelatesToActiveTask)
	case agentcontract.IntakeQuestionRoute:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.Route), string(agentcontract.TurnRouteAnswerQuestion)))
	case agentcontract.IntakeQuestionClassification:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.Classification), string(agentcontract.IntakeClassificationQuickReply)))
	case agentcontract.IntakeQuestionTaskShape:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.TaskShape), string(agentcontract.TaskShapeImmediateReply)))
	case agentcontract.IntakeQuestionLevel:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.TaskLevel), string(agentcontract.TaskLevelLow)))
	case agentcontract.IntakeQuestionDeliverableKind:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.DeliverableKind), string(agentcontract.DeliverableKindNone)))
	case agentcontract.IntakeQuestionResponseLanguage:
		return choiceAnswer(orDefault(outcome.TurnDecision.ResponseLanguage, toolcontract.ResponseLanguageSameAsConversation))
	case agentcontract.IntakeQuestionPriorTaskReference:
		return choiceAnswer(orDefault(string(outcome.TurnDecision.PriorTaskReference), string(agentcontract.PriorTaskReferenceNone)))
	case agentcontract.IntakeQuestionApproval:
		return choiceAnswer(approvalName(outcome))
	case agentcontract.IntakeQuestionBusyRoute:
		return choiceAnswer(string(outcome.TurnDecision.BusyRoute))
	case agentcontract.IntakeQuestionChoice:
		return choiceAnswer(selectedChoiceKey(outcome))
	}
	return model.DecisionAnswer{Type: question.Type}
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
		return string(agentcontract.ApprovalSignalUnclear)
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
	return agentcontract.IntakeChoiceOptionNone
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
