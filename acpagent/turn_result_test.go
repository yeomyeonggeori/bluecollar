package acpagent

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestATurnWhoseContextTheHostCancelledStopsAsCancelledWhateverStatusTheRunHad(t *testing.T) {
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()

	stopReason := stopReasonOfTurn(cancelledContext, agentcontract.TaskStatusRunning)

	if stopReason != acp.StopReasonCancelled {
		t.Fatalf("the host's cancel cancels the prompt's context before the run's status flips, and the turn has to answer %q, got %q", acp.StopReasonCancelled, stopReason)
	}
}
