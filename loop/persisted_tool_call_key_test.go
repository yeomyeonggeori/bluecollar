package loop

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestRestoredFormerKernelToolCallKeyPreventsDuplicateSuccess(t *testing.T) {
	toolInput := json.RawMessage(`{"command":"pwd"}`)
	formerToolInputKey := "shell\x00" + agentcontract.CanonicalToolInput(toolInput)
	formerObservation := turnObservation{
		ObservationID:      "obs-legacy-shell",
		Action:             "continue",
		Tool:               "shell",
		Output:             toolcontract.ToolOutput{Content: "command completed"},
		ToolInputKey:       formerToolInputKey,
		RecoveryAttemptKey: formerToolInputKey,
	}
	events := []agentcontract.TaskEvent{{
		Name: agentcontract.ToolTaskEventName("shell", agentcontract.ToolTaskEventResultSuffix),
		Body: marshalEventBody(formerObservation),
	}}

	restoredObservations := observationsFromTaskEvents(events)
	currentToolInputKey := agentcontract.CanonicalToolCallKey(toolcontract.BashToolName, toolInput)
	previousObservation, wasFound := previousSuccessfulToolInputObservation(restoredObservations, currentToolInputKey)
	if !wasFound {
		t.Fatal("a completed shell call from the ledger must match the equivalent bash call after restart")
	}
	if previousObservation.Tool != toolcontract.BashToolName {
		t.Fatalf("restored observation tool = %q, want %q", previousObservation.Tool, toolcontract.BashToolName)
	}
	if previousObservation.ToolInputKey != currentToolInputKey {
		t.Fatalf("restored tool input key = %q, want %q", previousObservation.ToolInputKey, currentToolInputKey)
	}
	if previousObservation.RecoveryAttemptKey != currentToolInputKey {
		t.Fatalf("restored recovery attempt key = %q, want %q", previousObservation.RecoveryAttemptKey, currentToolInputKey)
	}
}

func TestCanonicalizePersistedToolCallKeyChangesOnlyTheToolName(t *testing.T) {
	inputSuffix := " \t{\"command\": \"pwd\"} \x00tail\n"
	testCases := []struct {
		name        string
		toolCallKey string
		expectedKey string
	}{
		{
			name:        "legacy name",
			toolCallKey: " shell \x00" + inputSuffix,
			expectedKey: toolcontract.BashToolName + "\x00" + inputSuffix,
		},
		{name: "empty key", toolCallKey: "", expectedKey: ""},
		{name: "missing delimiter", toolCallKey: "file_write", expectedKey: "file_write"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actualKey := canonicalizePersistedToolCallKey(testCase.toolCallKey); actualKey != testCase.expectedKey {
				t.Fatalf("canonicalizePersistedToolCallKey(%q) = %q, want %q", testCase.toolCallKey, actualKey, testCase.expectedKey)
			}
		})
	}
}

func TestRestoredFormerKernelAttemptFingerprintPreservesFailureCode(t *testing.T) {
	toolInput := json.RawMessage(`{"command":"pwd"}`)
	formerToolInputKey := "shell\x00" + agentcontract.CanonicalToolInput(toolInput)
	errorCode := toolcontract.FailureCodes.OperationFailed.String()
	formerObservation := turnObservation{
		ObservationID:      "obs-legacy-shell-failure",
		Action:             "continue",
		Tool:               "shell",
		Failure:            &toolcontract.ToolFailure{Code: errorCode},
		ToolInputKey:       formerToolInputKey,
		AttemptFingerprint: attemptFingerprint(formerToolInputKey, errorCode),
	}
	events := []agentcontract.TaskEvent{{
		Name: agentcontract.ToolTaskEventName("shell", agentcontract.ToolTaskEventResultSuffix),
		Body: marshalEventBody(formerObservation),
	}}

	restoredObservations := observationsFromTaskEvents(events)
	currentToolInputKey := agentcontract.CanonicalToolCallKey(toolcontract.BashToolName, toolInput)
	expectedFingerprint := attemptFingerprint(currentToolInputKey, errorCode)
	if len(restoredObservations) != 1 || restoredObservations[0].AttemptFingerprint != expectedFingerprint {
		t.Fatalf("restored attempt fingerprint = %+v, want %q", restoredObservations, expectedFingerprint)
	}
}
