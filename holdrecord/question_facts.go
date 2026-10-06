package holdrecord

import (
	"context"
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type QuestionFacts struct {
	ResponseLanguage string
	OriginalRequest  string
	ModelDraft       string
	Tool             toolcontract.ToolDefinition
	Input            json.RawMessage
	Target           agentcontract.ApprovalTarget
	Choices          []Choice
}

type QuestionWorder interface {
	WordQuestion(ctx context.Context, facts QuestionFacts) QuestionWording
}

type QuestionWording struct {
	Text    string
	Failure error
}
