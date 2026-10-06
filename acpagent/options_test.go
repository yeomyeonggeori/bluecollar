package acpagent

import (
	"context"
	"net"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func testOptions(languageModel model.LanguageModelProvider) Options {
	return Options{
		AgentName:      "bluecollar",
		LanguageModels: agentcontract.TaskTierLanguageModels{Low: languageModel},
		DecisionModel:  scriptedDecisionModel(languageModel),
	}
}

func newTestAgent(t *testing.T, languageModel model.LanguageModelProvider) *Agent {
	t.Helper()
	return newAgentFromOptions(t, testOptions(languageModel))
}

func newAgentFromOptions(t *testing.T, options Options) *Agent {
	t.Helper()
	runningAgent, errorValue := New(options)
	if errorValue != nil {
		t.Fatalf("new agent: %v", errorValue)
	}
	return runningAgent
}

type pipedHost struct {
	connection   *acp.ClientSideConnection
	sessionID    acp.SessionId
	capabilities acp.AgentCapabilities
}

func openPipedHost(t *testing.T, options Options, catalogTransport mcp.Transport, client acp.Client) *pipedHost {
	t.Helper()
	runningAgent := newAgentFromOptions(t, options)
	runningAgent.resolveTransport = func(acp.McpServer) (mcp.Transport, error) { return catalogTransport, nil }
	agentEnd, clientEnd := net.Pipe()
	t.Cleanup(func() { agentEnd.Close(); clientEnd.Close() })
	go func() {
		connection := acp.NewAgentSideConnection(runningAgent, agentEnd, agentEnd)
		runningAgent.Connect(connection)
		<-connection.Done()
	}()
	connection := acp.NewClientSideConnection(client, clientEnd, clientEnd)
	initialized, errorValue := connection.Initialize(t.Context(), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	if errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	newSession, errorValue := connection.NewSession(t.Context(), acp.NewSessionRequest{Cwd: workingDirectoryOf(client, t), McpServers: []acp.McpServer{{Stdio: &acp.McpServerStdio{Name: "host"}}}})
	if errorValue != nil {
		t.Fatalf("session/new: %v", errorValue)
	}
	return &pipedHost{connection: connection, sessionID: newSession.SessionId, capabilities: initialized.AgentCapabilities}
}

type workingDirectoryHost interface {
	workingDirectory() string
}

func workingDirectoryOf(client acp.Client, t *testing.T) string {
	if host, hasDirectory := client.(workingDirectoryHost); hasDirectory && host.workingDirectory() != "" {
		return host.workingDirectory()
	}
	return t.TempDir()
}

func (host *pipedHost) prompt(t *testing.T, meta map[string]any, blocks ...acp.ContentBlock) (acp.PromptResponse, error) {
	t.Helper()
	return host.connection.Prompt(t.Context(), acp.PromptRequest{SessionId: host.sessionID, Prompt: blocks, Meta: meta})
}

type recordingHost struct {
	hostClient
	directory string
	updates   []acp.SessionUpdate
}

func (client *recordingHost) workingDirectory() string {
	return client.directory
}

func (client *recordingHost) SessionUpdate(ctx context.Context, notification acp.SessionNotification) error {
	client.hostClient.mutex.Lock()
	client.updates = append(client.updates, notification.Update)
	client.hostClient.mutex.Unlock()
	return client.hostClient.SessionUpdate(ctx, notification)
}

func (client *recordingHost) keptUpdates() []acp.SessionUpdate {
	client.hostClient.mutex.Lock()
	defer client.hostClient.mutex.Unlock()
	return append([]acp.SessionUpdate{}, client.updates...)
}

func noteWriteTurnScript() *scriptedLanguageModel {
	return &scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"회의록"}}`,
		`{"action":"reply","final":true,"message":"노트를 남겼습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}
}

func TestAnAgentWithoutALowTierModelRefusesToBeBuilt(t *testing.T) {
	if _, errorValue := New(Options{}); errorValue == nil {
		t.Fatal("an agent with no language model can only panic on its first turn, so it must be refused up front")
	}
}
