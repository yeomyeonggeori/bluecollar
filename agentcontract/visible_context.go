package agentcontract

import "time"

type VisibleContext struct {
	Messages                   []VisibleContextMessage
	MessagesOpenOtherExchanges bool
	CurrentMaterials           []VisibleContextMaterial
	Materials                  []VisibleContextMaterial
	HasMoreBefore              bool
	HistoryCursor              string
	ResponseLanguage           string
}

type VisibleContextMessage struct {
	Speaker            string
	SpeakerCallingName string
	SpeakerHandle      string
	Text               string
	SentAt             time.Time
	Materials          []VisibleContextMaterial
}

type VisibleContextMaterial struct {
	MaterialID        string
	URL               string
	FileID            string
	Platform          string
	MessageID         string
	Filename          string
	ContentType       string
	SizeBytes         int64
	Path              string
	IsAvailable       bool
	ErrorCode         string
	Message           string
	MarkdownPreview   string
	ConversionStatus  string
	ConversionMessage string
}
