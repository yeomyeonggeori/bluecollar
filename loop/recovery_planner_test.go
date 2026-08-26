package loop

import (
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"
)

func TestRecoveryPacketDoesNotHardCodeToolAllowedList(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site_serve",
		Output:        toolcontract.ToolOutput{Content: "site workspace must contain app/dist; build in Blueclaw before publishing"},
		Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureExternalService,
			Code:            toolcontract.FailureCodes.OperationFailed.String(),
			Stage:           "site_serve",
			UserSafeSummary: "site workspace must contain app/dist; build in Blueclaw before publishing",
		},
	}

	packet := buildRecoveryPacket(observation)

	if len(packet.AllowedTools) != 0 {
		t.Fatalf("expected recovery packet not to hard-code tool choices, got %+v", packet.AllowedTools)
	}
	if packet.WhatFailed == "" || packet.WhyLikely == "" || len(packet.MustDoNext) == 0 {
		t.Fatalf("expected factual recovery context, got %+v", packet)
	}
}

func TestRecoveryPacketSchemaFailureRetriesSameToolWithFixedInput(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-002",
		Action:        "continue",
		Tool:          "ask_confirm",
		ToolInputKey:  "ask_confirm\x00{}",
		Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureInvalidInput,
			Code:            toolcontract.FailureCodes.InvalidInput.String(),
			Stage:           "ask_confirm",
			UserSafeSummary: "ask_confirm requires userFacingMessage",
		},
	}

	packet := buildRecoveryPacket(observation)

	if packet.RetryPolicy == retryPolicyDoNotRetry {
		t.Fatalf("expected a missing-field schema failure to be retryable with corrected input, got %q", packet.RetryPolicy)
	}
	joined := strings.Join(packet.MustDoNext, " ")
	if !strings.Contains(joined, "same tool") {
		t.Fatalf("expected guidance to retry the same tool with corrected input, got %+v", packet.MustDoNext)
	}
}

func TestRecoveryPacketKeepsTypedHintTools(t *testing.T) {
	failedObservation := turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "site_serve",
		ToolInputKey:  "site_serve\x00{\"siteID\":\"site-1\"}",
		Failure: &toolcontract.ToolFailure{
			RecoveryHints: []toolcontract.RecoveryHint{{ToolNames: []string{"file_edit"}}},
		},
	}

	packet := buildRecoveryPacket(failedObservation)
	if len(packet.AllowedTools) != 1 || packet.AllowedTools[0] != "file_edit" {
		t.Fatalf("expected typed recovery hint tools to remain available, got %+v", packet.AllowedTools)
	}
}

func TestRecoveryIsToldWhatTheCommandPrinted(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-009",
		Action:        "continue",
		Tool:          "shell",
		Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureUnknown,
			Code:            toolcontract.FailureCodes.OperationFailed.String(),
			Stage:           "shell",
			UserSafeSummary: "the command exited 1",
		},
	}
	observation.Output.Content = "ModuleNotFoundError: No module named 'requests'"

	packet := buildRecoveryPacket(observation)

	if !strings.Contains(packet.WhyLikely, "ModuleNotFoundError") {
		t.Fatalf("the shell already said why and recovery was handed an exit status instead: %q", packet.WhyLikely)
	}
	if !strings.Contains(packet.WhyLikely, "the command exited 1") {
		t.Fatalf("the summary labels the failure and belongs alongside what it printed: %q", packet.WhyLikely)
	}
}

func TestASummaryThatIsAlreadyTheOutputIsNotRepeated(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-010",
		Action:        "continue",
		Tool:          "task_add",
		Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureInvalidInput,
			Code:            toolcontract.FailureCodes.InvalidInput.String(),
			Stage:           "task_add",
			UserSafeSummary: "a title is required",
		},
	}
	observation.Output.Content = "a title is required"

	if why := buildRecoveryPacket(observation).WhyLikely; why != "a title is required" {
		t.Fatalf("a tool whose output is its summary says it once, got %q", why)
	}
}

func TestRecoveryShowsTheInputItAsksToChange(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-011",
		Action:        "continue",
		Tool:          "shell",
		ToolInput:     json.RawMessage(`{"command":"python3 -c \"print(card['card_id'])\""}`),
		Failure: &toolcontract.ToolFailure{
			Kind: toolcontract.FailureUnknown, Code: toolcontract.FailureCodes.OperationFailed.String(),
			Stage: "shell", UserSafeSummary: "the command exited 1",
		},
	}
	observation.Output.Content = "KeyError: 'card_id'"

	packet := buildRecoveryPacket(observation)

	if !strings.Contains(packet.InputThatFailed, "card_id") {
		t.Fatalf("mustDoNext asks for the input to change and the packet did not carry it: %+v", packet)
	}
}
