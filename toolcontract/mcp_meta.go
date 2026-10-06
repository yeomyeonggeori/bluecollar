package toolcontract

import "strings"

const (
	MetaKeyRequiresApproval     = "toolcontract/requiresApproval"
	MetaKeySideEffectClass      = "toolcontract/sideEffectClass"
	MetaKeyApprovalScope        = "toolcontract/approvalScope"
	MetaKeyApprovalScopeSummary = "toolcontract/approvalScopeSummary"
	MetaKeyApprovalInputFields  = "toolcontract/approvalInputFields"
	MetaKeyAttachmentDevicePath = "toolcontract/attachmentDevicePath"
)

func DescriptorMeta(descriptor ToolDescriptor) map[string]any {
	return map[string]any{
		MetaKeyRequiresApproval:     descriptor.RequiresApproval,
		MetaKeySideEffectClass:      descriptor.SideEffectClass,
		MetaKeyApprovalScope:        descriptor.ApprovalScope,
		MetaKeyApprovalScopeSummary: descriptor.ApprovalScopeSummary,
		MetaKeyApprovalInputFields:  append([]string{}, descriptor.ApprovalInputFields...),
	}
}

func ApplyDescriptorMeta(descriptor *ToolDescriptor, meta map[string]any) {
	readMetaString(meta, MetaKeySideEffectClass, &descriptor.SideEffectClass)
	readMetaString(meta, MetaKeyApprovalScope, &descriptor.ApprovalScope)
	readMetaString(meta, MetaKeyApprovalScopeSummary, &descriptor.ApprovalScopeSummary)
	if requiresApproval, isPresent := meta[MetaKeyRequiresApproval].(bool); isPresent {
		descriptor.RequiresApproval = requiresApproval
	}
	descriptor.ApprovalInputFields = append(descriptor.ApprovalInputFields, metaStrings(meta, MetaKeyApprovalInputFields)...)
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
