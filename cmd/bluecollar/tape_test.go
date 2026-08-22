package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

func TestReplayNeedsNoEndpointAndRecordingWritesOnlyForItsOwner(t *testing.T) {
	tapePath := filepath.Join(t.TempDir(), "turn.tape")
	if errorValue := os.WriteFile(tapePath, []byte(`{"index":0,"kind":"text","text":"done"}`+"\n"), 0o600); errorValue != nil {
		t.Fatalf("writing the tape failed: %v", errorValue)
	}

	player, closeTape, errorValue := turnLanguageModel(runOptions{replayTapePath: tapePath}, nil)
	if errorValue != nil {
		t.Fatalf("replay resolves without an endpoint, since that is the point of it: %v", errorValue)
	}
	closeTape()
	if text, _ := player.GenerateResponse(t.Context(), "anything"); text != "done" {
		t.Fatalf("the replayed call answers from the tape, got %q", text)
	}

	recordPath := filepath.Join(t.TempDir(), "recorded.tape")
	_, closeRecorder, errorValue := turnLanguageModel(runOptions{recordTapePath: recordPath}, openaicompatible.NewProvider("http://127.0.0.1:1/v1", "", "model"))
	if errorValue != nil {
		t.Fatalf("opening the tape for recording failed: %v", errorValue)
	}
	closeRecorder()
	info, errorValue := os.Stat(recordPath)
	if errorValue != nil {
		t.Fatalf("the recorder creates its tape up front: %v", errorValue)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("a tape holds every prompt and answer of the run, so it is the owner's alone: %v", info.Mode().Perm())
	}
}

func TestAMissingTapeStopsTheRunInsteadOfCallingAModel(t *testing.T) {
	_, _, errorValue := turnLanguageModel(runOptions{replayTapePath: filepath.Join(t.TempDir(), "absent.tape")}, nil)
	if errorValue == nil || !strings.Contains(errorValue.Error(), "absent.tape") {
		t.Fatalf("silently falling back to the endpoint would bill a run the operator asked to replay: %v", errorValue)
	}
}
