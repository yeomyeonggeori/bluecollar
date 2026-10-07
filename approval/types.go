package approval

import (
	"context"
	"encoding/json"

	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type Turn struct {
	ResponseLanguage string
	Prompt           string
}

type Asker interface {
	Ask(context.Context, holdrecord.Hold) approvalcore.Verdict
}

type approvalRequest struct {
	turn           Turn
	taskRunID      string
	toolDefinition toolcontract.ToolDefinition
	toolInput      json.RawMessage
}

func (request approvalRequest) call() approvalcore.Call {
	return approvalcore.Call{
		TaskRunID:        request.taskRunID,
		ToolName:         request.toolDefinition.Name,
		ToolInput:        request.toolInput,
		ApprovalScope:    request.toolDefinition.ApprovalScope,
		SideEffectClass:  request.toolDefinition.SideEffectClass,
		ResponseLanguage: request.turn.ResponseLanguage,
	}
}

func (request approvalRequest) questionFacts() holdrecord.QuestionFacts {
	return holdrecord.QuestionFacts{
		ResponseLanguage: request.turn.ResponseLanguage,
		OriginalRequest:  request.turn.Prompt,
		Tool:             request.toolDefinition,
		Input:            request.toolInput,
	}
}
