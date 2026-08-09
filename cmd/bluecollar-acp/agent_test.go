package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type scriptedLanguageModel struct {
	contents      []string
	callCount     int
	actionPrompts []string
}

func (languageModel *scriptedLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *scriptedLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name != "bluecollar_agent_turn_action" {
		return model.StructuredResponse{Content: `{"route":"start_task","classification":"bounded_task","taskShape":"maintenance_task","level":"xlow","responseLanguage":"en","reason":"test"}`}, nil
	}
	languageModel.actionPrompts = append(languageModel.actionPrompts, allMessageContent(request))
	if languageModel.callCount >= len(languageModel.contents) {
		return model.StructuredResponse{Content: `{"action":"finish","message":"done","goalSatisfied":true}`}, nil
	}
	content := languageModel.contents[languageModel.callCount]
	languageModel.callCount++
	return model.StructuredResponse{Content: content}, nil
}

type hostToolCall struct {
	toolName string
}

func publishedCatalog(t *testing.T, calls *[]hostToolCall) *mcp.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "host", Version: "test"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "note_write",
		Description: "write a note",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		Meta:        mcp.Meta{"blueclaw/sideEffectClass": "state_change"},
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		*calls = append(*calls, hostToolCall{toolName: "note_write"})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "note written"}}}, nil
	})
	return server
}

type hostClient struct {
	mutex        sync.Mutex
	agentMessage string
	ledger       []agentcontract.LedgerRecord
	toolCalls    []acp.ToolCallId
}

func (client *hostClient) SessionUpdate(_ context.Context, notification acp.SessionNotification) error {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if chunk := notification.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
		client.agentMessage += chunk.Content.Text.Text
	}
	if toolCall := notification.Update.ToolCall; toolCall != nil {
		client.toolCalls = append(client.toolCalls, toolCall.ToolCallId)
	}
	if record, isLedgerEvent := ledgerRecordOfMeta(notification.Update); isLedgerEvent {
		client.ledger = append(client.ledger, record)
	}
	return nil
}

func (client *hostClient) keptLedger() []agentcontract.LedgerRecord {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return append([]agentcontract.LedgerRecord{}, client.ledger...)
}

func (client *hostClient) ledgerEventNames() []string {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	names := []string{}
	for _, record := range client.ledger {
		names = append(names, record.Name)
	}
	return names
}

func ledgerRecordOfMeta(update acp.SessionUpdate) (agentcontract.LedgerRecord, bool) {
	for _, meta := range []map[string]any{
		metaOf(update.ToolCall), metaOf(update.ToolCallUpdate), metaOf(update.AgentThoughtChunk),
	} {
		if meta == nil {
			continue
		}
		encoded, errorValue := json.Marshal(meta[agentcontract.LedgerMetaKey])
		if errorValue != nil {
			continue
		}
		record := agentcontract.LedgerRecord{}
		if json.Unmarshal(encoded, &record) == nil && record.Name != "" {
			return record, true
		}
	}
	return agentcontract.LedgerRecord{}, false
}

func metaOf(update any) map[string]any {
	switch typedUpdate := update.(type) {
	case *acp.SessionUpdateToolCall:
		if typedUpdate != nil {
			return typedUpdate.Meta
		}
	case *acp.SessionToolCallUpdate:
		if typedUpdate != nil {
			return typedUpdate.Meta
		}
	case *acp.SessionUpdateAgentThoughtChunk:
		if typedUpdate != nil {
			return typedUpdate.Meta
		}
	}
	return nil
}

func (client *hostClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{Outcome: "cancelled"}}}, nil
}

func (client *hostClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, io.ErrUnexpectedEOF
}
func (client *hostClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, io.ErrUnexpectedEOF
}
func (client *hostClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, io.ErrUnexpectedEOF
}
func (client *hostClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, io.ErrUnexpectedEOF
}
func (client *hostClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, io.ErrUnexpectedEOF
}
func (client *hostClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, io.ErrUnexpectedEOF
}
func (client *hostClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, io.ErrUnexpectedEOF
}

func driveOneTurn(t *testing.T, catalogTransport mcp.Transport, languageModel *scriptedLanguageModel) (*hostClient, acp.PromptResponse) {
	t.Helper()
	return driveOneTurnWithMeta(t, catalogTransport, languageModel, nil)
}

