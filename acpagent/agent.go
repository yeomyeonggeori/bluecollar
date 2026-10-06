package acpagent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type session struct {
	catalog        *catalog
	taskRunIDMutex sync.Mutex
	taskRunID      string
	kernel         *loop.AgentKernel
	taskRuns       *taskstate.TaskRunService
	taskEvents     *taskstate.TaskEventService
	gate           *approval.Gate
}

type Agent struct {
	options          Options
	languageModel    model.LanguageModelProvider
	decisionPlanner  intake.DecisionPlanner
	resolveTransport transportResolver
	sessionUpdates   *deferredSessionUpdateSender
	permissions      *deferredPermissionRequester

	mutex             sync.Mutex
	sessionsByID      map[acp.SessionId]*session
	nextSessionNumber int
}

func New(options Options) (*Agent, error) {
	if errorValue := options.validate(); errorValue != nil {
		return nil, errorValue
	}
	return &Agent{
		options:          options,
		languageModel:    options.LanguageModels.Low,
		decisionPlanner:  intake.NewDecisionPlanner(options.DecisionModel, nil, nil),
		resolveTransport: transportForServer,
		sessionUpdates:   &deferredSessionUpdateSender{ready: make(chan struct{})},
		permissions:      &deferredPermissionRequester{ready: make(chan struct{})},
		sessionsByID:     map[acp.SessionId]*session{},
	}, nil
}

func (runningAgent *Agent) Connect(connection *acp.AgentSideConnection) {
	runningAgent.sessionUpdates.connect(connection)
	runningAgent.permissions.connect(connection)
}

func (runningAgent *Agent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			McpCapabilities:    acp.McpCapabilities{Http: true},
			PromptCapabilities: acp.PromptCapabilities{Image: true},
		},
	}, nil
}

func (runningAgent *Agent) NewSession(ctx context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	openedCatalog, errorValue := openCatalog(ctx, request.McpServers, runningAgent.resolveTransport)
	if errorValue != nil {
		return acp.NewSessionResponse{}, errorValue
	}
	taskEvents := taskstate.NewTaskEventService()
	taskRuns := taskstate.NewTaskRunService(taskEvents)
	kernel := loop.NewAgentKernel(taskRuns, taskstate.NewTaskStepService())
	kernel.UseLanguageModelProvider(runningAgent.languageModel)
	kernel.UseTaskTierLanguageModels(runningAgent.options.LanguageModels)
	kernel.UseDecisionModel(runningAgent.options.DecisionModel)
	kernel.UseToolResultImageSource(openedCatalog)
	kernel.UseInstructionBundleLoader(instructionBundleLoaderFor(runningAgent.options.Skills, request.Cwd))
	if runningAgent.options.Skills.Retriever != nil {
		kernel.UseSkillRetriever(runningAgent.options.Skills.Retriever)
	}
	if runningAgent.options.LLMCallRepository != nil {
		taskEvents.UseLLMCallRepository(runningAgent.options.LLMCallRepository)
	}

	runningAgent.mutex.Lock()
	defer runningAgent.mutex.Unlock()
	runningAgent.nextSessionNumber++
	sessionID := acp.SessionId("bluecollar-" + strconv.Itoa(runningAgent.nextSessionNumber))
	runningAgent.sessionsByID[sessionID] = &session{
		catalog:    openedCatalog,
		kernel:     kernel,
		taskRuns:   taskRuns,
		taskEvents: taskEvents,
		gate:       approval.New(taskRuns, runningAgent.languageModel, permissionAsker{requester: runningAgent.permissions, sessionID: sessionID}),
	}
	return acp.NewSessionResponse{SessionId: sessionID}, nil
}

func (runningAgent *Agent) Prompt(ctx context.Context, request acp.PromptRequest) (promptResponse acp.PromptResponse, errorValue error) {
	openSession, isKnown := runningAgent.session(request.SessionId)
	if !isKnown {
		return acp.PromptResponse{}, errors.New("bluecollar has no session by that id; open one with session/new first")
	}
	defer failTurnOnPanic(openSession, &promptResponse, &errorValue)
	return runningAgent.runPrompt(ctx, openSession, request)
}

