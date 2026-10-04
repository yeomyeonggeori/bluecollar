package toolcontract

import (
	"encoding/json"
	"strings"
)

const (
	DeliveredSourceSuffix       = ".source.json"
	DeliveredSourceMaximumBytes = 1 << 20
	deliveredKnownMaximumBytes  = 8000
)

type DeliveredSource struct {
	IsLayoutOwnedByCode bool            `json:"layoutOwnedByCode"`
	Claims              []SourceClaim   `json:"claims"`
	Known               json.RawMessage `json:"known,omitempty"`
}

type SourceClaim struct {
	Path string `json:"path"`
	At   string `json:"at,omitempty"`
	Text string `json:"text"`
}

type sourceDocument struct {
	Schema      json.RawMessage `json:"schema"`
	Declaration json.RawMessage `json:"declaration"`
	Claims      []SourceClaim   `json:"claims"`
	Known       json.RawMessage `json:"known"`
}

func DeliveredSourcePath(filePath string) string {
	return strings.TrimSpace(filePath) + DeliveredSourceSuffix
}

func DeliveredSourceOf(document []byte) (*DeliveredSource, bool) {
	if len(document) == 0 || len(document) > DeliveredSourceMaximumBytes {
		return nil, false
	}
	var source sourceDocument
	if json.Unmarshal(document, &source) != nil || len(source.Claims) == 0 {
		return nil, false
	}
	known := source.Known
	if len(known) > deliveredKnownMaximumBytes || string(known) == "null" {
		known = nil
	}
	return &DeliveredSource{
		IsLayoutOwnedByCode: isPresent(source.Schema) || isPresent(source.Declaration),
		Claims:              source.Claims,
		Known:               known,
	}, true
}

func isPresent(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed != "" && trimmed != "null"
}
