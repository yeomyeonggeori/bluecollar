package agentcontract

import (
	"encoding/json"
	"strings"
)

type HarnessSession struct {
	HarnessName string `json:"harnessName,omitempty"`
	SessionID   string `json:"sessionID,omitempty"`
	IsResumable bool   `json:"isResumable"`
}

type HeldCall struct {
	ApprovalToken     string          `json:"approvalToken,omitempty"`
	ToolName          string          `json:"toolName"`
	ToolInput         json.RawMessage `json:"toolInput,omitempty"`
	ApprovedToolInput json.RawMessage `json:"approvedToolInput,omitempty"`
	ApprovalScope     string          `json:"approvalScope,omitempty"`
	Confirmation      string          `json:"confirmation"`
	ObservationID     string          `json:"observationID,omitempty"`
	HarnessSession    HarnessSession  `json:"harnessSession"`
}

func (heldCall HeldCall) ApprovedInput() json.RawMessage {
	if len(heldCall.ApprovedToolInput) > 0 {
		return heldCall.ApprovedToolInput
	}
	return heldCall.ToolInput
}

func (heldCall HeldCall) CanonicalCallKey() string {
	return CanonicalToolCallKey(heldCall.ToolName, heldCall.ToolInput)
}

func CanonicalToolCallKey(toolName string, toolInput json.RawMessage) string {
	return strings.TrimSpace(toolName) + "\x00" + CanonicalToolInput(toolInput)
}

func CanonicalToolInput(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return "{}"
	}
	var document any
	if errorValue := json.Unmarshal(toolInput, &document); errorValue != nil {
		return strings.TrimSpace(string(toolInput))
	}
	canonicalDocument, errorValue := json.Marshal(document)
	if errorValue != nil {
		return strings.TrimSpace(string(toolInput))
	}
	return string(canonicalDocument)
}
