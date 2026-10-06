package loop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type contextSummaryLanguageModel struct {
	nativeAgentActionLanguageModel
	content  string
	requests []model.StructuredResponseRequest
}

func (provider *contextSummaryLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	provider.requests = append(provider.requests, request)
	return model.StructuredResponse{Content: provider.content}, nil
}

type contextSelectionDecisionModel struct {
	requests      []model.DecisionRequest
	answers       map[string]model.DecisionAnswer
	defaultChoice string
	errorValue    error
}

func (provider *contextSelectionDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	provider.requests = append(provider.requests, request)
	if provider.errorValue != nil {
		return model.DecisionResponse{}, provider.errorValue
	}
	answers := provider.answers
	if answers == nil {
		answers = map[string]model.DecisionAnswer{}
		for key := range request.Questions {
			answers[key] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: provider.defaultChoice}
		}
	}
	return model.DecisionResponse{Answers: answers}, nil
}

func completeSummaryResponseForTest(document string) string {
	var content TaskContextSummaryContent
	if errorValue := json.Unmarshal([]byte(document), &content); errorValue != nil {
		panic(errorValue)
	}
	return marshalEventBody(normalizeSummaryContent(content))
}

func newContextCompactionTestServices(provider model.LanguageModelProvider, options TurnOptions) turnRunnerTestServices {
	services := newTurnRunnerTestServices(provider, options)
	services.runner.UseDecisionModel(&contextSelectionDecisionModel{defaultChoice: "false"})
	return services
}

func numberedConversationSummaryObservations(count int, size int, marker string) []turnObservation {
	observations := numberedContextSummaryObservations(count, size, marker)
	for index := range observations {
		observations[index].Tool = ""
		observations[index].AssistantText = observations[index].ContentText()
		observations[index].Summary = observations[index].ContentText()
	}
	return observations
}

func TestContextSelectionKeepsConversationWhenToolOmissionIsEnough(t *testing.T) {
	provider := &contextSummaryLanguageModel{content: completeSummaryResponseForTest(`{"goal":"inspect","constraints":["preserve the chosen format"]}`)}
	services := newContextCompactionTestServices(provider, TurnOptions{ContextWindowTokens: 100000})
	decisionModel := services.runner.decisionModel.(*contextSelectionDecisionModel)
	observations := numberedContextSummaryObservations(24, 18000, "tool-only-record")
	for index := range observations {
		observations[index].AssistantText = "I will inspect the next record."
		observations[index].ToolInput = json.RawMessage(`{"path":"sample.txt"}`)
	}
	request := AgentTurnRequest{Prompt: "inspect the collection", VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{{Speaker: "user", Text: "preserve the chosen format"}}}}
	state := agentTaskState{Request: request, Options: services.runner.options, Observations: observations}
	selected := services.runner.promptStateForAction(t.Context(), "run-1", state)
	if len(provider.requests) != 1 || len(decisionModel.requests) != 1 || len(decisionModel.requests[0].Questions) != 14 {
		t.Fatalf("expected one summary and one batched decision for 14 older records, got %d summaries and %+v", len(provider.requests), decisionModel.requests)
	}
	if strings.Contains(structuredRequestText(provider.requests[0]), "tool-only-record") || strings.Contains(structuredRequestText(provider.requests[0]), "sample.txt") {
		t.Fatal("tool input or result leaked into the non-tool summary request")
	}
	if !strings.Contains(marshalEventBody(decisionModel.requests[0].State), "tool-only-record-001") || !strings.Contains(marshalEventBody(decisionModel.requests[0].State), "sample.txt") {
		t.Fatal("the decision model did not receive full call arguments and results")
	}
	if len(selected.Request.VisibleContext.Messages) != 1 || selected.Request.VisibleContext.Messages[0].Text != request.VisibleContext.Messages[0].Text {
		t.Fatal("tool omission also replaced the conversation")
	}
	for _, observation := range selected.Observations {
		if observation.ObservationID == "obs-001" && (observation.Tool != "" || observation.AssistantText != observations[0].AssistantText) {
			t.Fatal("omitting a tool record must retain its assistant output until conversation compaction is needed")
		}
		if observation.ObservationID == "obs-024" && observation.ContentText() != observations[23].ContentText() {
			t.Fatal("the most recent result was changed")
		}
	}
	before := services.runner.estimateActionPromptTokenCount(state)
	after := services.runner.estimateActionPromptTokenCount(selected)
	if after >= compactionTriggerTokenThreshold(state.Options.ContextWindowTokens) || after >= before {
		t.Fatalf("selection failed to bring the request under budget: %d -> %d", before, after)
	}
	if observations[0].Tool == "" || len(observations[0].ContentText()) < 18000 {
		t.Fatal("selection mutated the canonical record")
	}
	t.Logf("tool selection estimated tokens %d -> %d; summary calls=1 decision calls=1", before, after)
}

