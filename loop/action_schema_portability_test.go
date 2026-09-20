package loop

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func schemaReferencePaths(t *testing.T, path string, value any) []string {
	t.Helper()
	switch typedValue := value.(type) {
	case map[string]any:
		paths := []string{}
		for fieldName, fieldValue := range typedValue {
			if fieldName == "$ref" {
				paths = append(paths, path)
				continue
			}
			paths = append(paths, schemaReferencePaths(t, path+"/"+fieldName, fieldValue)...)
		}
		return paths
	case []any:
		paths := []string{}
		for index, item := range typedValue {
			paths = append(paths, schemaReferencePaths(t, path+"/"+strconv.Itoa(index), item)...)
		}
		return paths
	default:
		return nil
	}
}

func unmarshaledSchema(t *testing.T, document string) any {
	t.Helper()
	var value any
	if errorValue := json.Unmarshal([]byte(document), &value); errorValue != nil {
		t.Fatalf("expected schema json: %v", errorValue)
	}
	return value
}

func TestSchemasSentWithoutTheirDefinitionsCarryNoReference(t *testing.T) {
	for name, document := range map[string]string{
		"finalizer":         finalizerActionSchema(),
		"terminal no tools": terminalNoToolsActionSchema(),
	} {
		if references := schemaReferencePaths(t, name, unmarshaledSchema(t, document)); len(references) > 0 {
			t.Fatalf("%s travels without a $defs block, so it must carry no $ref, found %+v in %s", name, references, document)
		}
	}

	state := nativeAgentActionTestStateWithTools("task_add", toolcontract.ShellToolName)
	state.Observations = []turnObservation{newContentObservation("obs-001", "continue", "task_add", "added")}
	tools, errorValue := nativeAgentActionTools(BuildAgentActionRequest(state).StructuredOutputSchema.Document)
	if errorValue != nil {
		t.Fatalf("expected native action tools: %v", errorValue)
	}
	terminalToolCount := 0
	for _, tool := range tools {
		if isNativeTerminalAction(tool.Function.Name) {
			terminalToolCount++
		}
		if references := schemaReferencePaths(t, tool.Function.Name, unmarshaledSchema(t, string(tool.Function.Parameters))); len(references) > 0 {
			t.Fatalf("native tool %q is sent standalone, so its parameters must carry no $ref, found %+v in %s", tool.Function.Name, references, tool.Function.Parameters)
		}
	}
	if terminalToolCount == 0 {
		t.Fatal("expected the native tool set to contain a terminal action")
	}
}

func TestAWrongTypedActionFieldAsksAgainNamingTheField(t *testing.T) {
	provider := nativeAgentActionLanguageModel{
		chatResponses: []model.ChatCompletionResponse{
			nativeAgentActionChatResponse("reply", `{"final":true,"message":"done","executionStateUpdate":"goal reached"}`),
			nativeAgentActionChatResponse("reply", `{"final":true,"message":"done"}`),
		},
	}
	state := nativeAgentActionTestStateWithTools("task_add", toolcontract.ShellToolName)

	action, errorValue := DecideAgentAction(context.Background(), &provider, state)

	if errorValue != nil {
		t.Fatalf("expected the wrong-typed field to be corrected rather than to end the turn: %v", errorValue)
	}
	if action.Action != "finish" || action.Message != "done" {
		t.Fatalf("expected the corrected final reply, got %+v", action)
	}
	if len(provider.chatRequests) != 2 {
		t.Fatalf("expected exactly one further ask, got %d", len(provider.chatRequests))
	}
	correction := provider.chatRequests[1].Messages[len(provider.chatRequests[1].Messages)-1].Content
	if !strings.Contains(correction, "executionStateUpdate") || !strings.Contains(correction, string(model.StructuredOutputValidationType)) {
		t.Fatalf("expected a typed validation issue naming executionStateUpdate, got %q", correction)
	}
}

func TestAWrongTypedActionFieldNeverEndsTheRun(t *testing.T) {
	_, errorValue := ParseAgentActionResponse(model.StructuredResponse{
		Content: `{"action":"reply","final":true,"message":"done","executionStateUpdate":"goal reached"}`,
	})

	if errorValue == nil {
		t.Fatal("expected a wrong-typed execution state to be reported")
	}
	if !isUnreadableModelActionError(errorValue) {
		t.Fatalf("expected the turn to hand the mistake back to the model rather than fail, got %v", errorValue)
	}
	if !strings.Contains(errorValue.Error(), "executionStateUpdate") {
		t.Fatalf("expected the error to name the field, got %v", errorValue)
	}
}