func (runningAgent *Agent) runPrompt(ctx context.Context, openSession *session, request acp.PromptRequest) (acp.PromptResponse, error) {
	isResumedFromHostLedger := replayLedger(openSession, request.Meta)
	if !isResumedFromHostLedger {
		openSession.adoptNamedTaskRun(request.Meta, promptText(request.Prompt))
	}
	turnRequest := runningAgent.turnRequestFor(openSession, request, isResumedFromHostLedger)
	stopObserving := openSession.taskEvents.RegisterTurnObserver(func(rawTurnEvent taskstate.RawTurnEvent) {
		openSession.rememberTaskRun(rawTurnEvent.TaskRunID)
		sendLedgerEvent(ctx, runningAgent.sessionUpdates, request.SessionId, rawTurnEvent)
	})
	defer stopObserving()

	turnDecision, errorValue := runningAgent.decisionForTurn(ctx, turnRequest)
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	turnRequest.PrecomputedTurnDecision = &turnDecision
	openSession.catalog.toolSet.UseToolCallGate(newHostCheckedGate(openSession.gate.TurnGate(approval.Turn{
		ResponseLanguage: turnDecision.ResponseLanguage,
		Prompt:           turnRequest.Prompt,
	}), runningAgent.options.HostCheckedToolNames))

	turnResult, errorValue := openSession.kernel.RunTurn(ctx, turnRequest)
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	return promptResponseFor(turnResult), nil
}

func (runningAgent *Agent) turnRequestFor(openSession *session, request acp.PromptRequest, isResumedFromHostLedger bool) agentcontract.AgentTurnRequest {
	turnRequest, _ := turnRequestOfMeta(request.Meta)
	turnRequest.RequesterPersonID = requesterPersonID
	turnRequest.IsRuntimeRestartResume = turnRequest.IsRuntimeRestartResume || isResumedFromHostLedger
	turnRequest.ConversationID = firstNonEmpty(turnRequest.ConversationID, string(request.SessionId))
	turnRequest.ExistingTaskRunID = openSession.currentTaskRunID()
	turnRequest.Prompt = promptText(request.Prompt)
	turnRequest.InputParts = append(partsWithoutImages(turnRequest.InputParts), imagePartsOf(request.Prompt)...)
	turnRequest.AgentIdentity = agentcontract.AgentIdentity{Name: runningAgent.options.AgentName}
	turnRequest.ToolSet = openSession.catalog.toolSet
	turnRequest.PinnedToolNames = openSession.catalog.toolNames
	turnRequest.PinnedSkillNames = runningAgent.options.Skills.PinnedSkillNames
	turnRequest.CarriedOutCalls = carriedOutCallsOfMeta(request.Meta)
	turnRequest.CheckpointSender = checkpointSender(runningAgent.sessionUpdates, request.SessionId)
	return turnRequest
}

func (runningAgent *Agent) decisionForTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (agentcontract.TurnDecision, error) {
	if turnRequest.PrecomputedTurnDecision != nil {
		return *turnRequest.PrecomputedTurnDecision, nil
	}
	return runningAgent.routeTurn(ctx, turnRequest)
}

func failTurnOnPanic(openSession *session, promptResponse *acp.PromptResponse, errorValue *error) {
	recovered := recover()
	if recovered == nil {
		return
	}
	reason := fmt.Sprintf("the bluecollar turn panicked: %v", recovered)
	if taskRunID := openSession.currentTaskRunID(); taskRunID != "" {
		openSession.taskRuns.FailTaskRun(taskRunID, reason)
	}
	*promptResponse = acp.PromptResponse{}
	*errorValue = errors.New(reason)
}

