package main

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestEveryToolTheRunnerBringsSaysWhenNotToUseIt(t *testing.T) {
	toolSet := newWorkspaceToolSet(shell{workingDirectoryPath: t.TempDir()}, nil)

	for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
		if strings.TrimSpace(toolDefinition.WhenToUse) == "" || strings.TrimSpace(toolDefinition.WhenNotToUse) == "" {
			t.Fatalf("%s states what it does and not when it is the wrong choice, so picking it wrongly can only be corrected after the fact", toolDefinition.Name)
		}
	}
}

type stubToolSelector struct{}

func (stubToolSelector) SelectToolNames(context.Context, agentcontract.ToolSelectionNeed) ([]agentcontract.SelectedTool, error) {
	return nil, nil
}

func TestTheHostOffersFindToolsOnlyWhenItCanAnswerIt(t *testing.T) {
	withoutSelector := newWorkspaceToolSet(shell{workingDirectoryPath: t.TempDir()}, nil)
	if withoutSelector.CanInvoke(toolcontract.FindToolsToolName) {
		t.Fatal("expected a host with no tool selector to leave find_tools out of the kernel it offers")
	}
	withSelector := newWorkspaceToolSet(shell{workingDirectoryPath: t.TempDir()}, stubToolSelector{})
	if !withSelector.CanInvoke(toolcontract.FindToolsToolName) {
		t.Fatal("expected a host with a tool selector to register the find_tools it names")
	}
	for _, toolName := range withSelector.ListToolNames() {
		if _, isRegistered := withSelector.ToolDefinition(toolName); !isRegistered {
			t.Fatalf("the host offers %s without registering it", toolName)
		}
	}
}
