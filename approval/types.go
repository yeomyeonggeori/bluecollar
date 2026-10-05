package approval

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type Turn struct {
	ResponseLanguage string
	Prompt           string
}

type Asker interface {
	Ask(context.Context, Hold) Answer
}

type Answer string

const (
	NoAnswer Answer = ""
	Approved Answer = "approved"
	Rejected Answer = "rejected"
)

func (answer Answer) isGiven() bool {
	return answer == Approved || answer == Rejected
}

type approvalRequest struct {
	turn           Turn
	taskRunID      string
	toolDefinition toolcontract.ToolDefinition
	toolInput      json.RawMessage
}

func (request approvalRequest) toolName() string {
	return strings.TrimSpace(request.toolDefinition.Name)
}

func (request approvalRequest) approvalScope() string {
	return strings.TrimSpace(request.toolDefinition.ApprovalScope)
}

type outcomeKind string

const (
	outcomeApproved     outcomeKind = "approved"
	outcomeRejected     outcomeKind = "rejected"
	outcomeUnanswered   outcomeKind = "unanswered"
	outcomeUnanswerable outcomeKind = "unanswerable"
)

type outcome struct {
	kind   outcomeKind
	holdID string
}
