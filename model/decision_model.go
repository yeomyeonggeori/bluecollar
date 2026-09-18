package model

import (
	"context"
	"sort"
)

type DecisionQuestionType string

const (
	DecisionQuestionTypeChoice DecisionQuestionType = "choice"
	DecisionQuestionTypeNoul   DecisionQuestionType = "noul"
)

type DecisionQuestion struct {
	Type         DecisionQuestionType `json:"type"`
	Instructions string               `json:"instructions"`
	Criteria     any                  `json:"criteria,omitempty"`
}

type ChoiceQuestion struct {
	Instructions       string
	OptionDescriptions map[string]string
}

type NoulQuestion struct {
	Instructions     string
	TrueDescription  string
	FalseDescription string
}

func (question ChoiceQuestion) Question() DecisionQuestion {
	return DecisionQuestion{
		Type:         DecisionQuestionTypeChoice,
		Instructions: question.Instructions,
		Criteria:     question.OptionDescriptions,
	}
}

func (question NoulQuestion) Question() DecisionQuestion {
	criteria := map[string]string{}
	if question.TrueDescription != "" {
		criteria["true"] = question.TrueDescription
	}
	if question.FalseDescription != "" {
		criteria["false"] = question.FalseDescription
	}
	if len(criteria) == 0 {
		return DecisionQuestion{Type: DecisionQuestionTypeNoul, Instructions: question.Instructions}
	}
	return DecisionQuestion{Type: DecisionQuestionTypeNoul, Instructions: question.Instructions, Criteria: criteria}
}

func (question ChoiceQuestion) OptionNames() []string {
	names := make([]string, 0, len(question.OptionDescriptions))
	for name := range question.OptionDescriptions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type DecisionRequest struct {
	Model     string                      `json:"model,omitempty"`
	State     any                         `json:"state"`
	Questions map[string]DecisionQuestion `json:"questions"`
	SessionID string                      `json:"sessionID,omitempty"`
}

type DecisionAnswer struct {
	Type          DecisionQuestionType `json:"type"`
	Choice        string               `json:"choice,omitempty"`
	Noul          float64              `json:"noul,omitempty"`
	Probabilities map[string]float64   `json:"probabilities,omitempty"`
	Confidence    float64              `json:"confidence,omitempty"`
}

type DecisionResponse struct {
	Answers          map[string]DecisionAnswer `json:"answers"`
	Usage            Usage                     `json:"usage"`
	ModelName        string                    `json:"modelName,omitempty"`
	ProviderName     string                    `json:"providerName,omitempty"`
	UpstreamProvider string                    `json:"upstreamProvider,omitempty"`
	LatencyMS        int64                     `json:"latencyMs,omitempty"`
}

type DecisionModel interface {
	Decide(context.Context, DecisionRequest) (DecisionResponse, error)
}

func (answer DecisionAnswer) ChoiceProbability(option string) float64 {
	return answer.Probabilities[option]
}

func (answer DecisionAnswer) IsYes() bool {
	return answer.Noul >= 0.5
}
