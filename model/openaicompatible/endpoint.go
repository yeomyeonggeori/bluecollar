package openaicompatible

import (
	"errors"
	"strings"
)

type Endpoint struct {
	URL             string   `json:"url"`
	ModelName       string   `json:"model"`
	APIKey          string   `json:"-"`
	ProviderOrder   []string `json:"providerOrder,omitempty"`
	ProviderSort    string   `json:"providerSort,omitempty"`
	ReasoningEffort string   `json:"reasoningEffort,omitempty"`
}

func (endpoint Endpoint) IsConfigured() bool {
	return strings.TrimSpace(endpoint.URL) != "" && strings.TrimSpace(endpoint.ModelName) != ""
}

func (endpoint Endpoint) Provider() (*Provider, error) {
	if strings.TrimSpace(endpoint.URL) == "" {
		return nil, errors.New("a model endpoint needs a url")
	}
	if strings.TrimSpace(endpoint.ModelName) == "" {
		return nil, errors.New("a model endpoint needs the name the model answers to there")
	}
	provider := NewProvider(endpoint.URL, endpoint.APIKey, endpoint.ModelName)
	provider.providerOrder = append([]string{}, endpoint.ProviderOrder...)
	provider.providerSort = strings.TrimSpace(endpoint.ProviderSort)
	provider.reasoningEffort = strings.TrimSpace(endpoint.ReasoningEffort)
	return provider, nil
}

func (endpoint Endpoint) EmbeddingProvider() (*EmbeddingProvider, error) {
	if strings.TrimSpace(endpoint.URL) == "" {
		return nil, errors.New("an embedding endpoint needs a url")
	}
	if strings.TrimSpace(endpoint.ModelName) == "" {
		return nil, errors.New("an embedding endpoint needs the name the model answers to there")
	}
	return NewEmbeddingProvider(endpoint.URL, endpoint.APIKey, endpoint.ModelName), nil
}
