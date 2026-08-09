package openaicompatible

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func TestAnImageReachesTheEndpointAsAnImageRatherThanNothing(t *testing.T) {
	messages := []model.ChatCompletionMessage{{
		Role:    "user",
		Content: "what is on this board?",
		Parts:   []model.MessagePart{{Type: "image", MimeType: "image/png", DataBase64: "aGVsbG8="}},
	}}

	encoded, errorValue := json.Marshal(chatCompletionMessages(messages))

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.Contains(string(encoded), "image_url") {
		t.Fatalf("a model that can see was sent no image at all, only text: %s", encoded)
	}
	if !strings.Contains(string(encoded), "data:image/png;base64,aGVsbG8=") {
		t.Fatalf("the image bytes did not survive the encoding: %s", encoded)
	}
}

func TestAMessageWithoutImagesKeepsThePlainTextShape(t *testing.T) {
	messages := []model.ChatCompletionMessage{{Role: "user", Content: "do this: ", Parts: []model.MessagePart{{Text: "list the workspace"}}}}

	chat := chatCompletionMessages(messages)

	if chat[0]["content"] != "do this: list the workspace" {
		t.Fatalf("text-only messages must keep the shape every endpoint accepts, got %v", chat[0]["content"])
	}
}
