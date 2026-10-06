package acpagent

import (
	"context"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func checkpointSender(sender sessionUpdateSender, sessionID acp.SessionId) agentcontract.AgentCheckpointSender {
	return func(ctx context.Context, checkpoint agentcontract.AgentCheckpoint) error {
		return sender.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: sessionID,
			Update: acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content: acp.TextBlock(checkpoint.Message),
				Meta:    map[string]any{CheckpointMetaKey: map[string]any{"toolName": checkpoint.ToolName}},
			}},
		})
	}
}
