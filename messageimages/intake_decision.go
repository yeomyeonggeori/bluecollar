package messageimages

import (
	"encoding/base64"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func ImagePartsOf(parts []agentcontract.AgentPart) []agentcontract.AgentPart {
	imageParts := []agentcontract.AgentPart{}
	for _, part := range parts {
		if part.Type == agentcontract.AgentPartTypeImage && part.Image != nil && strings.TrimSpace(part.Image.DataBase64) != "" {
			imageParts = append(imageParts, part)
		}
	}
	return imageParts
}

func AgentImageToMessagePart(part agentcontract.AgentPart) model.MessagePart {
	if part.Image == nil {
		return model.MessagePart{}
	}
	dataBase64 := strings.TrimSpace(part.Image.DataBase64)
	if dataBase64 == "" {
		return model.MessagePart{}
	}
	if _, errorValue := base64.StdEncoding.DecodeString(dataBase64); errorValue != nil {
		return model.MessagePart{}
	}
	return model.MessagePart{
		Type:       "image",
		MimeType:   strings.TrimSpace(part.Image.MimeType),
		DataBase64: dataBase64,
		Text:       strings.TrimSpace(part.Image.Filename),
	}
}

func ImageMessageParts(parts []agentcontract.AgentPart) []model.MessagePart {
	messageParts := []model.MessagePart{}
	for _, part := range ImagePartsOf(parts) {
		messagePart := AgentImageToMessagePart(part)
		if strings.TrimSpace(messagePart.DataBase64) == "" || strings.TrimSpace(messagePart.MimeType) == "" {
			continue
		}
		messageParts = append(messageParts, messagePart)
	}
	return messageParts
}