func TestContextSelectionReusesSummaryOnlyWhenConversationMustShrink(t *testing.T) {
	provider := &contextSummaryLanguageModel{content: completeSummaryResponseForTest(`{"goal":"inspect","context":"collection review","constraints":["keep the exact output format"],"pendingSteps":["review the remaining records"],"openQuestions":["which destination to use"]}`)}
	services := newContextCompactionTestServices(provider, TurnOptions{ContextWindowTokens: 100000})
	services.runner.UseDecisionModel(&contextSelectionDecisionModel{defaultChoice: "true"})
	store := &recordingSpillStore{locator: "/workspace/compacted.jsonl", bytes: 220000, hint: "Read this file."}
	services.runner.UseToolResultSpillStore(store)
	messages := []agentcontract.VisibleContextMessage{}
	for index := 0; index < 12; index++ {
		messages = append(messages, agentcontract.VisibleContextMessage{Speaker: "user", Text: strings.Repeat("conversation fact ", 1250)})
	}
	observations := numberedContextSummaryObservations(12, 3000, "relevant-result")
	observations[0].AssistantText = "I will review these records."
	request := AgentTurnRequest{Prompt: "current request stays exact", WorkspaceRootPath: "/workspace", VisibleContext: agentcontract.VisibleContext{Messages: messages}}
	state := agentTaskState{Request: request, Options: services.runner.options, Observations: observations}
	selected := services.runner.promptStateForAction(t.Context(), "run-2", state)
	if len(provider.requests) != 1 || len(selected.Request.VisibleContext.Messages) != 1 || len(request.VisibleContext.Messages) != 12 {
		t.Fatal("conversation replacement must reuse one summary and preserve the canonical input")
	}
	if selected.Request.Prompt != request.Prompt || selected.Request.VisibleContext.Messages[0].Text != messages[11].Text {
		t.Fatal("the current request or latest conversation message was replaced")
	}
	text := marshalEventBody(selected.Observations)
	if !strings.Contains(text, "keep the exact output format") || !strings.Contains(text, "which destination to use") || !strings.Contains(text, "relevant-result-001") {
		t.Fatal("the summary lost constraints, open questions or a relevant tool result")
	}
	if len(store.saved) != 1 || !strings.Contains(store.saved[0].Content, "conversation_message") || !strings.Contains(store.saved[0].Content, observations[0].AssistantText) {
		t.Fatal("replaced conversation and assistant output were not saved whole")
	}
	before := services.runner.estimateActionPromptTokenCount(state)
	after := services.runner.estimateActionPromptTokenCount(selected)
	if after >= compactionTriggerTokenThreshold(state.Options.ContextWindowTokens) {
		t.Fatalf("conversation compaction still exceeds its budget: %d -> %d", before, after)
	}
	t.Logf("conversation compaction estimated tokens %d -> %d; summary calls=%d", before, after, len(provider.requests))
}

func TestContextSelectionRetainsRecordsOnMissingInvalidOrFailedDecisions(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		answer     model.DecisionAnswer
		errorValue error
	}{
		{name: "missing"},
		{name: "invalid choice", answer: model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: "unknown"}},
		{name: "wrong answer type", answer: model.DecisionAnswer{Type: model.DecisionQuestionTypeNoul, Choice: "false"}},
		{name: "failed provider", errorValue: errors.New("decision provider unavailable")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			provider := &contextSelectionDecisionModel{answers: map[string]model.DecisionAnswer{"obs-001": testCase.answer}, errorValue: testCase.errorValue}
			services := newTurnRunnerTestServices(&sequenceLanguageModel{}, TurnOptions{})
			services.runner.UseDecisionModel(provider)
			removed := services.runner.irrelevantToolObservationIDs(t.Context(), "run-3", agentTaskState{}, TaskContextSummary{}, numberedContextSummaryObservations(1, 100, "record"))
			if len(removed) != 0 {
				t.Fatal("an uncertain decision removed a record")
			}
		})
	}
}

