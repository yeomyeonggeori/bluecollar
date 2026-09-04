package openaicompatible

import "testing"

func TestEndpointRefusesToBuildAProviderWithoutAURL(t *testing.T) {
	_, errorValue := Endpoint{ModelName: "example/model"}.Provider()
	if errorValue == nil {
		t.Fatal("an endpoint with no url must not build a provider")
	}
}

func TestEndpointRefusesToBuildAProviderWithoutAModelName(t *testing.T) {
	_, errorValue := Endpoint{URL: "http://127.0.0.1:9/v1"}.Provider()
	if errorValue == nil {
		t.Fatal("an endpoint with no model name must not build a provider")
	}
}

func TestEndpointBuildsBothProvidersFromWhatItWasGiven(t *testing.T) {
	endpoint := Endpoint{URL: "http://127.0.0.1:9/v1/", ModelName: "example/model", APIKey: "key"}
	if !endpoint.IsConfigured() {
		t.Fatal("an endpoint with a url and a model name is configured")
	}
	languageModel, errorValue := endpoint.Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if languageModel.modelName != "example/model" || languageModel.endpointURL != "http://127.0.0.1:9/v1" {
		t.Fatalf("the provider must carry the endpoint it was given, got %q at %q", languageModel.modelName, languageModel.endpointURL)
	}
	embeddingProvider, errorValue := endpoint.EmbeddingProvider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if embeddingProvider.ModelName() != "example/model" || embeddingProvider.endpointURL != "http://127.0.0.1:9/v1" {
		t.Fatalf("the embedding provider must carry the endpoint it was given, got %q at %q", embeddingProvider.ModelName(), embeddingProvider.endpointURL)
	}
}

func TestAnEndpointMissingEitherHalfIsNotConfigured(t *testing.T) {
	if (Endpoint{URL: "http://127.0.0.1:9/v1"}).IsConfigured() {
		t.Fatal("an endpoint with no model name is not configured")
	}
	if (Endpoint{ModelName: "example/model"}).IsConfigured() {
		t.Fatal("an endpoint with no url is not configured")
	}
}
