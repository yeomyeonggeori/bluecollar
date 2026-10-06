package acpagent

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type recordedLLMCall struct {
	taskRunID string
	hasWire   bool
}

type recordingLLMCallRepository struct {
	mutex sync.Mutex
	calls []recordedLLMCall
}

func (repository *recordingLLMCallRepository) InsertLLMCall(taskEvent agentcontract.TaskEvent, record agentcontract.LLMCallRecord) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.calls = append(repository.calls, recordedLLMCall{taskRunID: taskEvent.TaskRunID, hasWire: record.Exchange != nil})
	return nil
}

func (repository *recordingLLMCallRepository) recorded() []recordedLLMCall {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return append([]recordedLLMCall{}, repository.calls...)
}

var _ taskstate.LLMCallRepository = (*recordingLLMCallRepository)(nil)

type wireRecordingLanguageModel struct {
	model.LanguageModelProvider
}

func (languageModel wireRecordingLanguageModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	model.RecordWireExchange(ctx, model.WireExchange{Endpoint: "test://model", Request: "request", Response: "response"})
	return languageModel.LanguageModelProvider.GenerateStructuredResponse(ctx, request)
}

type countingDecisionModel struct {
	model.DecisionModel
	mutex     sync.Mutex
	callCount int
}

func (decisionModel *countingDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	decisionModel.callCount++
	decisionModel.mutex.Unlock()
	return decisionModel.DecisionModel.Decide(ctx, request)
}

func TestTheTierThatTheTaskLevelNamesAnswersTheTurn(t *testing.T) {
	hostCalls := []hostToolCall{}
	lowTier := &scriptedLanguageModel{}
	extraLowTier := noteWriteTurnScript()
	options := testOptions(lowTier)
	options.LanguageModels.XLow = extraLowTier

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &hostClient{})
	if _, errorValue := host.prompt(t, nil, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if extraLowTier.callCount == 0 || len(hostCalls) != 1 {
		t.Fatalf("the turn was routed to xlow, so the xlow provider has to do its work, got %d calls and %d tool calls", extraLowTier.callCount, len(hostCalls))
	}
	if len(lowTier.actionPrompts) != 0 {
		t.Fatalf("the low tier answers routing, not a turn another tier was named for, got %d action prompts", len(lowTier.actionPrompts))
	}
}

func TestTheDecisionModelTheHostGivesIsTheOneTheLoopAsks(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := noteWriteTurnScript()
	decisionModel := &countingDecisionModel{DecisionModel: scriptedDecisionModel(languageModel)}
	options := testOptions(languageModel)
	options.DecisionModel = decisionModel

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &hostClient{})
	if _, errorValue := host.prompt(t, nil, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if decisionModel.callCount == 0 {
		t.Fatal("the host's decision model was never asked, so the loop is deciding with something else")
	}
}

func TestWireExchangesLandInTheHostsLLMCallRepository(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := wireRecordingLanguageModel{noteWriteTurnScript()}
	repository := &recordingLLMCallRepository{}
	options := testOptions(languageModel)
	options.LLMCallRepository = repository

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &hostClient{})
	if _, errorValue := host.prompt(t, nil, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	hasWireExchange := slices.ContainsFunc(repository.recorded(), func(call recordedLLMCall) bool { return call.hasWire })
	if !hasWireExchange {
		t.Fatalf("a call that carries its wire exchange has to reach the host's llm_call, got %+v", repository.recorded())
	}
}

func TestAPromptNamingATaskRunMakesTheLoopRunUnderThatIdentifier(t *testing.T) {
	hostCalls := []hostToolCall{}
	repository := &recordingLLMCallRepository{}
	options := testOptions(wireRecordingLanguageModel{noteWriteTurnScript()})
	options.LLMCallRepository = repository

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &hostClient{})
	if _, errorValue := host.prompt(t, map[string]any{TaskRunMetaKey: "host-run-42"}, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	recorded := repository.recorded()
	if len(recorded) == 0 {
		t.Fatal("no call was recorded to tell the run apart")
	}
	for _, call := range recorded {
		if call.taskRunID != "host-run-42" {
			t.Fatalf("the host named the run, and the loop ran under %q instead", call.taskRunID)
		}
	}
}

