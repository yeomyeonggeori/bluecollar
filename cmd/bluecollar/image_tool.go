package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type imageReadInput struct {
	Path string `json:"path"`
}

var imageReadInputSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {"type": "string"}
  },
  "required": ["path"]
}`)

var imageReadOutputSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "path": {"type": "string"},
    "contentType": {"type": "string"},
    "sizeBytes": {"type": "integer"}
  },
  "required": ["path", "contentType", "sizeBytes"]
}`)

var imageContentTypeBySuffix = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

func imageContentTypeForPath(path string) string {
	return imageContentTypeBySuffix[strings.ToLower(filepath.Ext(path))]
}

func registerImageTool(toolSet *toolcontract.ToolSet, runningShell shell) {
	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[imageReadInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/image_read",
			Name:            toolcontract.ImageReadToolName,
			SideEffectClass: toolcontract.ToolSideEffectRead,
			OutputSchema:    imageReadOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: imageReadOutputSchema},
			Description:     "Look at an image file rather than trying to decode its pixels yourself.",
			WhenToUse:       "a screenshot, photo, chart or diagram whose content you need.",
			WhenNotToUse:    "a text file of any kind, including SVG and CSV; file_read returns those exactly.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     imageReadInputSchema,
		},
		Result: toolcontract.IdentityToolResult,
		Handler: func(ctx context.Context, input imageReadInput) (toolcontract.ToolResult, error) {
			contentType := imageContentTypeForPath(input.Path)
			if contentType == "" {
				return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput,
					toolcontract.ImageReadToolName, input.Path+" is not an image this tool can show"), nil
			}
			encoded, errorValue := runningShell.readFileBase64(ctx, input.Path)
			if errorValue != nil {
				return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound,
					toolcontract.ImageReadToolName, errorValue.Error()), nil
			}
			document, errorValue := json.Marshal(imageReadDocument{Path: input.Path, ContentType: contentType, SizeBytes: decodedSizeOf(encoded)})
			if errorValue != nil {
				return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed,
					toolcontract.ImageReadToolName, errorValue.Error()), nil
			}
			result := toolcontract.ToolSuccessData(string(document), json.RawMessage(document))
			result.Attachments = []toolcontract.FileAttachment{{
				DevicePath:    input.Path,
				Filename:      filepath.Base(input.Path),
				ContentType:   contentType,
				ContentBase64: encoded,
			}}
			return result, nil
		},
	})
}

type imageReadDocument struct {
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	SizeBytes   int    `json:"sizeBytes"`
}

func decodedSizeOf(base64Encoded string) int {
	return base64.StdEncoding.DecodedLen(len(base64Encoded))
}
