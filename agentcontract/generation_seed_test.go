package agentcontract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type requestCapturingProvider struct {
	staticReplyProvider
	structuredRequests *[]model.StructuredResponseRequest
}

func (provider requestCapturingProvider) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	*provider.structuredRequests = append(*provider.structuredRequests, request)
	return provider.staticReplyProvider.GenerateStructuredResponse(ctx, request)
}

func TestObservedStructuredCallRecordsTheSeedItSent(t *testing.T) {
	sentRequests := []model.StructuredResponseRequest{}
	records := []LLMCallRecord{}
	observed := ObserveLanguageModel(requestCapturingProvider{staticReplyProvider{content: `{"reply":"ok"}`}, &sentRequests}, func(record LLMCallRecord) {
		records = append(records, record)
	})

	observed.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		Messages:               []model.Message{{Role: "user", Content: "hello"}},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "reply", Document: "{\n\t\"type\": \"object\"\n}"},
	})

	sentSeed := sentRequests[0].GenerationOptions.Seed
	if sentSeed == nil || records[0].Seed == nil || *records[0].Seed != *sentSeed {
		t.Fatalf("expected the recorded seed to be the one sent, sent %v recorded %v", sentSeed, records[0].Seed)
	}
}

func TestObservedStructuredCallKeepsACallersSeed(t *testing.T) {
	sentRequests := []model.StructuredResponseRequest{}
	observed := ObserveLanguageModel(requestCapturingProvider{staticReplyProvider{content: `{}`}, &sentRequests}, func(LLMCallRecord) {})
	callerSeed := int64(42)

	observed.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{GenerationOptions: model.GenerationOptions{Seed: &callerSeed}})

	if *sentRequests[0].GenerationOptions.Seed != callerSeed {
		t.Fatalf("expected the caller's seed to be sent unchanged, got %d", *sentRequests[0].GenerationOptions.Seed)
	}
}

type wireReportingProvider struct {
	staticReplyProvider
}

func (provider wireReportingProvider) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	model.RecordWireExchange(ctx, model.WireExchange{Endpoint: "https://model.example.com/v1/chat/completions", Request: `{"prompt":"PRIVATE_PROMPT"}`, Response: "data: PRIVATE_REPLY"})
	return provider.staticReplyProvider.GenerateResponse(ctx, prompt)
}

func TestObservedCallCarriesTheWireExchangeOutsideItsBody(t *testing.T) {
	records := []LLMCallRecord{}
	observed := ObserveLanguageModel(wireReportingProvider{staticReplyProvider{content: "ok"}}, func(record LLMCallRecord) {
		records = append(records, record)
	})

	observed.GenerateResponse(context.Background(), "hello")

	body, _ := json.Marshal(records[0])
	if strings.Contains(string(body), "PRIVATE") {
		t.Fatalf("expected the ledger body to hold no exchanged content, got %s", body)
	}
	exchange := records[0].Exchange
	if exchange == nil || !strings.Contains(exchange.Request, "PRIVATE_PROMPT") || exchange.Response != "data: PRIVATE_REPLY" {
		t.Fatalf("expected the exchange the provider put on the wire, got %+v", exchange)
	}
}

func TestObservedCallWithoutAWireKeepsNoExchange(t *testing.T) {
	records := []LLMCallRecord{}
	observed := ObserveLanguageModel(staticReplyProvider{content: "ok"}, func(record LLMCallRecord) {
		records = append(records, record)
	})

	observed.GenerateResponse(context.Background(), "hello")

	if records[0].Exchange != nil {
		t.Fatalf("expected no exchange from a provider that sent nothing, got %+v", records[0].Exchange)
	}
}

func TestContextObserverTakesTheCallOverTheWrappersOwn(t *testing.T) {
	wrapperRecords := []LLMCallRecord{}
	contextRecords := []LLMCallRecord{}
	observed := ObserveLanguageModel(staticReplyProvider{content: "ok"}, func(record LLMCallRecord) {
		wrapperRecords = append(wrapperRecords, record)
	})
	rewrapped := ObserveLanguageModel(observed, func(LLMCallRecord) {
		t.Fatal("expected an observed model not to be wrapped twice")
	})

	rewrapped.GenerateResponse(WithLLMCallObserver(context.Background(), func(record LLMCallRecord) {
		contextRecords = append(contextRecords, record)
	}), "for the task")
	rewrapped.GenerateResponse(context.Background(), "for nobody")

	if len(contextRecords) != 1 || len(wrapperRecords) != 1 {
		t.Fatalf("expected the context's observer to take the task's call and the wrapper's the other, got %d and %d", len(contextRecords), len(wrapperRecords))
	}
}