func TestAHandedBackLedgerReplaysIntoTheRunTheHostNamed(t *testing.T) {
	hostCalls := []hostToolCall{}
	firstHost := &hostClient{}
	firstSession := openPipedHost(t, testOptions(noteWriteTurnScript()), publishedCatalogTransport(t, &hostCalls), firstHost)
	if _, errorValue := firstSession.prompt(t, nil, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	repository := &recordingLLMCallRepository{}
	options := testOptions(wireRecordingLanguageModel{&scriptedLanguageModel{contents: []string{
		`{"action":"reply","final":true,"message":"이미 남겼습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}})
	options.LLMCallRepository = repository
	resumedSession := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &hostClient{})
	meta := map[string]any{TaskRunMetaKey: "host-run-43", agentcontract.LedgerMetaKey: firstHost.keptLedger()}
	if _, errorValue := resumedSession.prompt(t, meta, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	recorded := repository.recorded()
	if len(recorded) == 0 || recorded[len(recorded)-1].taskRunID != "host-run-43" {
		t.Fatalf("the replayed ledger and the resumed turn belong to the run the host named, got %+v", recorded)
	}
	if len(hostCalls) != 1 {
		t.Fatalf("a replayed ledger must not run the tool again, got %d calls", len(hostCalls))
	}
}

func TestAMidTurnCheckpointReachesAStandardClientAsAThoughtChunk(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := &scriptedLanguageModel{level: "medium", contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"회의록"},"message":"노트를 남기는 중입니다"}`,
		`{"action":"reply","final":true,"message":"노트를 남겼습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}
	client := &recordingHost{}

	host := openPipedHost(t, testOptions(languageModel), publishedCatalogTransport(t, &hostCalls), client)
	if _, errorValue := host.prompt(t, nil, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	checkpoints := checkpointChunksOf(client.keptUpdates())
	if len(checkpoints) != 1 || checkpoints[0].Content.Text == nil || checkpoints[0].Content.Text.Text != "노트를 남기는 중입니다" {
		t.Fatalf("the checkpoint's words have to arrive as a thought chunk marked in _meta, got %+v", checkpoints)
	}
	if client.agentMessage != "" {
		t.Fatalf("a checkpoint is not the answer, so it must not join the message the client renders as the reply, got %q", client.agentMessage)
	}
}

func checkpointChunksOf(updates []acp.SessionUpdate) []*acp.SessionUpdateAgentThoughtChunk {
	chunks := []*acp.SessionUpdateAgentThoughtChunk{}
	for _, update := range updates {
		if chunk := update.AgentThoughtChunk; chunk != nil && chunk.Meta[CheckpointMetaKey] != nil {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

type blockingCatalog struct {
	started chan struct{}
	release chan struct{}
}

func (catalog blockingCatalog) transport(t *testing.T) mcp.Transport {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "host", Version: "test"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "note_write",
		Description: "write a note",
		InputSchema: map[string]any{"type": "object"},
		Meta:        mcp.Meta{"bluecollar/sideEffectClass": "state_change"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		catalog.started <- struct{}{}
		<-catalog.release
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "note written"}}}, nil
	})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go server.Run(t.Context(), serverTransport)
	return clientTransport
}

func waitForLedgerEvent(t *testing.T, client *hostClient, eventName string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(client.ledgerEventNames(), eventName) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the ledger never showed %q, got %v", eventName, client.ledgerEventNames())
}

func TestASteerSentMidTurnIsReadByTheLoopBeforeItsNextStep(t *testing.T) {
	languageModel := &scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{}}`,
		`{"action":"reply","final":true,"message":"done","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}
	catalog := blockingCatalog{started: make(chan struct{}, 1), release: make(chan struct{})}
	client := &hostClient{}
	host := openPipedHost(t, testOptions(languageModel), catalog.transport(t), client)
	finished := make(chan error, 1)
	go func() {
		_, errorValue := host.prompt(t, nil, acp.TextBlock("회의록 정리해줘"))
		finished <- errorValue
	}()
	<-catalog.started

	notifyError := host.connection.NotifyExtension(t.Context(), SteerMethod, SteerNotification{SessionID: host.sessionID, Instruction: "write it in English"})
	waitForLedgerEvent(t, client, agentcontract.TaskEventAgentSteerReceived)
	close(catalog.release)

	if notifyError != nil {
		t.Fatalf("steer notification: %v", notifyError)
	}
	if errorValue := <-finished; errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}
	if !slices.Contains(client.ledgerEventNames(), agentcontract.TaskEventTaskSteerApplied) {
		t.Fatalf("the loop has to read the steer between steps, got %v", client.ledgerEventNames())
	}
	if !containsSubstring(languageModel.actionPrompts, "write it in English") {
		t.Fatal("the instruction has to reach the model's next step")
	}
}

func TestAnImageBlockReachesTheModelAndTheAgentSaysItTakesImages(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := noteWriteTurnScript()
	imageData := base64.StdEncoding.EncodeToString([]byte("not really a png"))

	host := openPipedHost(t, testOptions(languageModel), publishedCatalogTransport(t, &hostCalls), &hostClient{})
	_, errorValue := host.prompt(t, nil, acp.TextBlock("이 사진 정리해줘"), acp.ImageBlock(imageData, "image/png"))

	if errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}
	if !host.capabilities.PromptCapabilities.Image {
		t.Fatal("a client only sends an image to an agent that declared promptCapabilities.image")
	}
	if !slices.Contains(languageModel.sawImageData, imageData) {
		t.Fatalf("the image the person attached never reached the model, got %d images", len(languageModel.sawImageData))
	}
}

func TestAToolTheHostChecksRunsWithoutBluecollarAskingAgain(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := questioningLanguageModel{&scriptedLanguageModel{contents: []string{
		`{"action":"continue","toolName":"note_write","toolInput":{"text":"회의록"}}`,
		`{"action":"reply","final":true,"message":"done","goalSatisfied":true}`,
	}}}
	options := testOptions(languageModel)
	client := &askedHostClient{selectedOption: approveOptionID}

	host := openPipedHost(t, options, hostGatedCatalog(t, &hostCalls), client)
	if _, errorValue := host.prompt(t, nil, acp.TextBlock("회의록 남겨줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if len(client.requests) != 0 {
		t.Fatalf("the host asks the person for this tool, so asking again makes it twice, got %d requests", len(client.requests))
	}
	if len(hostCalls) != 1 {
		t.Fatalf("a call the host checked has to run, got %d", len(hostCalls))
	}
}

type panickingLanguageModel struct {
	*scriptedLanguageModel
	panicsRemaining int
}

func (languageModel *panickingLanguageModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "bluecollar_agent_turn_action" && languageModel.panicsRemaining > 0 {
		languageModel.panicsRemaining--
		panic("provider adapter blew up")
	}
	return languageModel.scriptedLanguageModel.GenerateStructuredResponse(ctx, request)
}

func TestAPanicInATurnFailsThatTurnAndLeavesTheAgentServing(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := &panickingLanguageModel{scriptedLanguageModel: noteWriteTurnScript(), panicsRemaining: 1}

	host := openPipedHost(t, testOptions(languageModel), publishedCatalogTransport(t, &hostCalls), &hostClient{})
	_, firstError := host.prompt(t, nil, acp.TextBlock("회의록 정리해줘"))
	secondResponse, secondError := host.prompt(t, nil, acp.TextBlock("다시 해줘"))

	if firstError == nil {
		t.Fatal("a turn that panicked has to come back failed, not as a clean end of turn")
	}
	if secondError != nil || secondResponse.StopReason == "" {
		t.Fatalf("the agent must still serve after a failed turn, got %v", secondError)
	}
}

func writeSkill(t *testing.T, workingDirectory string, name string, description string, body string) {
	t.Helper()
	skillDirectory := filepath.Join(workingDirectory, workingDirectorySkillsPath, name)
	if errorValue := os.MkdirAll(skillDirectory, 0o755); errorValue != nil {
		t.Fatal(errorValue)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body + "\n"
	if errorValue := os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"), []byte(content), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func TestSkillsInTheWorkingDirectoryAreOfferedWhenTheHostGivesNone(t *testing.T) {
	hostCalls := []hostToolCall{}
	workingDirectory := t.TempDir()
	writeSkill(t, workingDirectory, "meeting-minutes", "정리해줘 회의록 meeting minutes", "Always list decisions first.")
	languageModel := noteWriteTurnScript()

	host := openPipedHost(t, testOptions(languageModel), publishedCatalogTransport(t, &hostCalls), &recordingHost{directory: workingDirectory})
	if _, errorValue := host.prompt(t, nil, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if !containsSubstring(languageModel.actionPrompts, "meeting-minutes") {
		t.Fatal("a skill sitting in the working directory has to be offered to the model")
	}
}

func TestASkillLoaderTheHostGivesReplacesTheWorkingDirectory(t *testing.T) {
	hostCalls := []hostToolCall{}
	workingDirectory := t.TempDir()
	writeSkill(t, workingDirectory, "meeting-minutes", "정리해줘 회의록 meeting minutes", "Always list decisions first.")
	languageModel := noteWriteTurnScript()
	options := testOptions(languageModel)
	handedOverBundle := agentcontract.InstructionBundle{Skills: []agentcontract.SkillInstruction{{
		Name: "host-skill", Description: "정리해줘 회의록 from the host", Prompt: "Host prompt body.",
		Source: agentcontract.InstructionSource{Path: "host:host-skill", SkillName: "host-skill"},
	}}}

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &recordingHost{directory: workingDirectory})
	if _, errorValue := host.prompt(t, map[string]any{InstructionBundleMetaKey: handedOverBundle}, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if !containsSubstring(languageModel.actionPrompts, "host-skill") {
		t.Fatal("the host's own skill has to be offered")
	}
	if containsSubstring(languageModel.actionPrompts, "meeting-minutes") {
		t.Fatal("once the host names its skills, the working directory is not a second source")
	}
}

func TestAPinnedSkillIsLoadedWithoutBeingRetrieved(t *testing.T) {
	hostCalls := []hostToolCall{}
	workingDirectory := t.TempDir()
	writeSkill(t, workingDirectory, "tax-rules", "unrelated words only", "Pinned body marker.")
	languageModel := noteWriteTurnScript()
	options := testOptions(languageModel)
	pinnedRequest := agentcontract.AgentTurnRequest{PinnedSkillNames: []string{"tax-rules"}}

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &recordingHost{directory: workingDirectory})
	if _, errorValue := host.prompt(t, map[string]any{TurnRequestMetaKey: pinnedRequest}, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if !containsSubstring(languageModel.actionPrompts, "Pinned body marker.") {
		t.Fatal("a pinned skill's body has to reach the model whatever the request says")
	}
}

type choosingSkillRetriever struct {
	chosenSkillName string
	mutex           sync.Mutex
	callCount       int
}

func (retriever *choosingSkillRetriever) Available(_ agentcontract.AgentRequest, skills []agentcontract.SkillInstruction) []agentcontract.SkillInstruction {
	return skills
}

func (retriever *choosingSkillRetriever) Retrieve(_ context.Context, _ agentcontract.AgentRequest, _ []agentcontract.SkillInstruction, _ int) agentcontract.SkillRetrievalResult {
	return retriever.chosen()
}

func (retriever *choosingSkillRetriever) Search(_ context.Context, _ agentcontract.AgentRequest, _ []agentcontract.SkillInstruction, _ agentcontract.SkillSearchQuerySet, _ int) agentcontract.SkillRetrievalResult {
	return retriever.chosen()
}

func (retriever *choosingSkillRetriever) Refresh(context.Context, []agentcontract.SkillInstruction) {}

func (retriever *choosingSkillRetriever) chosen() agentcontract.SkillRetrievalResult {
	retriever.mutex.Lock()
	defer retriever.mutex.Unlock()
	retriever.callCount++
	return agentcontract.SkillRetrievalResult{
		RetrievalMode:      "host",
		CandidateCount:     1,
		SelectedCandidates: []agentcontract.SkillCandidate{{Name: retriever.chosenSkillName, Score: 1}},
	}
}

func TestASkillRetrieverTheHostGivesDecidesWhichSkillIsOffered(t *testing.T) {
	hostCalls := []hostToolCall{}
	workingDirectory := t.TempDir()
	writeSkill(t, workingDirectory, "meeting-minutes", "정리해줘 회의록 minutes marker", "Always list decisions first.")
	writeSkill(t, workingDirectory, "tax-rules", "ledger rounding marker", "Tax body.")
	languageModel := noteWriteTurnScript()
	retriever := &choosingSkillRetriever{chosenSkillName: "tax-rules"}
	options := testOptions(languageModel)
	options.Skills.Retriever = retriever

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &recordingHost{directory: workingDirectory})
	if _, errorValue := host.prompt(t, nil, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if retriever.callCount == 0 || !containsSubstring(languageModel.actionPrompts, "ledger rounding marker") {
		t.Fatalf("the host's retriever picked tax-rules, so the model has to be offered it, retriever calls %d", retriever.callCount)
	}
	if containsSubstring(languageModel.actionPrompts, "minutes marker") {
		t.Fatal("a skill the host's retriever did not pick must not be offered")
	}
}

func TestATurnRequestTheHostHandsOverReachesTheLoopWithItsAttachmentsAndContext(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := &scriptedLanguageModel{level: "medium", contents: []string{
		`{"action":"reply","final":true,"message":"봤습니다","goalSatisfied":true}`,
	}}
	handedOver := agentcontract.AgentTurnRequest{
		IsRuntimeRestartResume: true,
		InputParts:             []agentcontract.AgentPart{{Type: agentcontract.AgentPartTypeFile, File: &agentcontract.AgentFilePart{Filename: "meeting-notes.csv", Path: "/workspace/inbox/meeting-notes.csv"}}},
		VisibleContext: agentcontract.VisibleContext{CurrentMaterials: []agentcontract.VisibleContextMaterial{
			{Filename: "meeting-notes.csv", URL: "https://files.example.com/meeting-notes.csv", IsAvailable: true},
		}},
	}
	routing := &routingProbeLanguageModel{scriptedLanguageModel: languageModel}
	options := testOptions(routing)
	options.LanguageModels.XLow = routing

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &hostClient{})
	if _, errorValue := host.prompt(t, map[string]any{TurnRequestMetaKey: handedOver}, acp.TextBlock("첨부 요약해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if routing.routingCalls != 0 {
		t.Fatalf("the host already routed this turn, so the agent routing it again is a second opinion that can disagree, got %d routing calls", routing.routingCalls)
	}
	if !strings.Contains(strings.Join(routing.partTexts, "\n"), "/workspace/inbox/meeting-notes.csv") {
		t.Fatalf("the attached file's path has to reach the model, got %v", routing.partTexts)
	}
	if len(languageModel.actionPrompts) == 0 {
		t.Fatal("the loop never asked the model")
	}
	if !strings.Contains(languageModel.actionPrompts[0], "https://files.example.com/meeting-notes.csv") {
		t.Fatalf("the context the host sees has to reach the model, got %v", languageModel.actionPrompts)
	}
}

type routingProbeLanguageModel struct {
	*scriptedLanguageModel
	routingCalls int
	partTexts    []string
}

func (languageModel *routingProbeLanguageModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "bluecollar_turn_router" {
		languageModel.routingCalls++
	}
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			languageModel.partTexts = append(languageModel.partTexts, part.Text)
		}
	}
	return languageModel.scriptedLanguageModel.GenerateStructuredResponse(ctx, request)
}
