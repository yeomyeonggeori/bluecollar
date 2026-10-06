package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func connectCatalogServer(t *testing.T, server *mcp.Server) (mcp.Transport, *mcp.ServerSession) {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	session, errorValue := server.Connect(t.Context(), serverTransport, nil)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Cleanup(func() { session.Close() })
	return clientTransport, session
}

func TestCatalogPreservesStructuredResultsOnSuccessAndFailure(t *testing.T) {
	for _, isError := range []bool{false, true} {
		for _, hasStructuredContent := range []bool{false, true} {
			name := "success"
			if isError {
				name = "failure"
			}
			if hasStructuredContent {
				name += "_structured"
			}
			t.Run(name, func(t *testing.T) {
				verifyCatalogResult(t, isError, hasStructuredContent)
			})
		}
	}
}

func verifyCatalogResult(t *testing.T, isError bool, hasStructuredContent bool) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	structuredContent := map[string]any{"code": "account_unavailable", "attempts": float64(2)}
	server.AddTool(&mcp.Tool{Name: "account_read", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result := &mcp.CallToolResult{IsError: isError, Content: []mcp.Content{&mcp.TextContent{Text: "account response"}}}
			if hasStructuredContent {
				result.StructuredContent = structuredContent
			}
			return result, nil
		})
	transport, _ := connectCatalogServer(t, server)
	opened, errorValue := openCatalog(t.Context(), []acp.McpServer{{}}, func(acp.McpServer) (mcp.Transport, error) { return transport, nil })
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer opened.Close()
	if errorValue := opened.sessions[0].Ping(t.Context(), nil); errorValue != nil {
		t.Fatalf("successful initialization closed its session: %v", errorValue)
	}
	result, errorValue := opened.toolSet.Invoke(t.Context(), toolcontract.ToolInvocation{ToolName: "account_read", Input: json.RawMessage(`{}`)})
	if errorValue != nil || result.Failed() != isError || result.ContentText() != "account response" {
		t.Fatalf("result semantics changed: %+v, %v", result, errorValue)
	}
	if isError && (result.Failure.Code != toolcontract.FailureCodes.OperationFailed.String() || result.Failure.Stage != "account_read") {
		t.Fatalf("failure identity changed: %+v", result.Failure)
	}
	if !hasStructuredContent && isError {
		if len(result.Output.Data) != 0 {
			t.Fatalf("text-only failure acquired data: %s", result.Output.Data)
		}
		return
	}
	var actual map[string]any
	if errorValue := json.Unmarshal(result.Output.Data, &actual); errorValue != nil {
		t.Fatalf("structured result was lost: %s, %v", result.Output.Data, errorValue)
	}
	expected := map[string]any{}
	if hasStructuredContent {
		expected = structuredContent
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("structured result changed: %v, expected %v", actual, expected)
	}
}

func TestCatalogInitializationFailureClosesEarlierSessions(t *testing.T) {
	for _, failureStage := range []string{"transport", "connect", "list"} {
		t.Run(failureStage, func(t *testing.T) {
			verifyCatalogFailureCleanup(t, failureStage)
		})
	}
}

func verifyCatalogFailureCleanup(t *testing.T, failureStage string) {
	t.Helper()
	firstServer := mcp.NewServer(&mcp.Implementation{Name: "first", Version: "1"}, nil)
	firstTransport, firstSession := connectCatalogServer(t, firstServer)
	expectedFailure := errors.New("catalog unavailable")
	secondServer := mcp.NewServer(&mcp.Implementation{Name: "second", Version: "1"}, nil)
	secondServer.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if failureStage == "connect" && method == "initialize" || failureStage == "list" && method == "tools/list" {
				return nil, expectedFailure
			}
			return next(ctx, method, request)
		}
	})
	secondTransport, secondSession := connectCatalogServer(t, secondServer)
	resolutionCount := 0
	opened, errorValue := openCatalog(t.Context(), []acp.McpServer{{}, {}}, func(acp.McpServer) (mcp.Transport, error) {
		resolutionCount++
		if resolutionCount == 1 {
			return firstTransport, nil
		}
		if failureStage == "transport" {
			return nil, expectedFailure
		}
		return secondTransport, nil
	})
	if opened != nil || errorValue == nil {
		t.Fatalf("initialization failure was hidden: %v, %v", opened, errorValue)
	}
	if !strings.Contains(errorValue.Error(), expectedFailure.Error()) {
		t.Fatalf("initialization failure changed: %v", errorValue)
	}
	assertCatalogSessionClosed(t, firstSession)
	if failureStage == "list" {
		assertCatalogSessionClosed(t, secondSession)
	}
}