func driveOneTurnWithMeta(t *testing.T, catalogTransport mcp.Transport, languageModel *scriptedLanguageModel, promptMeta map[string]any) (*hostClient, acp.PromptResponse) {
	t.Helper()
	agentInputReader, agentInputWriter := io.Pipe()
	agentOutputReader, agentOutputWriter := io.Pipe()
	runningAgent := newAgent(languageModel, "bluecollar")
	runningAgent.resolveTransport = func(acp.McpServer) (mcp.Transport, error) { return catalogTransport, nil }
	go func() {
		agentConnection := acp.NewAgentSideConnection(runningAgent, agentOutputWriter, agentInputReader)
		runningAgent.sessionUpdates = agentConnection
		<-agentConnection.Done()
	}()

	host := &hostClient{}
	connection := acp.NewClientSideConnection(host, agentInputWriter, agentOutputReader)
	if _, errorValue := connection.Initialize(t.Context(), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); errorValue != nil {
		t.Fatalf("initialize: %v", errorValue)
	}
	newSession, errorValue := connection.NewSession(t.Context(), acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{{Stdio: &acp.McpServerStdio{Name: "host"}}}})
	if errorValue != nil {
		t.Fatalf("session/new: %v", errorValue)
	}
	promptResponse, errorValue := connection.Prompt(t.Context(), acp.PromptRequest{
		SessionId: newSession.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("회의록 정리해줘")},
		Meta:      promptMeta,
	})
	if errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}
	return host, promptResponse
}

func TestAHostDrivesTheLoopOverACPAndItsToolsComeFromTheCatalog(t *testing.T) {
	hostCalls := []hostToolCall{}
	catalogServer := publishedCatalog(t, &hostCalls)
	catalogClientTransport, catalogServerTransport := mcp.NewInMemoryTransports()
	go catalogServer.Run(t.Context(), catalogServerTransport)

	_, promptResponse := driveOneTurn(t, catalogClientTransport, &scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"회의록"}}`,
		`{"action":"finish","message":"노트를 남겼습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}})

	if promptResponse.StopReason == "" {
		t.Fatal("the host has to learn how the turn ended")
	}
	if len(hostCalls) != 1 || hostCalls[0].toolName != "note_write" {
		t.Fatalf("the loop owns no tools, so the work has to land on the host's catalog, got %+v", hostCalls)
	}
}

func TestTheLoopsVerdictReachesTheHostAsAStopReason(t *testing.T) {
	for status, expectedStopReason := range map[taskstate.TaskStatus]acp.StopReason{
		taskstate.TaskStatusCompleted: acp.StopReasonEndTurn,
		taskstate.TaskStatusCancelled: acp.StopReasonCancelled,
		taskstate.TaskStatusBlocked:   acp.StopReasonRefusal,
	} {
		if stopReason := stopReasonForStatus(status); stopReason != expectedStopReason {
			t.Fatalf("a task the loop left %q reaches the host as %q, expected %q", status, stopReason, expectedStopReason)
		}
	}
}

