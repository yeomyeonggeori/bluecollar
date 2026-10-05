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

func (answer Answer) isAnswer() bool {
	return answer == Approved || answer == Rejected
}

type approvalRequest struct {
	turn      Turn
	taskRunID string
	tool      toolcontract.ToolDefinition
	input     json.RawMessage
}

func (request approvalRequest) toolName() string {
	return strings.TrimSpace(request.tool.Name)
}

func (request approvalRequest) approvalScope() string {
	return strings.TrimSpace(request.tool.ApprovalScope)
}

type verdict string

const (
	verdictApproved     verdict = "approved"
	verdictRejected     verdict = "rejected"
	verdictUnanswered   verdict = "unanswered"
	verdictUnanswerable verdict = "unanswerable"
)

type ruling struct {
	verdict        verdict
	approvedCallID string
}
