package agentcontract

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const llmCallErrorMaximumCharacters = 300
const TurnRouterSchemaName = "bluecollar_turn_router"
const AttachmentDescriptionSchemaName = "bluecollar_attachment_description"
const LLMCallKindDecision = "decision"
const AgentActionSchemaName = "bluecollar_agent_turn_action"

type LLMCallRecord struct {
	Kind                   string                                   `json:"kind"`
	Transport              string                                   `json:"transport,omitempty"`
	SchemaName             string                                   `json:"schemaName,omitempty"`
	Provider               string                                   `json:"provider,omitempty"`
	UpstreamProvider       string                                   `json:"upstreamProvider,omitempty"`
	Model                  string                                   `json:"model,omitempty"`
	ModelTier              string                                   `json:"modelTier,omitempty"`
	SelectedBackend        string                                   `json:"selectedBackend,omitempty"`
	FinishReason           string                                   `json:"finishReason,omitempty"`
	LatencyMS              int64                                    `json:"latencyMs"`
	PromptBytes            int                                      `json:"promptBytes"`
	SchemaBytes            int                                      `json:"schemaBytes,omitempty"`
	ToolCount              int                                      `json:"toolCount,omitempty"`
	ToolBytes              int                                      `json:"toolBytes,omitempty"`
	ContentBytes           int                                      `json:"contentBytes"`
	UsedFallback           bool                                     `json:"usedFallback,omitempty"`
	FallbackReason         string                                   `json:"fallbackReason,omitempty"`
	PromptTokens           int64                                    `json:"promptTokens,omitempty"`
	CompletionTokens       int64                                    `json:"completionTokens,omitempty"`
	TotalTokens            int64                                    `json:"totalTokens,omitempty"`
	CachedPromptTokens     int64                                    `json:"cachedPromptTokens,omitempty"`
	CacheWriteTokens       int64                                    `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens        int64                                    `json:"reasoningTokens,omitempty"`
	CostUSD                float64                                  `json:"costUSD,omitempty"`
	UpstreamInferenceCost  float64                                  `json:"upstreamInferenceCostUSD,omitempty"`
	IsError                bool                                     `json:"isError,omitempty"`
	Error                  string                                   `json:"error,omitempty"`
	DiagnosticCategory     model.StructuredOutputDiagnosticCategory `json:"diagnosticCategory,omitempty"`
	DiagnosticFinishReason model.StructuredOutputFinishReason       `json:"diagnosticFinishReason,omitempty"`
	DiagnosticToolName     string                                   `json:"diagnosticToolName,omitempty"`
	DiagnosticIssues       []model.StructuredOutputValidationIssue  `json:"diagnosticIssues,omitempty"`
	DiagnosticRepairStatus model.StructuredOutputRepairStatus       `json:"diagnosticRepairStatus,omitempty"`
	DecisionAnswers        map[string]model.DecisionAnswer          `json:"decisionAnswers,omitempty"`
	DecisionDraws          map[string]float64                       `json:"decisionDraws,omitempty"`
	DecidedMessageCount    int                                      `json:"decidedMessageCount,omitempty"`
	QuestionCount          int                                      `json:"questionCount,omitempty"`
	AttachmentsDescribed   bool                                     `json:"attachmentsDescribed"`
	AttachmentDescriptions []string                                 `json:"attachmentDescriptions,omitempty"`
}

type LLMCallObserver func(record LLMCallRecord)

type IntakeCallLedger struct {
	Records []LLMCallRecord
}

func (ledger *IntakeCallLedger) Observe(record LLMCallRecord) {
	if !isIntakeCallRecord(record) {
		return
	}
	ledger.Records = append(ledger.Records, record)
}

func isIntakeCallRecord(record LLMCallRecord) bool {
	if record.Kind == LLMCallKindDecision {
		return true
	}
	switch record.SchemaName {
	case TurnRouterSchemaName, AttachmentDescriptionSchemaName:
		return true
	default:
		return false
	}
}

func (ledger *IntakeCallLedger) LanguageModel(provider model.LanguageModelProvider) model.LanguageModelProvider {
	return ObserveLanguageModel(provider, ledger.Observe)
}

type observedLanguageModel struct {
	provider model.LanguageModelProvider
	observe  LLMCallObserver
}

func ObserveLanguageModel(provider model.LanguageModelProvider, observe LLMCallObserver) model.LanguageModelProvider {
	if provider == nil || observe == nil {
		return provider
	}
	if _, isObserved := provider.(interface {
		observedInnerProvider() model.LanguageModelProvider
	}); isObserved {
		return provider
	}
	base := observedLanguageModel{provider: provider, observe: observe}
	_, hasRecovery := provider.(model.RecoveryResponder)
	_, hasLocalRecovery := provider.(model.LocalRecoveryResponder)
	if hasRecovery && hasLocalRecovery {
		return observedRecoveryCapabilities{base, observedRecoveryCapability{base}, observedLocalRecoveryCapability{base}}
	}
	if hasRecovery {
		return struct {
			observedLanguageModel
			observedRecoveryCapability
		}{base, observedRecoveryCapability{base}}
	}
	if hasLocalRecovery {
		return struct {
			observedLanguageModel
			observedLocalRecoveryCapability
		}{base, observedLocalRecoveryCapability{base}}
	}
	return base
}

func (observedModel observedLanguageModel) observedInnerProvider() model.LanguageModelProvider {
	return observedModel.provider
}

func (observedModel observedLanguageModel) TextChatCompleter() (model.ChatCompleter, bool) {
	completer, isAvailable := model.ResolveTextChatCompleter(observedModel.provider)
	if !isAvailable {
		return nil, false
	}
	return observedChatCompleter{observedModel: observedModel, delegate: completer}, true
}

func (observedModel observedLanguageModel) RecoveryChatCompleter() (model.RecoveryChatCompleter, bool) {
	completer, isAvailable := model.ResolveRecoveryChatCompleter(observedModel.provider)
	if !isAvailable {
		return nil, false
	}
	return observedRecoveryChatCapability{observedModel: observedModel, delegate: completer}, true
}

func (observedModel observedLanguageModel) LocalRecoveryChatCompleter() (model.LocalRecoveryChatCompleter, bool) {
	completer, isAvailable := model.ResolveLocalRecoveryChatCompleter(observedModel.provider)
	if !isAvailable {
		return nil, false
	}
	return observedLocalRecoveryChatCapability{observedModel: observedModel, delegate: completer}, true
}

type observedChatCompleter struct {
	observedModel observedLanguageModel
	delegate      model.ChatCompleter
}

func (completer observedChatCompleter) GenerateChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := completer.delegate.GenerateChatCompletion(ctx, request)
	completer.observedModel.observe(chatCallRecord("chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (observedModel observedLanguageModel) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	startedAt := time.Now()
	reply, errorValue := observedModel.provider.GenerateResponse(ctx, prompt)
	observedModel.observe(textCallRecord("text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (observedModel observedLanguageModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	startedAt := time.Now()
	response, errorValue := observedModel.provider.GenerateStructuredResponse(ctx, request)
	observedModel.observe(structuredCallRecord(request, response, startedAt, errorValue))
	return response, errorValue
}

type observedRecoveryCapability struct{ observedModel observedLanguageModel }
type observedLocalRecoveryCapability struct{ observedModel observedLanguageModel }
type observedRecoveryChatCapability struct {
	observedModel observedLanguageModel
	delegate      model.RecoveryChatCompleter
}
type observedLocalRecoveryChatCapability struct {
	observedModel observedLanguageModel
	delegate      model.LocalRecoveryChatCompleter
}

type observedRecoveryCapabilities struct {
	observedLanguageModel
	observedRecoveryCapability
	observedLocalRecoveryCapability
}

func (capability observedRecoveryCapability) GenerateRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return capability.observedModel.recoveryResponse(ctx, prompt)
}

func (capability observedLocalRecoveryCapability) GenerateLocalRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	return capability.observedModel.localRecoveryResponse(ctx, prompt)
}

func (capability observedRecoveryChatCapability) GenerateRecoveryChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := capability.delegate.GenerateRecoveryChatCompletion(ctx, request)
	capability.observedModel.observe(chatCallRecord("recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (capability observedLocalRecoveryChatCapability) GenerateLocalRecoveryChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	startedAt := time.Now()
	response, errorValue := capability.delegate.GenerateLocalRecoveryChatCompletion(ctx, request)
	capability.observedModel.observe(chatCallRecord("local_recovery_chat", request, response, startedAt, errorValue))
	return response, errorValue
}

func (observedModel observedLanguageModel) recoveryResponse(ctx context.Context, prompt string) (string, error) {
	recoveryProvider, isRecoveryProvider := observedModel.provider.(model.RecoveryResponder)
	if !isRecoveryProvider {
		return observedModel.GenerateResponse(ctx, prompt)
	}
	startedAt := time.Now()
	reply, errorValue := recoveryProvider.GenerateRecoveryResponse(ctx, prompt)
	observedModel.observe(textCallRecord("recovery_text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func (observedModel observedLanguageModel) localRecoveryResponse(ctx context.Context, prompt string) (string, error) {
	localRecoveryProvider, isLocalRecoveryProvider := observedModel.provider.(model.LocalRecoveryResponder)
	if !isLocalRecoveryProvider {
		return observedModel.GenerateResponse(ctx, prompt)
	}
	startedAt := time.Now()
	reply, errorValue := localRecoveryProvider.GenerateLocalRecoveryResponse(ctx, prompt)
	observedModel.observe(textCallRecord("local_recovery_text", prompt, reply, startedAt, errorValue))
	return reply, errorValue
}

func textCallRecord(kind string, prompt string, reply string, startedAt time.Time, errorValue error) LLMCallRecord {
	record := LLMCallRecord{
		Kind:         kind,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
		PromptBytes:  len(prompt),
		ContentBytes: len(reply),
	}
	if errorValue != nil {
		applyLLMCallError(&record, errorValue)
	}
	return record
}

func applyLLMCallError(record *LLMCallRecord, errorValue error) {
	record.IsError = true
	diagnostic, hasDiagnostic := model.StructuredOutputDiagnosticFromError(errorValue)
	if !hasDiagnostic {
		record.Error = truncateText(compactWhitespace(errorValue.Error()), llmCallErrorMaximumCharacters)
		return
	}
	record.DiagnosticCategory = diagnostic.Category
	record.DiagnosticFinishReason = diagnostic.FinishReason
	record.DiagnosticToolName = diagnostic.ToolName
	record.DiagnosticIssues = append([]model.StructuredOutputValidationIssue{}, diagnostic.ValidationIssues...)
	record.DiagnosticRepairStatus = diagnostic.RepairStatus
}

func chatCallRecord(kind string, request model.ChatCompletionRequest, response model.ChatCompletionResponse, startedAt time.Time, errorValue error) LLMCallRecord {
	record := llmCallRecord(kind, chatResponseProvenance(response), startedAt, errorValue)
	record.SchemaName = chatRequestSchemaName(request)
	record.PromptBytes = chatRequestByteCount(request)
	record.ToolCount = len(request.Tools)
	record.ToolBytes = chatRequestToolByteCount(request)
	record.ContentBytes = len(response.Message.Content)
	return record
}

func structuredCallRecord(request model.StructuredResponseRequest, response model.StructuredResponse, startedAt time.Time, errorValue error) LLMCallRecord {
	record := llmCallRecord("structured", structuredResponseProvenance(response), startedAt, errorValue)
	record.SchemaName = strings.TrimSpace(request.StructuredOutputSchema.Name)
	record.PromptBytes = structuredRequestByteCount(request)
	record.SchemaBytes = len(request.StructuredOutputSchema.Document)
	record.ContentBytes = len(response.Content)
	return record
}

type llmCallProvenance struct {
	Transport        string
	ProviderName     string
	UpstreamProvider string
	ModelName        string
	ModelTier        string
	SelectedBackend  string
	FinishReason     string
	UsedFallback     bool
	FallbackReason   string
	Usage            model.Usage
}

func chatResponseProvenance(response model.ChatCompletionResponse) llmCallProvenance {
	return llmCallProvenance{
		Transport:        response.Transport,
		ProviderName:     response.ProviderName,
		UpstreamProvider: response.UpstreamProvider,
		ModelName:        response.ModelName,
		ModelTier:        response.ModelTier,
		SelectedBackend:  response.SelectedBackend,
		FinishReason:     response.FinishReason,
		UsedFallback:     response.UsedFallback,
		FallbackReason:   response.FallbackReason,
		Usage:            response.Usage,
	}
}

func structuredResponseProvenance(response model.StructuredResponse) llmCallProvenance {
	return llmCallProvenance{
		Transport:        response.Transport,
		ProviderName:     response.ProviderName,
		UpstreamProvider: response.UpstreamProvider,
		ModelName:        response.ModelName,
		ModelTier:        response.ModelTier,
		SelectedBackend:  response.SelectedBackend,
		FinishReason:     response.FinishReason,
		UsedFallback:     response.UsedFallback,
		FallbackReason:   response.FallbackReason,
		Usage:            response.Usage,
	}
}

func llmCallRecord(kind string, provenance llmCallProvenance, startedAt time.Time, errorValue error) LLMCallRecord {
	record := LLMCallRecord{
		Kind:                  kind,
		Transport:             provenance.Transport,
		Provider:              provenance.ProviderName,
		UpstreamProvider:      provenance.UpstreamProvider,
		Model:                 provenance.ModelName,
		ModelTier:             provenance.ModelTier,
		SelectedBackend:       provenance.SelectedBackend,
		FinishReason:          provenance.FinishReason,
		LatencyMS:             time.Since(startedAt).Milliseconds(),
		UsedFallback:          provenance.UsedFallback,
		FallbackReason:        truncateText(compactWhitespace(provenance.FallbackReason), llmCallErrorMaximumCharacters),
		PromptTokens:          provenance.Usage.PromptTokens,
		CompletionTokens:      provenance.Usage.CompletionTokens,
		TotalTokens:           provenance.Usage.TotalTokens,
		CachedPromptTokens:    provenance.Usage.CachedPromptTokens,
		CacheWriteTokens:      provenance.Usage.CacheWriteTokens,
		ReasoningTokens:       provenance.Usage.ReasoningTokens,
		CostUSD:               provenance.Usage.CostUSD,
		UpstreamInferenceCost: provenance.Usage.UpstreamInferenceCost,
	}
	if errorValue != nil {
		applyLLMCallError(&record, errorValue)
	}
	return record
}

func chatRequestSchemaName(request model.ChatCompletionRequest) string {
	return strings.TrimSpace(request.SchemaName)
}

func chatRequestByteCount(request model.ChatCompletionRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += messageByteCount(message.Content, message.Parts)
	}
	return byteCount
}

func chatRequestToolByteCount(request model.ChatCompletionRequest) int {
	byteCount := 0
	for _, tool := range request.Tools {
		document, errorValue := json.Marshal(tool)
		if errorValue != nil {
			continue
		}
		byteCount += len(document)
	}
	return byteCount
}

func structuredRequestByteCount(request model.StructuredResponseRequest) int {
	byteCount := 0
	for _, message := range request.Messages {
		byteCount += messageByteCount(message.Content, message.Parts)
	}
	return byteCount
}

func messageByteCount(content string, parts []model.MessagePart) int {
	byteCount := len(content)
	for _, part := range parts {
		byteCount += len(part.Text) + len(part.DataBase64)
	}
	return byteCount
}