func TestTheHostSeesTheLoopsLedgerWithoutBeingInsideIt(t *testing.T) {
	hostCalls := []hostToolCall{}
	catalogServer := publishedCatalog(t, &hostCalls)
	catalogClientTransport, catalogServerTransport := mcp.NewInMemoryTransports()
	go catalogServer.Run(t.Context(), catalogServerTransport)

	host, _ := driveOneTurn(t, catalogClientTransport, &scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"회의록"}}`,
		`{"action":"finish","message":"노트를 남겼습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}})

	ledgerNames := host.ledgerEventNames()
	for _, expectedEventName := range []string{"agent.action", "tool.note_write.requested", "tool.note_write.result"} {
		if !containsString(ledgerNames, expectedEventName) {
			t.Fatalf("a host outside the agent process still owns the ledger, so %q has to reach it; got %+v", expectedEventName, ledgerNames)
		}
	}
	if len(host.toolCalls) == 0 {
		t.Fatal("a generic ACP client renders tool calls from the standard update, so one has to be sent alongside the ledger record")
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type cancellingHostClient struct {
	hostClient
	cancelAfterToolCalls int
	cancel               func()
}

func (client *cancellingHostClient) SessionUpdate(ctx context.Context, notification acp.SessionNotification) error {
	client.hostClient.SessionUpdate(ctx, notification)
	if notification.Update.ToolCall != nil && len(client.hostClient.toolCalls) >= client.cancelAfterToolCalls {
		client.cancel()
	}
	return nil
}

func TestACancelledTurnStopsCallingTools(t *testing.T) {
	hostCalls := []hostToolCall{}
	catalogServer := publishedCatalog(t, &hostCalls)
	catalogClientTransport, catalogServerTransport := mcp.NewInMemoryTransports()
	go catalogServer.Run(t.Context(), catalogServerTransport)

	languageModel := &scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"one"}}`,
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"two"}}`,
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"three"}}`,
		`{"action":"finish","message":"done","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}

	agentInputReader, agentInputWriter := io.Pipe()
	agentOutputReader, agentOutputWriter := io.Pipe()
	runningAgent := newAgent(languageModel, "bluecollar")
	runningAgent.resolveTransport = func(acp.McpServer) (mcp.Transport, error) { return catalogClientTransport, nil }
	go func() {
		agentConnection := acp.NewAgentSideConnection(runningAgent, agentOutputWriter, agentInputReader)
		runningAgent.sessionUpdates = agentConnection
		<-agentConnection.Done()
	}()

	host := &cancellingHostClient{cancelAfterToolCalls: 1}
	connection := acp.NewClientSideConnection(host, agentInputWriter, agentOutputReader)
	connection.Initialize(t.Context(), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	newSession, errorValue := connection.NewSession(t.Context(), acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{{Stdio: &acp.McpServerStdio{Name: "host"}}}})
	if errorValue != nil {
		t.Fatalf("session/new: %v", errorValue)
	}
	host.cancel = func() { connection.Cancel(t.Context(), acp.CancelNotification{SessionId: newSession.SessionId}) }

	promptResponse, errorValue := connection.Prompt(t.Context(), acp.PromptRequest{
		SessionId: newSession.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("세 번 적어줘")},
	})
	if errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if promptResponse.StopReason != acp.StopReasonCancelled {
		t.Fatalf("a turn the host cancelled has to come back cancelled, got %q", promptResponse.StopReason)
	}
	if len(hostCalls) >= len(languageModel.contents) {
		t.Fatalf("a cancel that arrives mid-turn has to stop the tools, the script ran all %d of them", len(hostCalls))
	}
}

func publishedCatalogTransport(t *testing.T, calls *[]hostToolCall) mcp.Transport {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go publishedCatalog(t, calls).Run(t.Context(), serverTransport)
	return clientTransport
}

func TestAHostHandsBackTheLedgerItKept(t *testing.T) {
	hostCalls := []hostToolCall{}

	firstHost, _ := driveOneTurn(t, publishedCatalogTransport(t, &hostCalls), &scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"회의록"}}`,
		`{"action":"finish","message":"노트를 남겼습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}})
	keptLedger := firstHost.keptLedger()
	callsBeforeResume := len(hostCalls)

	resumedLanguageModel := &scriptedLanguageModel{contents: []string{
		`{"action":"finish","message":"이미 남겼습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}
	driveOneTurnWithMeta(t, publishedCatalogTransport(t, &hostCalls), resumedLanguageModel, map[string]any{agentcontract.LedgerMetaKey: keptLedger})

	if !containsSubstring(resumedLanguageModel.actionPrompts, "note written") {
		t.Fatalf("a turn resumed on a ledger the host kept has to see what already ran, got prompts %d", len(resumedLanguageModel.actionPrompts))
	}
	if len(hostCalls) != callsBeforeResume {
		t.Fatalf("a turn handed the ledger of what already ran must not run it again, got %d against %d", len(hostCalls), callsBeforeResume)
	}
}

func allMessageContent(request model.StructuredResponseRequest) string {
	segments := []string{}
	for _, message := range request.Messages {
		segments = append(segments, message.Content)
	}
	return strings.Join(segments, "\n")
}

func containsSubstring(values []string, wanted string) bool {
	for _, value := range values {
		if strings.Contains(value, wanted) {
			return true
		}
	}
	return false
}

func TestAHostSaysWhatItCarriedOutAndTheTurnSeesIt(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := &scriptedLanguageModel{contents: []string{
		`{"action":"finish","message":"이미 남겼습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}

	driveOneTurnWithMeta(t, publishedCatalogTransport(t, &hostCalls), languageModel, map[string]any{
		agentcontract.CarriedOutCallMetaKey: []agentcontract.CarriedOutCall{{
			ToolName:  "note_write",
			ToolInput: json.RawMessage(`{"text":"회의록"}`),
			Result:    toolcontract.ToolSuccessData("note written by the host", json.RawMessage(`{}`)),
		}},
	})

	if !containsSubstring(languageModel.actionPrompts, "note written by the host") {
		t.Fatal("a call the host carried out has to reach the model, or it asks for the same thing again")
	}
	if len(hostCalls) != 0 {
		t.Fatalf("the host already ran it, so the agent must not run it again, got %+v", hostCalls)
	}
}
