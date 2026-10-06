package toolcontract

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDescriptorMetaSurvivesTheWireIntoTheSameDescriptor(t *testing.T) {
	published := ToolDescriptor{
		SideEffectClass:      ToolSideEffectDestructive,
		RequiresApproval:     true,
		ApprovalScope:        "calendar",
		ApprovalScopeSummary: "every change to the team calendar",
		ApprovalInputFields:  []string{"eventHint", "reason"},
	}
	encoded, errorValue := json.Marshal(DescriptorMeta(published))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var received map[string]any
	if errorValue := json.Unmarshal(encoded, &received); errorValue != nil {
		t.Fatal(errorValue)
	}

	var read ToolDescriptor
	ApplyDescriptorMeta(&read, received)

	if !reflect.DeepEqual(read, published) {
		t.Fatalf("read back %+v, published %+v", read, published)
	}
}

func TestApplyDescriptorMetaLeavesAbsentKeysAlone(t *testing.T) {
	descriptor := ToolDescriptor{SideEffectClass: ToolSideEffectRead}

	ApplyDescriptorMeta(&descriptor, map[string]any{})

	if descriptor.SideEffectClass != ToolSideEffectRead || descriptor.RequiresApproval {
		t.Fatalf("an empty meta changed the descriptor: %+v", descriptor)
	}
}

func TestAttachmentMetaCarriesTheDevicePathAcrossTheWire(t *testing.T) {
	encoded, errorValue := json.Marshal(AttachmentMeta(FileAttachment{DevicePath: "/workspace/chart.png"}))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var received map[string]any
	if errorValue := json.Unmarshal(encoded, &received); errorValue != nil {
		t.Fatal(errorValue)
	}

	var read FileAttachment
	ApplyAttachmentMeta(&read, received)

	if read.DevicePath != "/workspace/chart.png" {
		t.Fatalf("device path read back as %q", read.DevicePath)
	}
}
