package agentcontract

import "time"

type VisibleContext struct {
	Messages []VisibleContextMessage
	// MessagesOpenOtherExchanges says what the messages are. A message written
	// under a root continues one exchange, and what it carries is that exchange.
	// A message that starts its own carries what the other exchanges in the same
	// place opened with, and those may have nothing to do with it. The reader is
	// told which it is holding rather than left to guess from the messages.
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
	MaterialID string
	// URL is the attachment's one platform-wide name: it stands in the
	// message text (or is minted by the adapter from its file identity), and
	// a read tool handed this exact URL fetches the file through the
	// platform's own record and authorization.
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
