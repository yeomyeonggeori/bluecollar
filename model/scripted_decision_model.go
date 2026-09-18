package model

import (
	"context"
	"errors"
	"sync"
)

type ScriptedDecisionModel struct {
	AnswerFor      func(questionName string, question DecisionQuestion) (DecisionAnswer, bool)
	DefaultAnswers map[string]DecisionAnswer
	ModelName      string
	Error          error

	mutex    sync.Mutex
	requests []DecisionRequest
}

func (scriptedModel *ScriptedDecisionModel) Decide(_ context.Context, request DecisionRequest) (DecisionResponse, error) {
	scriptedModel.recordRequest(request)
	if scriptedModel.Error != nil {
		return DecisionResponse{}, scriptedModel.Error
	}
	answers := map[string]DecisionAnswer{}
	for questionName, question := range request.Questions {
		answer, isAnswered := scriptedModel.answerFor(questionName, question)
		if !isAnswered {
			return DecisionResponse{}, errors.New("scripted decision model has no answer for question " + questionName)
		}
		answers[questionName] = answer
	}
	return DecisionResponse{Answers: answers, ModelName: scriptedModel.modelName()}, nil
}

func (scriptedModel *ScriptedDecisionModel) Requests() []DecisionRequest {
	scriptedModel.mutex.Lock()
	defer scriptedModel.mutex.Unlock()
	return append([]DecisionRequest{}, scriptedModel.requests...)
}

func (scriptedModel *ScriptedDecisionModel) recordRequest(request DecisionRequest) {
	scriptedModel.mutex.Lock()
	defer scriptedModel.mutex.Unlock()
	scriptedModel.requests = append(scriptedModel.requests, request)
}

func (scriptedModel *ScriptedDecisionModel) answerFor(questionName string, question DecisionQuestion) (DecisionAnswer, bool) {
	if scriptedModel.AnswerFor != nil {
		if answer, isAnswered := scriptedModel.AnswerFor(questionName, question); isAnswered {
			return answer, true
		}
	}
	answer, isAnswered := scriptedModel.DefaultAnswers[questionName]
	return answer, isAnswered
}

func (scriptedModel *ScriptedDecisionModel) modelName() string {
	if scriptedModel.ModelName == "" {
		return "scripted-decision-model"
	}
	return scriptedModel.ModelName
}
