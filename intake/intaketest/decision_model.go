// Package intaketest answers intake decision questions from an outcome a test
// states directly, so a test that is about what the runtime does with a
// decision does not have to spell out forty answers to get one.
package intaketest

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type Outcome struct {
	Addressing          agentcontract.AddressingDecision
	ReactionProbability float64
	TurnDecision        agentcontract.TurnDecision
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
	if strings.HasPrefix(shortName, "tool.") {
		return noulAnswer(containsValue(outcome.TurnDecision.InitialToolNames, strings.TrimPrefix(shortName, "tool.")))
	}
	if strings.HasPrefix(shortName, "format.") {
		return noulAnswer(containsValue(outcome.TurnDecision.RequestedOutputFormats, strings.TrimPrefix(shortName, "format.")))
	}
	if strings.HasPrefix(shortName, "choice.") {
		return noulAnswer(containsValue(outcome.TurnDecision.Choices, selectedChoiceKey(shortName, outcome)))
	}
	return namedAnswer(shortName, question, outcome)
}

func namedAnswer(shortName string, question model.DecisionQuestion, outcome Outcome) model.DecisionAnswer {
	switch shortName {
	case "target":
		return choiceAnswer(addressingTargetName(outcome))
	case "shouldRespond":
		return noulAnswer(outcome.Addressing.ShouldRespond)
	case "reaction":
		return reactionAnswer(outcome)
	case "reactionEmoji":
		return choiceAnswer(outcome.Addressing.ReactionEmoji)
	case "duty":
		return dutyAnswer(outcome)
	case "relatesToActiveTask":
		return noulAnswer(outcome.RelatesToActiveTask)
	case "route":
		return choiceAnswer(string(outcome.TurnDecision.Route))
	case "classification":
		return choiceAnswer(string(outcome.TurnDecision.Classification))
	case "taskShape":
		return choiceAnswer(string(outcome.TurnDecision.TaskShape))
	case "level":
		return choiceAnswer(string(outcome.TurnDecision.TaskLevel))
	case "deliverableKind":
		return choiceAnswer(string(outcome.TurnDecision.DeliverableKind))
	case "responseLanguage":
		return choiceAnswer(outcome.TurnDecision.ResponseLanguage)
	case "priorTaskReference":
		return choiceAnswer(string(outcome.TurnDecision.PriorTaskReference))
	case "approval":
		return choiceAnswer(approvalName(outcome))
	case "busyRoute":
		return choiceAnswer(string(outcome.TurnDecision.BusyRoute))
	}
	return model.DecisionAnswer{Type: question.Type}
}

func addressingTargetName(outcome Outcome) string {
	if target := strings.TrimSpace(string(outcome.Addressing.Target)); target != "" {
		return target
	}
	return string(agentcontract.AddressingTargetBot)
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
		Probabilities: map[string]float64{"none": 1 - probability, "react": probability},
		Confidence:    1,
	}
}

func reactionChoice(probability float64) string {
	if probability >= 0.5 {
		return "react"
	}
	return "none"
}

func dutyAnswer(outcome Outcome) model.DecisionAnswer {
	dutyName := "none"
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

func selectedChoiceKey(shortName string, outcome Outcome) string {
	optionNumber := strings.TrimPrefix(shortName, "choice.")
	for index, choiceKey := range outcome.PendingChoiceKeys {
		if optionNumber == strconv.Itoa(index+1) {
			return choiceKey
		}
	}
	return ""
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
