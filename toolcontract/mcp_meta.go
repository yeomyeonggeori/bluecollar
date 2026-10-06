package toolcontract

import (
	"encoding/json"
	"strings"
)

const (
	MetaKeyRequiresApproval     = "toolcontract/requiresApproval"
	MetaKeyHostGated            = "toolcontract/hostGated"
	MetaKeySideEffectClass      = "toolcontract/sideEffectClass"
	MetaKeyApprovalScope        = "toolcontract/approvalScope"
	MetaKeyApprovalScopeSummary = "toolcontract/approvalScopeSummary"
	MetaKeyApprovalInputFields  = "toolcontract/approvalInputFields"
	MetaKeyAttachmentDevicePath = "toolcontract/attachmentDevicePath"
	MetaKeyDescriptor           = "toolcontract/descriptor"
	MetaKeyResult               = "toolcontract/result"
)

func DescriptorMeta(descriptor ToolDescriptor) map[string]any {
	return map[string]any{
		MetaKeyRequiresApproval:     descriptor.RequiresApproval,
		MetaKeyHostGated:            descriptor.IsHostGated,
		MetaKeySideEffectClass:      descriptor.SideEffectClass,
		MetaKeyApprovalScope:        descriptor.ApprovalScope,
		MetaKeyApprovalScopeSummary: descriptor.ApprovalScopeSummary,
		MetaKeyApprovalInputFields:  append([]string{}, descriptor.ApprovalInputFields...),
		MetaKeyDescriptor:           descriptor,
	}
}

func ApplyDescriptorMeta(descriptor *ToolDescriptor, meta map[string]any) {
	if isHostGated, isPresent := meta[MetaKeyHostGated].(bool); isPresent {
		descriptor.IsHostGated = isHostGated
	}
	if published, isPublished := descriptorOfMeta(meta); isPublished {
		published.IsHostGated = descriptor.IsHostGated
		*descriptor = published
		return
	}
	readMetaString(meta, MetaKeySideEffectClass, &descriptor.SideEffectClass)
	readMetaString(meta, MetaKeyApprovalScope, &descriptor.ApprovalScope)
	readMetaString(meta, MetaKeyApprovalScopeSummary, &descriptor.ApprovalScopeSummary)
	if requiresApproval, isPresent := meta[MetaKeyRequiresApproval].(bool); isPresent {
		descriptor.RequiresApproval = requiresApproval
	}
	descriptor.ApprovalInputFields = append(descriptor.ApprovalInputFields, metaStrings(meta, MetaKeyApprovalInputFields)...)
}

func descriptorOfMeta(meta map[string]any) (ToolDescriptor, bool) {
	value, isPresent := meta[MetaKeyDescriptor]
	if !isPresent {
		return ToolDescriptor{}, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ToolDescriptor{}, false
	}
	descriptor := ToolDescriptor{}
	return descriptor, json.Unmarshal(encoded, &descriptor) == nil && descriptor.Name != ""
}

func ResultMeta(result ToolResult) map[string]any {
	return map[string]any{MetaKeyResult: result}
}

func ResultOfMeta(meta map[string]any) (ToolResult, bool) {
	value, isPresent := meta[MetaKeyResult]
	if !isPresent {
		return ToolResult{}, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ToolResult{}, false
	}
	result := ToolResult{}
	return result, json.Unmarshal(encoded, &result) == nil
}

func AttachmentMeta(attachment FileAttachment) map[string]any {
	if strings.TrimSpace(attachment.DevicePath) == "" {
		return nil
	}
	return map[string]any{MetaKeyAttachmentDevicePath: attachment.DevicePath}
}

func ApplyAttachmentMeta(attachment *FileAttachment, meta map[string]any) {
	readMetaString(meta, MetaKeyAttachmentDevicePath, &attachment.DevicePath)
}

func readMetaString(meta map[string]any, key string, target *string) {
	if value, isPresent := meta[key].(string); isPresent && strings.TrimSpace(value) != "" {
		*target = value
	}
}

func metaStrings(meta map[string]any, key string) []string {
	values, isList := meta[key].([]any)
	if !isList {
		return nil
	}
	texts := []string{}
	for _, value := range values {
		if text, isText := value.(string); isText && strings.TrimSpace(text) != "" {
			texts = append(texts, text)
		}
	}
	return texts
}
