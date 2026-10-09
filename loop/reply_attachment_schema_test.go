package loop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const deliveringToolPathMeaning = "the meaning the delivering tool gives its path"

func toolSetDeliveringFiles(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := newTestToolSet([]string{toolcontract.BashToolName})
	errorValue := registerTestTool(toolSet, toolcontract.ToolDefinition{
		Name:        toolcontract.FileDeliverToolName,
		Visibility:  toolcontract.ToolVisibilityInternal,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string","description":"` + deliveringToolPathMeaning + `"},"filename":{"type":"string"}},"required":["path"],"additionalProperties":false}}},"additionalProperties":false}`),
	}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("delivered"), nil
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return toolSet
}

type attachmentDescribingSchema struct {
	OneOf []struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	} `json:"oneOf"`
}

func replyAttachmentPathDescription(t *testing.T, schemaDocument string) string {
	t.Helper()
	var schema attachmentDescribingSchema
	if json.Unmarshal([]byte(schemaDocument), &schema) != nil {
		t.Fatal("the action schema is not JSON")
	}
	for _, variant := range schema.OneOf {
		if attachments, hasAttachments := variant.Properties["attachments"]; hasAttachments {
			return attachments.Items.Properties["path"].Description
		}
	}
	t.Fatal("expected a reply variant carrying attachments")
	return ""
}

func TestAReplysAttachmentPathMeansWhatTheDeliveringToolSaysItMeans(t *testing.T) {
	description := replyAttachmentPathDescription(t, ActionSchemaForToolSet(toolSetDeliveringFiles(t), false, false))

	if description != deliveringToolPathMeaning {
		t.Fatalf("a reply's attachment is delivered by %s, so its path must be described the way that tool describes it, got %q", toolcontract.FileDeliverToolName, description)
	}
}
