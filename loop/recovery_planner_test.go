package loop

import (
	"encoding/json"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestRecoveryPacketDoesNotHardCodeToolAllowedList(t *testing.T) {
	observation := turnObservation{
		ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-001", Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureExternalService,
			Code:            toolcontract.FailureCodes.OperationFailed.String(),
			Stage:           "task_update",
			UserSafeSummary: "task must carry a title before it is updated",
		}},
		Action: "continue",
		Tool:   "task_update",
		Output: toolcontract.ToolOutput{Content: "task must carry a title before it is updated"},
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
		ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-002", Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureInvalidInput,
			Code:            toolcontract.FailureCodes.InvalidInput.String(),
			Stage:           "ask_confirm",
			UserSafeSummary: "ask_confirm requires userFacingMessage",
		}},
		Action:       "continue",
		Tool:         "ask_confirm",
		ToolInputKey: "ask_confirm\x00{}",
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
		ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-001", Failure: &toolcontract.ToolFailure{
			RecoveryHints: []toolcontract.RecoveryHint{{ToolNames: []string{"edit"}}},
		}},
		Action:       "continue",
		Tool:         "task_update",
		ToolInputKey: "task_update\x00{\"taskID\":\"task-1\"}",
	}

	packet := buildRecoveryPacket(failedObservation)
	if len(packet.AllowedTools) != 1 || packet.AllowedTools[0] != "edit" {
		t.Fatalf("expected typed recovery hint tools to remain available, got %+v", packet.AllowedTools)
	}
}

func TestRecoveryIsToldWhatTheCommandPrinted(t *testing.T) {
	observation := turnObservation{
		ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-009", Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureUnknown,
			Code:            toolcontract.FailureCodes.OperationFailed.String(),
			Stage:           "bash",
			UserSafeSummary: "the command exited 1",
		}},
		Action: "continue",
		Tool:   "bash",
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
		ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-010", Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureInvalidInput,
			Code:            toolcontract.FailureCodes.InvalidInput.String(),
			Stage:           "task_add",
			UserSafeSummary: "a title is required",
		}},
		Action: "continue",
		Tool:   "task_add",
	}
	observation.Output.Content = "a title is required"

	if why := buildRecoveryPacket(observation).WhyLikely; why != "a title is required" {
		t.Fatalf("a tool whose output is its summary says it once, got %q", why)
	}
}

func TestRecoveryShowsTheInputItAsksToChange(t *testing.T) {
	observation := turnObservation{
		ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-011", ToolInput: json.RawMessage(`{"command":"python3 -c \"print(card['card_id'])\""}`), Failure: &toolcontract.ToolFailure{
			Kind: toolcontract.FailureUnknown, Code: toolcontract.FailureCodes.OperationFailed.String(),
			Stage: "bash", UserSafeSummary: "the command exited 1",
		}},
		Action: "continue",
		Tool:   "bash",
	}
	observation.Output.Content = "KeyError: 'card_id'"

	packet := buildRecoveryPacket(observation)

	if !strings.Contains(packet.InputThatFailed, "card_id") {
		t.Fatalf("mustDoNext asks for the input to change and the packet did not carry it: %+v", packet)
	}
}
