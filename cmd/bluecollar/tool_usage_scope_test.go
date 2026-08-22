package main

import (
	"strings"
	"testing"
)

func TestEveryToolTheRunnerBringsSaysWhenNotToUseIt(t *testing.T) {
	toolSet := newWorkspaceToolSet(shell{workingDirectoryPath: t.TempDir()})

	for _, toolDefinition := range toolSet.ListRegisteredToolDefinitions() {
		if strings.TrimSpace(toolDefinition.WhenToUse) == "" || strings.TrimSpace(toolDefinition.WhenNotToUse) == "" {
			t.Fatalf("%s states what it does and not when it is the wrong choice, so picking it wrongly can only be corrected after the fact", toolDefinition.Name)
		}
	}
}
