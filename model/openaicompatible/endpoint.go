package openaicompatible

import (
	"errors"
	"strings"
)

// Endpoint is everything a caller has to be given to reach one model: where to
// ask, what to call the model there, and the key that endpoint wants. A ladder
// of tiers is a ladder of these, so nothing below this line knows a model name.
type Endpoint struct {
	URL       string `json:"url"`
	ModelName string `json:"model"`
	APIKey    string `json:"-"`
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
	return NewProvider(endpoint.URL, endpoint.APIKey, endpoint.ModelName), nil
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
