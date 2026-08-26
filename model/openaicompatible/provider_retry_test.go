package openaicompatible

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func retryTestProvider(serverURL string) *Provider {
	provider := NewProvider(serverURL, "", "any/model")
	provider.retryBaseDelay = time.Millisecond
	return provider
}

func TestATransientEndpointFailureIsRetried(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		if requestCount <= 2 {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	_, errorValue := retryTestProvider(server.URL).post(context.Background(), []byte(`{}`))

	if errorValue != nil {
		t.Fatalf("one 502 from a shared pool ends the whole run when the request is never retried: %v", errorValue)
	}
	if requestCount != 3 {
		t.Fatalf("expected 3 attempts, got %d", requestCount)
	}
}

func TestARequestTheEndpointRejectedIsNotRetried(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		writer.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	_, errorValue := retryTestProvider(server.URL).post(context.Background(), []byte(`{}`))

	if errorValue == nil || requestCount != 1 {
		t.Fatalf("a 400 names a defect in the request itself, and resending it %d times cannot fix it: %v", requestCount, errorValue)
	}
}

func TestRetriesGiveUpAfterTheAttemptBudget(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, errorValue := retryTestProvider(server.URL).post(context.Background(), []byte(`{}`))

	if errorValue == nil {
		t.Fatal("an endpoint that never recovers must surface its error instead of retrying forever")
	}
	if requestCount != 1+transientRetryCount {
		t.Fatalf("expected %d attempts, got %d", 1+transientRetryCount, requestCount)
	}
	if !strings.Contains(errorValue.Error(), "503") {
		t.Fatalf("the surfaced error must carry the endpoint's status, got: %v", errorValue)
	}
}

func TestACancelledRunDoesNotKeepRetrying(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	provider := retryTestProvider(server.URL)
	provider.retryBaseDelay = time.Minute
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	_, errorValue := provider.post(ctx, []byte(`{}`))

	if errorValue == nil {
		t.Fatal("a cancelled run must not report success")
	}
	if time.Since(started) > 10*time.Second {
		t.Fatal("cancellation must cut the retry wait short instead of sleeping it out")
	}
}

func TestRetryDelayHonoursTheEndpointsRequestedWaitWithinTheCeiling(t *testing.T) {
	if delay := retryDelay(time.Second, 0, 5*time.Second); delay != 5*time.Second {
		t.Fatalf("an endpoint that names its recovery time knows it better than our backoff, got %v", delay)
	}
	if delay := retryDelay(time.Second, 0, time.Hour); delay != transientRetryDelayCeiling {
		t.Fatalf("a server asking for an hour would silently eat the elapsed budget, got %v", delay)
	}
	if delay := retryDelay(time.Second, 2, 0); delay != 4*time.Second {
		t.Fatalf("expected exponential backoff, got %v", delay)
	}
}

func TestAGenerationTheEndpointAbortedIsRetried(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		if requestCount <= 2 {
			writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"error"}]}`))
			return
		}
		writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"shell","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()

	response, errorValue := retryTestProvider(server.URL).GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.FinishReason != "tool_calls" || requestCount != 3 {
		t.Fatalf("finish_reason error is the endpoint failing mid-generation, not the model misformatting, and the correction loop cannot fix an upstream outage: %s after %d requests", response.FinishReason, requestCount)
	}
}

func TestAnEmptyFinishReasonWithToolCallsIsAToolCall(t *testing.T) {
	responseBody := []byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"shell","arguments":"{}"}}]}}]}`)

	response, errorValue := decodeChatCompletion(responseBody, "any/model")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.FinishReason != "tool_calls" {
		t.Fatalf("an endpoint that omits finish_reason still delivered the calls, and refusing them wastes the whole response: %q", response.FinishReason)
	}
}
