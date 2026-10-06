package acpagent

import (
	"context"
	"encoding/json"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type SteerNotification struct {
	SessionID   acp.SessionId `json:"sessionId"`
	Instruction string        `json:"instruction"`
	Reason      string        `json:"reason,omitempty"`
	MessageID   string        `json:"messageID,omitempty"`
}

func (runningAgent *Agent) HandleExtensionMethod(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method != SteerMethod {
		return nil, acp.NewMethodNotFound(method)
	}
	notification := SteerNotification{}
	if errorValue := json.Unmarshal(params, &notification); errorValue != nil {
		return nil, acp.NewInvalidParams(map[string]any{"error": errorValue.Error()})
	}
	runningAgent.steer(notification)
	return nil, nil
}

func (runningAgent *Agent) steer(notification SteerNotification) {
	openSession, isKnown := runningAgent.session(notification.SessionID)
	if !isKnown {
		return
	}
	taskRunID := openSession.currentTaskRunID()
	if taskRunID == "" {
		return
	}
	body, errorValue := json.Marshal(map[string]string{
		"instruction": notification.Instruction,
		"reason":      notification.Reason,
		"messageID":   notification.MessageID,
	})
	if errorValue != nil {
		return
	}
	openSession.taskRuns.AppendTaskEvent(taskRunID, agentcontract.TaskEventAgentSteerReceived, string(body))
}
