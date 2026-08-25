package agentcontract

import "time"

type VisibleContext struct {
	Messages         []VisibleContextMessage
	CurrentMaterials []VisibleContextMaterial
	Materials        []VisibleContextMaterial
	HasMoreBefore    bool
	HistoryCursor    string
	ResponseLanguage string
}

type VisibleContextMessage struct {
	Speaker            string
	SpeakerCallingName string
	SpeakerHandle      string
	Text               string
	SentAt             time.Time
	// ThreadRootID says which exchange a message belongs to. Context that spans
	// several of them reads as one conversation without it, and a request from
	// one exchange gets answered with the subject of another.
	ThreadRootID string
	Materials    []VisibleContextMaterial
}

type VisibleContextMaterial struct {
	FileHint          string
	MaterialID        string
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
