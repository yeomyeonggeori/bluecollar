package intaketest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type LanguageModelDecisionModel struct {
	LanguageModel model.LanguageModelProvider
	Addressing    agentcontract.AddressingDecision
	ModelName     string

	mutex         sync.Mutex
	routedOutcome Outcome
	hasRoutedTurn bool
}

var ErrLanguageModelUnavailable = errors.New("decision language model unavailable")

const turnDecisionInstruction = "Decide the turn for the newest message in the state that follows. Answer with exactly one JSON object for the schema."

func (decisionModel *LanguageModelDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if decisionModel.LanguageModel == nil {
		return model.DecisionResponse{}, ErrLanguageModelUnavailable
	}
	if outcome, isRouted := decisionModel.routedTurn(); isRouted && asksOnlyAboutTools(request.Questions) {
		return model.DecisionResponse{Answers: Answers(request.Questions, func(string) Outcome { return outcome }), ModelName: decisionModel.ModelName}, nil
	}
	structuredRequest, errorValue := turnDecisionRequest(request.State)
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	response, errorValue := decisionModel.LanguageModel.GenerateStructuredResponse(ctx, structuredRequest)
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	var turnDecision agentcontract.TurnDecision
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(response.Content)), &turnDecision); errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	outcome := Outcome{
		Addressing:        decisionModel.Addressing,
		TurnDecision:      turnDecision,
		PendingChoiceKeys: PendingChoiceKeys(request.State),
	}
	decisionModel.rememberRoutedTurn(outcome)
	return model.DecisionResponse{
		Answers:   Answers(request.Questions, func(string) Outcome { return outcome }),
		ModelName: decisionModel.ModelName,
	}, nil
}

func (decisionModel *LanguageModelDecisionModel) routedTurn() (Outcome, bool) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	return decisionModel.routedOutcome, decisionModel.hasRoutedTurn
}

func (decisionModel *LanguageModelDecisionModel) rememberRoutedTurn(outcome Outcome) {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	decisionModel.routedOutcome = outcome
	decisionModel.hasRoutedTurn = true
}

func asksOnlyAboutTools(questions map[string]model.DecisionQuestion) bool {
	for questionName := range questions {
		if !strings.Contains(questionName, "."+agentcontract.IntakeQuestionPrefixTool) {
			return false
		}
	}
	return len(questions) > 0
}

func turnDecisionRequest(state any) (model.StructuredResponseRequest, error) {
	stateDocument, errorValue := json.Marshal(state)
	if errorValue != nil {
		return model.StructuredResponseRequest{}, errorValue
	}
	schemaDocument, errorValue := turnDecisionSchema()
	if errorValue != nil {
		return model.StructuredResponseRequest{}, errorValue
	}
	return model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: turnDecisionInstruction},
			{Role: "user", Content: string(stateDocument)},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               agentcontract.TurnRouterSchemaName,
			Document:           schemaDocument,
			IsStrictlyEnforced: true,
		},
	}, nil
}

func turnDecisionSchema() (string, error) {
	document, errorValue := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"route":                  namedOptionSchema(agentcontract.TurnRouteNames),
			"classification":         namedOptionSchema(agentcontract.IntakeClassificationNames),
			"taskShape":              namedOptionSchema(agentcontract.TaskShapeNames),
			"level":                  namedOptionSchema(agentcontract.IntakeTaskLevelNames),
			"deliverableKind":        namedOptionSchema(agentcontract.DeliverableKindNames),
			"priorTaskReference":     namedOptionSchema(agentcontract.PriorTaskReferenceNames),
			"responseLanguage":       map[string]any{"type": "string"},
			"requestedOutputFormats": stringListSchema(),
			"initialToolNames":       stringListSchema(),
			"reason":                 map[string]any{"type": "string"},
			"userFacingReply":        map[string]any{"type": "string"},
		},
		"required":             []string{"route", "classification", "taskShape", "level", "responseLanguage"},
		"additionalProperties": false,
	})
	return string(document), errorValue
}

func namedOptionSchema(names []string) map[string]any {
	return map[string]any{"type": "string", "enum": names}
}

func stringListSchema() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}
