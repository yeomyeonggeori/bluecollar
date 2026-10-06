package acpagent

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestSessionUpdateWaitingForConnectionCanBeCancelled(t *testing.T) {
	sender := &deferredSessionUpdateSender{ready: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if errorValue := sender.SessionUpdate(ctx, acp.SessionNotification{}); !errors.Is(errorValue, context.Canceled) {
		t.Fatalf("unconnected sender ignored cancellation: %v", errorValue)
	}
}

func TestSessionUpdateReachesTheConnectionWhenItIsReady(t *testing.T) {
	sender := &deferredSessionUpdateSender{ready: make(chan struct{})}
	host := &hostClient{}
	notification := acp.SessionNotification{Update: acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock("ready")}}}
	completed := make(chan error, 1)
	go func() { completed <- sender.SessionUpdate(t.Context(), notification) }()
	sender.connect(host)
	select {
	case errorValue := <-completed:
		if errorValue != nil || host.agentMessage != "ready" {
			t.Fatalf("ready sender lost the update: %v, %q", errorValue, host.agentMessage)
		}
	case <-t.Context().Done():
		t.Fatal("ready sender did not deliver the update")
	}
}

type countingSessionUpdateSender struct {
	count int
}

func (sender *countingSessionUpdateSender) SessionUpdate(context.Context, acp.SessionNotification) error {
	sender.count++
	return nil
}

func BenchmarkSessionUpdateSender(b *testing.B) {
	b.Run("direct", func(b *testing.B) {
		connection := &countingSessionUpdateSender{}
		benchmarkSessionUpdateSender(b, connection)
	})
	b.Run("ready", func(b *testing.B) {
		sender := &deferredSessionUpdateSender{ready: make(chan struct{})}
		sender.connect(&countingSessionUpdateSender{})
		benchmarkSessionUpdateSender(b, sender)
	})
}

func benchmarkSessionUpdateSender(b *testing.B, sender sessionUpdateSender) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		if errorValue := sender.SessionUpdate(b.Context(), acp.SessionNotification{}); errorValue != nil {
			b.Fatal(errorValue)
		}
	}
}