func (runningAgent *Agent) routeTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (agentcontract.TurnDecision, error) {
	router := intake.NewTurnRouter(runningAgent.languageModel, runningAgent.decisionPlanner, agentcontract.IntakeOptions{IsEnabled: true})
	return router.Plan(ctx, agentcontract.AgentRequest{
		RequesterPersonID: turnRequest.RequesterPersonID,
		ConversationID:    turnRequest.ConversationID,
		Prompt:            turnRequest.Prompt,
		InputParts:        turnRequest.InputParts,
		ToolSet:           turnRequest.ToolSet,
	})
}

func (runningAgent *Agent) session(sessionID acp.SessionId) (*session, bool) {
	runningAgent.mutex.Lock()
	defer runningAgent.mutex.Unlock()
	openSession, isKnown := runningAgent.sessionsByID[sessionID]
	return openSession, isKnown
}

func (runningAgent *Agent) CloseSession(_ context.Context, request acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	runningAgent.mutex.Lock()
	defer runningAgent.mutex.Unlock()
	if openSession, isKnown := runningAgent.sessionsByID[request.SessionId]; isKnown {
		openSession.catalog.Close()
		delete(runningAgent.sessionsByID, request.SessionId)
	}
	return acp.CloseSessionResponse{}, nil
}

func stopReasonForStatus(status agentcontract.TaskStatus) acp.StopReason {
	switch status {
	case agentcontract.TaskStatusCancelled:
		return acp.StopReasonCancelled
	case agentcontract.TaskStatusBlocked:
		return acp.StopReasonRefusal
	default:
		return acp.StopReasonEndTurn
	}
}

func imagePartsOf(contentBlocks []acp.ContentBlock) []agentcontract.AgentPart {
	imageParts := []agentcontract.AgentPart{}
	for _, contentBlock := range contentBlocks {
		if image := contentBlock.Image; image != nil {
			imageParts = append(imageParts, agentcontract.AgentPart{
				Type:  agentcontract.AgentPartTypeImage,
				Image: &agentcontract.AgentImagePart{MimeType: image.MimeType, DataBase64: image.Data},
			})
		}
	}
	return imageParts
}

func partsWithoutImages(parts []agentcontract.AgentPart) []agentcontract.AgentPart {
	kept := []agentcontract.AgentPart{}
	for _, part := range parts {
		if part.Type != agentcontract.AgentPartTypeImage {
			kept = append(kept, part)
		}
	}
	return kept
}

func promptText(contentBlocks []acp.ContentBlock) string {
	segments := []string{}
	for _, contentBlock := range contentBlocks {
		if contentBlock.Text != nil {
			segments = append(segments, contentBlock.Text.Text)
		}
	}
	return strings.TrimSpace(strings.Join(segments, "\n"))
}

func (runningAgent *Agent) Cancel(_ context.Context, notification acp.CancelNotification) error {
	openSession, isKnown := runningAgent.session(notification.SessionId)
	if !isKnown {
		return nil
	}
	taskRunID := openSession.currentTaskRunID()
	if taskRunID == "" {
		return nil
	}
	openSession.taskRuns.CancelTaskRunWithReason(taskRunID, requesterPersonID, "the host cancelled this turn")
	return nil
}

func (openSession *session) rememberTaskRun(taskRunID string) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	openSession.taskRunIDMutex.Lock()
	defer openSession.taskRunIDMutex.Unlock()
	openSession.taskRunID = taskRunID
}

func (openSession *session) currentTaskRunID() string {
	openSession.taskRunIDMutex.Lock()
	defer openSession.taskRunIDMutex.Unlock()
	return openSession.taskRunID
}

func (runningAgent *Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (runningAgent *Agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (runningAgent *Agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, errors.New("bluecollar keeps no conversation history of its own; the host owns it")
}

func (runningAgent *Agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, errors.New("bluecollar keeps no session list of its own; the host owns it")
}

func (runningAgent *Agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (runningAgent *Agent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}