func TestContextSelectionProtectsFailureEffectsAndRecordedDependencies(t *testing.T) {
	observations := numberedContextSummaryObservations(24, 18000, "record")
	observations[2].Effects = []toolcontract.ResourceEffect{{ObjectType: "task", ID: "task-sample", Effect: "created"}}
	observations[23].RelatedResultIDs = []string{"obs-001"}
	failure := newFailureObservation("obs-006", "continue", "bash", "service unavailable", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "bash")
	failure.ToolInputKey = "bash\x00failed"
	observations[5] = failure
	for index := 6; index < len(observations); index++ {
		observations[index].ToolIsReadOnly = true
	}
	pinned := pinnedPromptObservationIDs(observations, nil)
	candidates := toolSelectionCandidates(observations[:14], TaskContextSummary{}, pinned)
	for _, observation := range candidates {
		if observation.ObservationID == "obs-003" || observation.ObservationID == "obs-006" || observation.ObservationID == "obs-014" {
			t.Fatal("recorded effects or active failure recovery were offered for removal")
		}
	}
	if len(omissionsKeepingDependencies([]string{"obs-001"}, observations)) != 0 {
		t.Fatal("a record referenced by a retained result was removed")
	}
}

func TestStructuredContextSummaryRejectsInvalidShapeAndPreservesExactFacts(t *testing.T) {
	valid := completeSummaryResponseForTest(`{"goal":"inspect","constraints":["preserve  double spaces"],"artifacts":["` + strings.Repeat("a", 600) + `"]}`)
	content, errorValue := decodeTaskContextSummaryContent(valid)
	if errorValue != nil || len(normalizeSummaryContent(content).Artifacts[0]) != 600 || content.Constraints[0] != "preserve  double spaces" {
		t.Fatalf("exact facts were changed: %+v %v", content, errorValue)
	}
	for _, document := range []string{`{"goal":"inspect"}`, strings.TrimSuffix(valid, "}") + `,"unexpected":true}`, strings.Replace(valid, `"constraints":["preserve  double spaces"]`, `"constraints":null`, 1)} {
		if _, errorValue := decodeTaskContextSummaryContent(document); errorValue == nil {
			t.Fatalf("invalid structured summary was accepted: %s", document)
		}
	}
}

func TestContextCompactionBelowBudgetMakesNoModelCalls(t *testing.T) {
	provider := &contextSummaryLanguageModel{}
	services := newContextCompactionTestServices(provider, TurnOptions{ContextWindowTokens: 100000})
	state := agentTaskState{Request: AgentTurnRequest{Prompt: "inspect"}, Options: services.runner.options, Observations: numberedContextSummaryObservations(12, 100, "record")}
	selected := services.runner.promptStateForAction(t.Context(), "run-4", state)
	if len(provider.requests) != 0 || len(services.runner.decisionModel.(*contextSelectionDecisionModel).requests) != 0 || len(selected.Observations) != 12 {
		t.Fatal("under-budget requests must keep their records without compaction calls")
	}
}

