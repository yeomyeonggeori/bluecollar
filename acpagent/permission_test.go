package acpagent

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type questioningLanguageModel struct {
	*scriptedLanguageModel
}

func (languageModel questioningLanguageModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "approval_question" {
		return model.StructuredResponse{Content: `{"question":"노트를 남길까요?"}`}, nil
	}
	return languageModel.scriptedLanguageModel.GenerateStructuredResponse(ctx, request)
}

type askedHostClient struct {
	hostClient
	selectedOption acp.PermissionOptionId
	mutex          sync.Mutex
	requests       []acp.RequestPermissionRequest
}

func (client *askedHostClient) RequestPermission(_ context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.requests = append(client.requests, request)
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: client.selectedOption}}}, nil
}

func gatedCatalog(t *testing.T, calls *[]hostToolCall) mcp.Transport {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "host", Version: "test"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "note_write",
		Description: "write a note",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		Meta:        mcp.Meta{toolcontract.MetaKeySideEffectClass: "state_change", toolcontract.MetaKeyRequiresApproval: true},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		*calls = append(*calls, hostToolCall{toolName: "note_write"})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "note written"}}}, nil
	})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go server.Run(t.Context(), serverTransport)
	return clientTransport
}

func driveGatedTurn(t *testing.T, selectedOption acp.PermissionOptionId, calls *[]hostToolCall) *askedHostClient {
	t.Helper()
	languageModel := questioningLanguageModel{&scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"회의록"}}`,
		`{"action":"reply","final":true,"message":"done","goalSatisfied":true}`,
	}}}
	catalogTransport := gatedCatalog(t, calls)
	agentInputReader, agentInputWriter := io.Pipe()
	agentOutputReader, agentOutputWriter := io.Pipe()
	runningAgent := newTestAgent(t, languageModel)
	runningAgent.resolveTransport = func(acp.McpServer) (mcp.Transport, error) { return catalogTransport, nil }
	go func() {
		agentConnection := acp.NewAgentSideConnection(runningAgent, agentOutputWriter, agentInputReader)
		runningAgent.Connect(agentConnection)
		<-agentConnection.Done()
	}()
	host := &askedHostClient{selectedOption: selectedOption}
	connection := acp.NewClientSideConnection(host, agentInputWriter, agentOutputReader)
	connection.Initialize(t.Context(), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	newSession, errorValue := connection.NewSession(t.Context(), acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{{Stdio: &acp.McpServerStdio{Name: "host"}}}})
	if errorValue != nil {
		t.Fatalf("session/new: %v", errorValue)
	}
	if _, errorValue := connection.Prompt(t.Context(), acp.PromptRequest{SessionId: newSession.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("회의록 남겨줘")}}); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}
	return host
}

func TestAGatedCallAsksTheClientAndRunsOnceTheClientAllows(t *testing.T) {
	hostCalls := []hostToolCall{}

	host := driveGatedTurn(t, approveOptionID, &hostCalls)

	if len(host.requests) != 1 {
		t.Fatalf("a gated call is put to the client once, got %d requests", len(host.requests))
	}
	request := host.requests[0]
	if request.ToolCall.Title == nil || !strings.Contains(*request.ToolCall.Title, "노트를 남길까요?") {
		t.Fatalf("the client is asked in the words the model wrote, got %+v", request.ToolCall.Title)
	}
	if len(request.Options) != 2 || request.Options[0].OptionId != approveOptionID || request.Options[1].OptionId != rejectOptionID {
		t.Fatalf("the client is offered allow and reject, got %+v", request.Options)
	}
	if len(hostCalls) != 1 {
		t.Fatalf("an allowed call runs, got %+v", hostCalls)
	}
}

func TestAGatedCallTheClientRejectsDoesNotRun(t *testing.T) {
	hostCalls := []hostToolCall{}

	host := driveGatedTurn(t, rejectOptionID, &hostCalls)

	if len(host.requests) != 1 {
		t.Fatalf("a gated call is put to the client once, got %d requests", len(host.requests))
	}
	if len(hostCalls) != 0 {
		t.Fatalf("a rejected call never runs, got %+v", hostCalls)
	}
}

func TestAnUngatedToolIsNeverPutToTheClient(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := &scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"회의록"}}`,
		`{"action":"reply","final":true,"message":"done","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}

	host, _ := driveOneTurn(t, publishedCatalogTransport(t, &hostCalls), languageModel)

	if len(hostCalls) != 1 || len(host.toolCalls) == 0 {
		t.Fatalf("a tool that carries no approval metadata is the host's to gate, got %+v", hostCalls)
	}
}

func TestAGatedCallTheClientDoesNotAnswerDoesNotRun(t *testing.T) {
	hostCalls := []hostToolCall{}

	host := driveGatedTurn(t, "dismissed", &hostCalls)

	if len(host.requests) != 1 || len(hostCalls) != 0 {
		t.Fatalf("an option the agent never offered answers nothing, so the call is refused, got %d requests and %+v", len(host.requests), hostCalls)
	}
}
