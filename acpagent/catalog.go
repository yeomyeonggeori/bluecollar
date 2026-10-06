package acpagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type catalog struct {
	sessions  []*mcp.ClientSession
	toolSet   *toolcontract.ToolSet
	toolNames []string
	parking   *runParking
}

type transportResolver func(acp.McpServer) (mcp.Transport, error)

func openCatalog(ctx context.Context, mcpServers []acp.McpServer, resolveTransport transportResolver) (openedCatalog *catalog, errorValue error) {
	openedSessions := []*mcp.ClientSession{}
	defer func() {
		if errorValue != nil {
			closeCatalogSessions(openedSessions)
		}
	}()
	toolNames := []string{}
	descriptors := map[string]toolcontract.ToolDescriptor{}
	handlers := map[string]*mcp.ClientSession{}

	for _, mcpServer := range mcpServers {
		transport, errorValue := resolveTransport(mcpServer)
		if errorValue != nil {
			return nil, errorValue
		}
		session, errorValue := mcp.NewClient(&mcp.Implementation{Name: "bluecollar", Version: "acp"}, nil).Connect(ctx, transport, nil)
		if errorValue != nil {
			return nil, errorValue
		}
		openedSessions = append(openedSessions, session)
		toolList, errorValue := session.ListTools(ctx, nil)
		if errorValue != nil {
			return nil, errorValue
		}
		for _, tool := range toolList.Tools {
			descriptor := descriptorForTool(tool)
			if descriptor.Visibility != toolcontract.ToolVisibilityInternal {
				toolNames = append(toolNames, tool.Name)
			}
			descriptors[tool.Name] = descriptor
			handlers[tool.Name] = session
		}
	}

	parking := &runParking{}
	toolSet := toolcontract.NewToolSet(toolNames)
	toolSet.AllowTestReplacement()
	for toolName, descriptor := range descriptors {
		if errorValue := toolSet.RegisterTool(descriptor, callThroughCatalog(handlers[toolName], toolName, parking)); errorValue != nil {
			return nil, errorValue
		}
	}
	return &catalog{sessions: openedSessions, toolSet: toolSet, toolNames: toolNames, parking: parking}, nil
}

func (openedCatalog *catalog) LoadImageContentBase64(ctx context.Context, taskRunID string, devicePath string) (string, error) {
	result, errorValue := openedCatalog.toolSet.Invoke(toolcontract.WithTaskRunID(ctx, taskRunID), toolcontract.ToolInvocation{
		ToolName: toolcontract.ImageReadToolName,
		Input:    toolcontract.MarshalToolInput(map[string]any{"path": devicePath}),
	})
	if errorValue != nil {
		return "", errorValue
	}
	if result.Failed() || len(result.Attachments) == 0 {
		return "", errors.New("the catalog's image_read returned no image for " + devicePath)
	}
	return result.Attachments[0].ContentBase64, nil
}

func (openedCatalog *catalog) Close() {
	closeCatalogSessions(openedCatalog.sessions)
}

func closeCatalogSessions(sessions []*mcp.ClientSession) {
	for _, session := range sessions {
		session.Close()
	}
}

func transportForServer(mcpServer acp.McpServer) (mcp.Transport, error) {
	if stdioServer := mcpServer.Stdio; stdioServer != nil {
		command := exec.Command(stdioServer.Command, stdioServer.Args...)
		for _, environmentVariable := range stdioServer.Env {
			command.Env = append(command.Env, environmentVariable.Name+"="+environmentVariable.Value)
		}
		return &mcp.CommandTransport{Command: command}, nil
	}
	if httpServer := mcpServer.Http; httpServer != nil {
		return &mcp.StreamableClientTransport{
			Endpoint:   httpServer.Url,
			HTTPClient: &http.Client{Transport: headerRoundTripper{headers: httpServer.Headers}},
		}, nil
	}
	return nil, errors.New("bluecollar takes a tool catalog over stdio or http, and this one is neither")
}

type headerRoundTripper struct {
	headers []acp.HttpHeader
}

func (roundTripper headerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	for _, header := range roundTripper.headers {
		request.Header.Set(header.Name, header.Value)
	}
	return http.DefaultTransport.RoundTrip(request)
}