func TestContextSelectionCheckpointPreservesToolAndIterationCounts(t *testing.T) {
	provider := &contextSummaryLanguageModel{content: completeSummaryResponseForTest(`{"goal":"inspect"}`)}
	services := newContextCompactionTestServices(provider, TurnOptions{ContextWindowTokens: 100000})
	observations := numberedContextSummaryObservations(24, 18000, "record")
	for index := range observations {
		observations[index].AssistantText = "Inspecting a record."
	}
	taskRun := services.taskRunService.CreateTaskRun("person-sample", "conversation-sample", "inspect")
	for _, observation := range observations {
		services.taskEventService.AppendTaskEvent(taskRun.TaskRunID, "tool.bash.result", marshalEventBody(observation))
	}
	request := AgentTurnRequest{Prompt: "inspect"}
	state := agentTaskState{Request: request, Options: services.runner.options, Observations: observations}
	selected := services.runner.promptStateForAction(t.Context(), taskRun.TaskRunID, state)
	events := services.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	restored, errorValue := restoreAgentTaskState(request, state.Options, taskRun, events)
	if errorValue != nil || restored.IterationCount != 24 || restored.ToolCallCount != 24 {
		t.Fatalf("selection changed the consumed budget after restart: iterations=%d tools=%d error=%v", restored.IterationCount, restored.ToolCallCount, errorValue)
	}
	reshown := services.runner.promptStateForAction(t.Context(), taskRun.TaskRunID, restored)
	if marshalEventBody(selected.Observations) != marshalEventBody(reshown.Observations) {
		t.Fatal("restart did not preserve assistant-only records and relevant full tool results")
	}
	if len(provider.requests) != 1 || len(services.runner.decisionModel.(*contextSelectionDecisionModel).requests) != 1 {
		t.Fatal("restoring the already compacted projection repeated model calls")
	}
}

func TestConversationCompactionWithoutDecisionModelRemainsAvailable(t *testing.T) {
	provider := &contextSummaryLanguageModel{content: completeSummaryResponseForTest(`{"goal":"inspect","constraints":["keep CSV"]}`)}
	services := newTurnRunnerTestServices(provider, TurnOptions{ContextWindowTokens: 1000})
	request := AgentTurnRequest{Prompt: "inspect", VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
		{Speaker: "user", Text: strings.Repeat("earlier conversation ", 2000)},
		{Speaker: "user", Text: "keep CSV"},
	}}}
	state := agentTaskState{Request: request, Options: services.runner.options}
	selected := services.runner.promptStateForAction(t.Context(), "no-decision-model", state)
	if len(provider.requests) != 1 || services.runner.estimateActionPromptTokenCount(selected) >= services.runner.estimateActionPromptTokenCount(state) {
		t.Fatalf("conversation compaction: summary calls=%d, tokens=%d -> %d, events=%s", len(provider.requests), services.runner.estimateActionPromptTokenCount(state), services.runner.estimateActionPromptTokenCount(selected), marshalEventBody(services.taskRunService.ListTaskEvent("no-decision-model")))
	}
	if !strings.Contains(marshalEventBody(selected.Observations), "keep CSV") {
		t.Fatal("the fallback conversation summary lost its constraint")
	}
}

func TestInvalidSummaryReferencesPreserveRecordsAndDoNotRepeat(t *testing.T) {
	provider := &contextSummaryLanguageModel{content: completeSummaryResponseForTest(`{"goal":"inspect","evidence":[{"fact":"a claim","observationIDs":["invented-observation"]}]}`)}
	services := newContextCompactionTestServices(provider, TurnOptions{ContextWindowTokens: 1000})
	state := agentTaskState{Request: AgentTurnRequest{Prompt: "inspect"}, Options: services.runner.options, Observations: numberedContextSummaryObservations(24, 2000, "record")}
	for index := 0; index < 2; index++ {
		selected := services.runner.promptStateForAction(t.Context(), "invalid-summary", state)
		if marshalEventBody(selected.Observations) != marshalEventBody(state.Observations) {
			t.Fatal("invalid summary references changed the prompt records")
		}
	}
	if len(provider.requests) != 1 || len(services.runner.decisionModel.(*contextSelectionDecisionModel).requests) != 0 {
		t.Fatal("an invalid summary was reused for selection or requested again for the same input")
	}
}

func TestCompactedTranscriptChainsPriorSavedRecords(t *testing.T) {
	transcript := compactedPlanTranscript(taskContextCompactionPlan{PreviousTranscript: "Read /workspace/previous.jsonl", ConversationMessages: []agentcontract.VisibleContextMessage{{Speaker: "user", Text: "keep CSV"}}})
	if !strings.Contains(transcript, "/workspace/previous.jsonl") || !strings.Contains(transcript, "keep CSV") {
		t.Fatal("a rolling transcript dropped the path to earlier saved records or replaced input")
	}
}
