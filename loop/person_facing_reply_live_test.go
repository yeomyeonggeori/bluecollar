//go:build llmeval

package loop

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type personFacingReplyCase struct {
	name           string
	prompt         string
	tool           toolcontract.ToolDefinition
	result         toolcontract.ToolResult
	machineTokens  []string
	requiredTokens []string
}

func TestLiveAReplyCarriesNoMachineTokens(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Fatal("OPENROUTER_API_KEY is required: building with the llmeval tag is the request to run this evaluation")
	}
	modelName := os.Getenv("BLUECOLLAR_MODEL_NAME")
	if modelName == "" {
		modelName = "z-ai/glm-5.3-flash"
	}
	provider, errorValue := (openaicompatible.Endpoint{URL: "https://openrouter.ai/api/v1", ModelName: modelName, APIKey: apiKey}).Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, replyCase := range personFacingReplyCases() {
		t.Run(replyCase.name, func(t *testing.T) {
			for attempt := 1; attempt <= 5; attempt++ {
				assertReplyCarriesNoMachineTokens(t, provider, replyCase, attempt)
			}
		})
	}
}

func personFacingReplyCases() []personFacingReplyCase {
	recipientNotFound := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.NotFound, "recipient_resolve", `{"errorCode":"recipient_not_found","failureStage":"recipient_resolve","message":"recipient \"이샘플\" was not found among approved internkim people with an active messenger account","retryable":false,"safeRetry":false}`)
	recipientNotFound.Failure.Retryable = false
	messageSend := toolcontract.ToolDefinition{
		Name:        "message_send",
		Description: "Send a direct message. personHint is the one recipient: name or job title without honorific or particle, email, @handle, or the personID a tool result showed.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"personHint":{"type":"string"},"message":{"type":"string"}},"required":["personHint","message"],"additionalProperties":false}`),
	}
	return []personFacingReplyCase{
		{
			name:          "a failed send",
			prompt:        "이샘플한테 '테스트'라고 DM 보내봐",
			tool:          messageSend,
			result:        recipientNotFound,
			machineTokens: []string{"recipient_not_found", "not_found", "recipient_resolve", "errorCode", "personHint", "failureStage", "ID"},
		},
		{
			name:           "a failed send whose error code was asked for",
			prompt:         "이샘플한테 '테스트'라고 DM 보내봐. 안 되면 에러 코드가 뭐였는지도 알려줘",
			tool:           messageSend,
			result:         recipientNotFound,
			machineTokens:  []string{"personHint", "failureStage", "recipient_resolve", "retryable"},
			requiredTokens: []string{"recipient_not_found"},
		},
		{
			name:   "an added task",
			prompt: "내일까지 '샘플 보고서' 업무 추가해줘",
			tool: toolcontract.ToolDefinition{
				Name:        "task_add",
				Description: "Add a work item for the requester. title is the exact requested title; dueDate is YYYY-MM-DD.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"dueDate":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
			},
			result:        testToolSuccess(`{"taskID":"9b1f6c2e-4a7d-4f0e-8c3a-2d5e7f9a1b3c","title":"샘플 보고서","dueDate":"2026-11-14","status":"todo","assigneePersonID":"0c7d2b9e-5f1a-4e3b-9d8c-6a2f4e1b7c5d"}`),
			machineTokens: []string{"9b1f6c2e", "0c7d2b9e", "taskID", "assigneePersonID", "todo", "ID"},
		},
	}
}

func assertReplyCarriesNoMachineTokens(t *testing.T, provider *openaicompatible.Provider, replyCase personFacingReplyCase, attempt int) {
	t.Helper()
	services := newTurnRunnerTestServices(provider, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 2, MaxElapsedSecond: 240})
	tools := newTestCapabilityToolSet([]string{replyCase.tool.Name})
	registerTestTool(tools, replyCase.tool, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return replyCase.result, nil
	})
	request := AgentTurnRequest{
		RequesterIdentity: RequesterIdentity{RequesterPersonID: "person-1"},
		ConversationID:    "conversation-1",
		Prompt:            replyCase.prompt,
		ResponseLanguage:  "ko",
		EnvironmentNow:    time.Date(2026, 11, 13, 10, 0, 0, 0, time.FixedZone("KST", 9*60*60)),
		ToolSet:           tools,
		PinnedToolNames:   tools.ListToolNames(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result, runError := services.runner.RunTurn(ctx, request)
	if runError != nil {
		t.Fatalf("attempt %d: %v", attempt, runError)
	}
	reply := strings.TrimSpace(result.FinishMessage + "\n" + result.UserNotice + "\n" + result.FailureNotice.Message)
	t.Logf("attempt %d: %q", attempt, reply)
	for _, token := range replyCase.machineTokens {
		if strings.Contains(reply, token) {
			t.Errorf("attempt %d: the reply shows the person the machine token %q: %q", attempt, token, reply)
		}
	}
	for _, token := range replyCase.requiredTokens {
		if !strings.Contains(reply, token) {
			t.Errorf("attempt %d: the person asked for %q and the reply does not give it: %q", attempt, token, reply)
		}
	}
}