func descriptorForTool(tool *mcp.Tool) toolcontract.ToolDescriptor {
	descriptor := toolcontract.ToolDescriptor{
		ID:          "catalog:" + tool.Name,
		Name:        tool.Name,
		Description: tool.Description,
		Visibility:  toolcontract.ToolVisibilityModel,
		InputSchema: encodedSchema(tool.InputSchema),
		ResultContract: &toolcontract.ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
		},
	}
	toolcontract.ApplyDescriptorMeta(&descriptor, tool.Meta)
	if descriptor.Visibility == "" {
		descriptor.Visibility = toolcontract.ToolVisibilityModel
	}
	if descriptor.SideEffectClass == "" && tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
		descriptor.SideEffectClass = toolcontract.ToolSideEffectRead
	}
	return descriptor
}

func encodedSchema(schema any) json.RawMessage {
	if schema == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	encoded, errorValue := json.Marshal(schema)
	if errorValue != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return encoded
}

func callThroughCatalog(session *mcp.ClientSession, toolName string, parking *runParking) toolcontract.ToolHandler {
	return func(ctx context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		arguments := map[string]any{}
		if len(invocation.Input) > 0 {
			json.Unmarshal(invocation.Input, &arguments)
		}
		callResult, errorValue := session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: arguments})
		if errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.Unavailable, toolName, errorValue.Error()), nil
		}
		if carriedResult, isCarried := toolcontract.ResultOfMeta(callResult.Meta); isCarried {
			carriedResult.Attachments = withImageBytes(carriedResult.Attachments, imageAttachmentsOfResult(callResult))
			if len(carriedResult.Output.Data) == 0 {
				carriedResult.Output.Data = json.RawMessage(`{}`)
			}
			parking.parkOnHeldCall(ctx, carriedResult)
			carriedResult.IsValidatedUpstream = true
			return carriedResult, nil
		}
		summary := textOfResult(callResult)
		if callResult.IsError {
			if callResult.StructuredContent != nil {
				return toolcontract.ToolFailureWithOutput(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, toolName, summary, structuredOfResult(callResult)), nil
			}
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, toolName, summary), nil
		}
		toolResult := toolcontract.ToolSuccessData(summary, structuredOfResult(callResult))
		toolResult.Attachments = imageAttachmentsOfResult(callResult)
		return toolResult, nil
	}
}

func withImageBytes(attachments []toolcontract.FileAttachment, images []toolcontract.FileAttachment) []toolcontract.FileAttachment {
	completed := append([]toolcontract.FileAttachment{}, attachments...)
	imageIndex := 0
	for index := range completed {
		if imageIndex >= len(images) || !strings.HasPrefix(strings.ToLower(completed[index].ContentType), "image/") {
			continue
		}
		completed[index].ContentBase64 = images[imageIndex].ContentBase64
		imageIndex++
	}
	return completed
}

func textOfResult(callResult *mcp.CallToolResult) string {
	segments := []string{}
	for _, content := range callResult.Content {
		if textContent, isText := content.(*mcp.TextContent); isText {
			segments = append(segments, textContent.Text)
		}
	}
	return strings.TrimSpace(strings.Join(segments, "\n"))
}

func imageAttachmentsOfResult(callResult *mcp.CallToolResult) []toolcontract.FileAttachment {
	attachments := []toolcontract.FileAttachment{}
	for _, content := range callResult.Content {
		if imageContent, isImage := content.(*mcp.ImageContent); isImage && len(imageContent.Data) > 0 {
			attachment := toolcontract.FileAttachment{
				ContentType:   imageContent.MIMEType,
				SizeBytes:     int64(len(imageContent.Data)),
				ContentBase64: base64.StdEncoding.EncodeToString(imageContent.Data),
			}
			toolcontract.ApplyAttachmentMeta(&attachment, imageContent.Meta)
			attachments = append(attachments, attachment)
		}
	}
	return attachments
}

func structuredOfResult(callResult *mcp.CallToolResult) json.RawMessage {
	if callResult.StructuredContent == nil {
		return json.RawMessage(`{}`)
	}
	encoded, errorValue := json.Marshal(callResult.StructuredContent)
	if errorValue != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