func TestStructuredCatalogFailureReachesTheHostsLedger(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "note_write", InputSchema: map[string]any{"type": "object"}, Meta: mcp.Meta{toolcontract.MetaKeySideEffectClass: "state_change"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				IsError:           true,
				Content:           []mcp.Content{&mcp.TextContent{Text: "account unavailable"}},
				StructuredContent: map[string]any{"code": "account_unavailable", "attempts": float64(2)},
			}, nil
		})
	transport, _ := connectCatalogServer(t, server)
	host, _ := driveOneTurn(t, transport, &scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{}}`,
		`{"action":"fail","message":"account unavailable","reason":"account unavailable","goalSatisfied":false,"failureResolution":"failure_report","usedFailureFacts":{"attempts":[{"toolName":"note_write","inputSummary":"{}","errorCode":"operation_failed","failureStage":"note_write","message":"account unavailable"}],"budgetState":"no_tool_fallback_available"}}`,
	}})
	for _, record := range host.keptLedger() {
		if record.Name != "tool.note_write.result" {
			continue
		}
		var observation struct {
			Output  toolcontract.ToolOutput   `json:"output"`
			Failure *toolcontract.ToolFailure `json:"failure"`
		}
		if errorValue := json.Unmarshal(record.Body, &observation); errorValue != nil {
			t.Fatal(errorValue)
		}
		if observation.Failure == nil || observation.Output.Content != "account unavailable" {
			t.Fatalf("failed call changed in the ledger: %+v", observation)
		}
		if string(observation.Output.Data) != `{"attempts":2,"code":"account_unavailable"}` {
			t.Fatalf("host ledger lost structured failure: %s", observation.Output.Data)
		}
		return
	}
	t.Fatal("host received no tool result event")
}

func assertCatalogSessionClosed(t *testing.T, session *mcp.ServerSession) {
	t.Helper()
	closed := make(chan struct{})
	go func() {
		session.Wait()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("failed catalog initialization left an MCP session open")
	}
}

func TestADescriptorReadsWhatTheToolDeclaresAboutItsApproval(t *testing.T) {
	tool := &mcp.Tool{Name: "event_delete", Meta: mcp.Meta{
		toolcontract.MetaKeySideEffectClass:      "destructive",
		toolcontract.MetaKeyApprovalScope:        "calendar",
		toolcontract.MetaKeyRequiresApproval:     true,
		toolcontract.MetaKeyApprovalScopeSummary: "every change to the team calendar",
		toolcontract.MetaKeyApprovalInputFields:  []any{"eventHint", "reason"},
	}}

	descriptor := descriptorForTool(tool)

	if descriptor.SideEffectClass != "destructive" || descriptor.ApprovalScope != "calendar" || descriptor.ApprovalScopeSummary != "every change to the team calendar" || !descriptor.RequiresApproval {
		t.Fatalf("the approval facts the tool declared were not read: %+v", descriptor)
	}
	if !reflect.DeepEqual(descriptor.ApprovalInputFields, []string{"eventHint", "reason"}) {
		t.Fatalf("the inputs that describe the action were not read: %v", descriptor.ApprovalInputFields)
	}
}

func TestAnImageAToolReturnsReachesTheModelAsAnImageAttachment(t *testing.T) {
	picture := []byte{0x89, 'P', 'N', 'G', 0x01, 0x02}
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "image_read", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: "a chart"},
				&mcp.ImageContent{Data: picture, MIMEType: "image/png"},
			}}, nil
		})
	transport, _ := connectCatalogServer(t, server)
	opened, errorValue := openCatalog(t.Context(), []acp.McpServer{{}}, func(acp.McpServer) (mcp.Transport, error) { return transport, nil })
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer opened.Close()

	result, errorValue := opened.toolSet.Invoke(t.Context(), toolcontract.ToolInvocation{ToolName: "image_read", Input: json.RawMessage(`{}`)})

	if errorValue != nil || len(result.Attachments) != 1 {
		t.Fatalf("the image was dropped: %+v, %v", result, errorValue)
	}
	attachment := result.Attachments[0]
	if attachment.ContentType != "image/png" || attachment.ContentBase64 != base64.StdEncoding.EncodeToString(picture) {
		t.Fatalf("the image changed on the way: %+v", attachment)
	}
	if result.ContentText() != "a chart" {
		t.Fatalf("the text beside the image changed: %q", result.ContentText())
	}
}
