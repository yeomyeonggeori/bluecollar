package acpagent

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func requesterTurnMeta() map[string]any {
	return map[string]any{TurnRequestMetaKey: agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{
			RequesterPersonID: "sample-person-id",
			RequesterName:     "이샘플",
			RequesterEmail:    "sample@example.com",
		},
	}}
}

func TestHostRequesterIdentityReachesTheModelOverACP(t *testing.T) {
	hostCalls := []hostToolCall{}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go publishedCatalog(t, &hostCalls).Run(t.Context(), serverTransport)
	languageModel := &scriptedLanguageModel{}
	driveOneTurnWithMeta(t, clientTransport, languageModel, requesterTurnMeta())
	if len(languageModel.actionPrompts) == 0 {
		t.Fatal("the agent made no model request")
	}
	for _, prompt := range languageModel.actionPrompts {
		if !strings.Contains(prompt, "[personID sample-person-id]") || strings.Contains(prompt, "[personID bluecollar]") {
			t.Fatal("the model did not receive the authenticated host requester ID")
		}
	}
}

func TestHostRequesterOwnsTheAdoptedAndNewLocalRuns(t *testing.T) {
	for _, taskRunID := range []string{"", "host-task-run"} {
		t.Run(taskRunID, func(t *testing.T) {
			runningAgent := newTestAgent(t, &scriptedLanguageModel{})
			createdSession, errorValue := runningAgent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: t.TempDir()})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			openSession, _ := runningAgent.session(createdSession.SessionId)
			metadata := requesterTurnMeta()
			metadata[TaskRunMetaKey] = taskRunID
			taskRun := openSession.openTaskRun(metadata, "read my calendar")
			if taskRun.RequesterPersonID != "sample-person-id" {
				t.Fatalf("local run belongs to %q", taskRun.RequesterPersonID)
			}
		})
	}
}
