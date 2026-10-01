package main

import (
	"context"
	"os"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/loop"
)

func TestASpillLandsWhereTheAgentsShellCanReadIt(t *testing.T) {
	store := shellSpillStore{runningShell: shell{workingDirectoryPath: t.TempDir()}}

	spillRef, errorValue := store.SaveToolResultSpill(context.Background(), loop.ToolResultSpill{Content: "the whole build log"})

	if errorValue != nil {
		t.Fatalf("a writable temporary directory must take a spill: %v", errorValue)
	}
	savedContent, readError := os.ReadFile(spillRef.Locator)
	if readError != nil || string(savedContent) != "the whole build log" {
		t.Fatalf("the locator has to name a file holding the whole content, got %q (%v)", savedContent, readError)
	}
	t.Cleanup(func() { os.Remove(spillRef.Locator) })
}
