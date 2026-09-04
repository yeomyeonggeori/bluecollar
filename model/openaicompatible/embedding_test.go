package openaicompatible

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddingProviderAsksTheEndpointItWasGiven(t *testing.T) {
	var requestedPath string
	var requestedDocument embeddingRequest
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestedPath = request.URL.Path
		authorization = request.Header.Get("Authorization")
		json.NewDecoder(request.Body).Decode(&requestedDocument)
		json.NewEncoder(responseWriter).Encode(map[string]any{
			"data": []map[string]any{{"embedding": []float64{0.5, -0.25}}},
		})
	}))
	defer server.Close()

	provider := NewEmbeddingProvider(server.URL+"/v1", "a-key", "example/embedding")
	embedding, errorValue := provider.GenerateEmbedding(context.Background(), "안녕하세요")
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	if requestedPath != "/v1/embeddings" {
		t.Fatalf("expected the embeddings route under the base url, got %q", requestedPath)
	}
	if requestedDocument.Model != "example/embedding" || requestedDocument.Input != "안녕하세요" {
		t.Fatalf("expected the model and input to travel as given, got %+v", requestedDocument)
	}
	if authorization != "Bearer a-key" {
		t.Fatalf("expected the key the endpoint was given, got %q", authorization)
	}
	if len(embedding) != 2 || embedding[0] != 0.5 || embedding[1] != -0.25 {
		t.Fatalf("expected the vector the endpoint answered, got %v", embedding)
	}
}

func TestEmbeddingProviderReportsAnEmptyAnswerRatherThanAnEmptyVector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	_, errorValue := NewEmbeddingProvider(server.URL, "", "example/embedding").GenerateEmbedding(context.Background(), "text")
	if errorValue == nil {
		t.Fatal("an endpoint that returns no vector is a failure, not an empty embedding")
	}
}

func TestEmbeddingProviderReportsTheStatusTheEndpointRefusedWith(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, errorValue := NewEmbeddingProvider(server.URL, "", "example/embedding").GenerateEmbedding(context.Background(), "text")
	if errorValue == nil {
		t.Fatal("a refused embedding request is a failure")
	}
}

func TestEmbeddingProviderSendsNoAuthorizationWithoutAKey(t *testing.T) {
	var authorization []string
	var hasAuthorization bool
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		authorization, hasAuthorization = request.Header["Authorization"]
		json.NewEncoder(responseWriter).Encode(map[string]any{
			"data": []map[string]any{{"embedding": []float64{1}}},
		})
	}))
	defer server.Close()

	if _, errorValue := NewEmbeddingProvider(server.URL, "  ", "example/embedding").GenerateEmbedding(context.Background(), "text"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if hasAuthorization {
		t.Fatalf("an endpoint that was given no key is asked without one, got %q", authorization)
	}
}
