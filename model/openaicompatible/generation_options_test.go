package openaicompatible

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func recordedRequestDocument(t *testing.T, answer string, ask func(*Provider)) map[string]any {
	t.Helper()
	recorded := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if errorValue := json.NewDecoder(request.Body).Decode(&recorded); errorValue != nil {
			t.Errorf("expected a decodable request: %v", errorValue)
		}
		responseWriter.Write([]byte(answer))
	}))
	defer server.Close()
	ask(NewProvider(server.URL, "", "example/model"))
	return recorded
}

const oneToolCallAnswer = `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"answer","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`

func TestAStructuredRequestCarriesTheSeedAndTemperatureItWasGiven(t *testing.T) {
	seed := int64(41)
	temperature := 0.2
	recorded := recordedRequestDocument(t, oneToolCallAnswer, func(provider *Provider) {
		provider.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
			Messages:               []model.Message{{Role: "user", Content: "hello"}},
			StructuredOutputSchema: model.StructuredOutputSchema{Name: "answer", Document: `{"type":"object"}`},
			GenerationOptions:      model.GenerationOptions{Seed: &seed, Temperature: &temperature},
		})
	})
	if recorded["seed"] != float64(41) {
		t.Fatalf("a seeded turn must ask the endpoint for that seed, got %v", recorded["seed"])
	}
	if recorded["temperature"] != 0.2 {
		t.Fatalf("a turn given a temperature must ask the endpoint for it, got %v", recorded["temperature"])
	}
}

func TestAChatRequestCarriesTheSeedAndTemperatureItWasGiven(t *testing.T) {
	seed := int64(7)
	temperature := 0.9
	recorded := recordedRequestDocument(t, oneToolCallAnswer, func(provider *Provider) {
		provider.GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{
			Messages:          []model.ChatCompletionMessage{{Role: "user", Content: "hello"}},
			GenerationOptions: model.GenerationOptions{Seed: &seed, Temperature: &temperature},
		})
	})
	if recorded["seed"] != float64(7) || recorded["temperature"] != 0.9 {
		t.Fatalf("a chat turn must carry the seed and temperature it was given, got %+v", recorded)
	}
}

func TestATurnGivenNoGenerationOptionsAsksForNone(t *testing.T) {
	recorded := recordedRequestDocument(t, oneToolCallAnswer, func(provider *Provider) {
		provider.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
			Messages:               []model.Message{{Role: "user", Content: "hello"}},
			StructuredOutputSchema: model.StructuredOutputSchema{Name: "answer", Document: `{"type":"object"}`},
		})
	})
	for _, field := range []string{"seed", "temperature", "max_tokens"} {
		if _, isPresent := recorded[field]; isPresent {
			t.Fatalf("a turn given no generation options must not invent %s: %+v", field, recorded)
		}
	}
}
