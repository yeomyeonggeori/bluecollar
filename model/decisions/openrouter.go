package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const DefaultEndpointURL = "https://openrouter.ai/api/alpha/decisions"

type EnvironmentNames struct {
	Endpoint string
	APIKey   string
	Model    string
}

var DecisionEnvironmentNames = EnvironmentNames{
	Endpoint: "BLUECOLLAR_DECISION_ENDPOINT",
	APIKey:   "BLUECOLLAR_DECISION_API_KEY",
	Model:    "BLUECOLLAR_DECISION_MODEL",
}

type Endpoint struct {
	URL        string
	ModelName  string
	APIKey     string
	HTTPClient *http.Client
}

var ErrDecisionAPIKeyMissing = errors.New("the decision API key is not set")

func EndpointFromEnvironment(names EnvironmentNames) (Endpoint, error) {
	apiKey := strings.TrimSpace(os.Getenv(names.APIKey))
	if apiKey == "" {
		return Endpoint{}, fmt.Errorf("%w: set %s", ErrDecisionAPIKeyMissing, names.APIKey)
	}
	modelName := strings.TrimSpace(os.Getenv(names.Model))
	if modelName == "" {
		return Endpoint{}, fmt.Errorf("%s is not set and there is no default decision model", names.Model)
	}
	endpointURL := strings.TrimSpace(os.Getenv(names.Endpoint))
	if endpointURL == "" {
		endpointURL = DefaultEndpointURL
	}
	return Endpoint{URL: endpointURL, ModelName: modelName, APIKey: apiKey}, nil
}

func (endpoint Endpoint) DecisionModel() model.DecisionModel {
	return decisionModel{endpoint: endpoint}
}

func ConfiguredDecisionModel(warnings io.Writer) model.DecisionModel {
	endpoint, errorValue := EndpointFromEnvironment(DecisionEnvironmentNames)
	if errorValue != nil {
		fmt.Fprintln(warnings, "no decision model:", errorValue)
		return nil
	}
	return endpoint.DecisionModel()
}

type decisionModel struct {
	endpoint Endpoint
}

type decisionRequestDocument struct {
	Model     string                            `json:"model"`
	State     any                               `json:"state"`
	Questions map[string]model.DecisionQuestion `json:"questions"`
}

type decisionResponseDocument struct {
	Answers  map[string]decisionAnswerDocument `json:"answers"`
	Usage    decisionUsageDocument             `json:"usage"`
	Model    string                            `json:"model"`
	Provider string                            `json:"provider"`
}

type decisionAnswerDocument struct {
	Choice        string             `json:"choice"`
	Noul          float64            `json:"noul"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type decisionUsageDocument struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

func (decisionModel decisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	modelName := strings.TrimSpace(request.Model)
	if modelName == "" {
		modelName = decisionModel.endpoint.ModelName
	}
	wireState, errorValue := request.WireState()
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	requestDocument, errorValue := json.Marshal(decisionRequestDocument{
		Model:     modelName,
		State:     wireState,
		Questions: request.Questions,
	})
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	startedAt := time.Now()
	responseDocument, errorValue := decisionModel.post(ctx, requestDocument)
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	return model.DecisionResponse{
		Answers:      answersFromDocument(responseDocument.Answers, request.Questions),
		Usage:        model.Usage{PromptTokens: responseDocument.Usage.InputTokens, CompletionTokens: responseDocument.Usage.OutputTokens, TotalTokens: responseDocument.Usage.InputTokens + responseDocument.Usage.OutputTokens, CostUSD: responseDocument.Usage.Cost},
		ModelName:    responseDocument.Model,
		ProviderName: responseDocument.Provider,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
	}, nil
}

func (decisionModel decisionModel) post(ctx context.Context, requestDocument []byte) (decisionResponseDocument, error) {
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, decisionModel.endpoint.URL, bytes.NewReader(requestDocument))
	if errorValue != nil {
		return decisionResponseDocument{}, errorValue
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+decisionModel.endpoint.APIKey)
	httpResponse, errorValue := decisionModel.httpClient().Do(httpRequest)
	if errorValue != nil {
		return decisionResponseDocument{}, errorValue
	}
	defer httpResponse.Body.Close()
	body, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return decisionResponseDocument{}, errorValue
	}
	if httpResponse.StatusCode != http.StatusOK {
		return decisionResponseDocument{}, errors.New("decisions endpoint answered " + httpResponse.Status + ": " + strings.TrimSpace(string(body)))
	}
	model.RecordWireExchange(ctx, model.WireExchange{Endpoint: httpRequest.URL.String(), Request: string(requestDocument), Response: string(body)})
	var responseDocument decisionResponseDocument
	if errorValue := json.Unmarshal(body, &responseDocument); errorValue != nil {
		return decisionResponseDocument{}, errorValue
	}
	return responseDocument, nil
}

func (decisionModel decisionModel) httpClient() *http.Client {
	if decisionModel.endpoint.HTTPClient != nil {
		return decisionModel.endpoint.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func answersFromDocument(documents map[string]decisionAnswerDocument, questions map[string]model.DecisionQuestion) map[string]model.DecisionAnswer {
	answers := map[string]model.DecisionAnswer{}
	for questionName, document := range documents {
		answers[questionName] = model.DecisionAnswer{
			Type:          questions[questionName].Type,
			Choice:        strings.TrimSpace(document.Choice),
			Noul:          document.Noul,
			Probabilities: document.Probabilities,
			Confidence:    document.Confidence,
		}
	}
	return answers
}
