package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
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
}

type agent struct {
	languageModel    model.LanguageModelProvider
	agentName        string
	resolveTransport transportResolver
	sessionUpdates   sessionUpdateSender

	mutex             sync.Mutex
	sessionsByID      map[acp.SessionId]*session
	nextSessionNumber int
}

func newAgent(languageModel model.LanguageModelProvider, agentName string) *agent {
	return &agent{
		languageModel:    languageModel,
		agentName:        agentName,
		resolveTransport: transportForServer,
		sessionsByID:     map[acp.SessionId]*session{},
	}
}

func (runningAgent *agent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			McpCapabilities: acp.McpCapabilities{Http: true},
		},
	}, nil
}

func (runningAgent *agent) NewSession(ctx context.Context, request acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	openedCatalog, errorValue := openCatalog(ctx, request.McpServers, runningAgent.resolveTransport)
	if errorValue != nil {
		return acp.NewSessionResponse{}, errorValue
	}
	taskEvents := taskstate.NewTaskEventService()
	taskRuns := taskstate.NewTaskRunService(taskEvents)
	kernel := loop.NewAgentKernel(taskRuns, taskstate.NewTaskStepService())
	kernel.UseLanguageModelProvider(runningAgent.languageModel)

	runningAgent.mutex.Lock()
	defer runningAgent.mutex.Unlock()
	runningAgent.nextSessionNumber++
	sessionID := acp.SessionId("bluecollar-" + strconv.Itoa(runningAgent.nextSessionNumber))
	runningAgent.sessionsByID[sessionID] = &session{
		catalog:    openedCatalog,
		kernel:     kernel,
		taskRuns:   taskRuns,
		taskEvents: taskEvents,
	}
	return acp.NewSessionResponse{SessionId: sessionID}, nil
}

func (runningAgent *agent) Prompt(ctx context.Context, request acp.PromptRequest) (acp.PromptResponse, error) {
	openSession, isKnown := runningAgent.session(request.SessionId)
	if !isKnown {
		return acp.PromptResponse{}, errors.New("bluecollar has no session by that id; open one with session/new first")
	}
	isResumedFromHostLedger := replayLedger(openSession, request.Meta)
	turnRequest := agentcontract.AgentTurnRequest{
		RequesterPersonID:      requesterPersonID,
		IsRuntimeRestartResume: isResumedFromHostLedger,
		ConversationID:         string(request.SessionId),
		ExistingTaskRunID:      openSession.currentTaskRunID(),
		Prompt:                 promptText(request.Prompt),
		AgentIdentity:          agentcontract.AgentIdentity{Name: runningAgent.agentName},
		ToolSet:                openSession.catalog.toolSet,
		PinnedToolNames:        openSession.catalog.toolNames,
		CarriedOutCalls:        carriedOutCallsOfMeta(request.Meta),
	}
	stopObserving := openSession.taskEvents.RegisterTurnObserver(func(rawTurnEvent taskstate.RawTurnEvent) {
		openSession.rememberTaskRun(rawTurnEvent.TaskRunID)
		sendLedgerEvent(ctx, runningAgent.sessionUpdates, request.SessionId, rawTurnEvent)
	})
	defer stopObserving()

	turnDecision, errorValue := runningAgent.routeTurn(ctx, turnRequest)
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	turnRequest.PrecomputedTurnDecision = &turnDecision

	turnResult, errorValue := openSession.kernel.RunTurn(ctx, turnRequest)
	if errorValue != nil {
		return acp.PromptResponse{}, errorValue
	}
	return acp.PromptResponse{StopReason: stopReasonForStatus(turnResult.TaskRun.Status)}, nil
}

func (runningAgent *agent) routeTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (agentcontract.TurnDecision, error) {
	router := intake.NewTurnRouter(runningAgent.languageModel, agentcontract.IntakeOptions{IsEnabled: true})
	return router.Plan(ctx, agentcontract.AgentRequest{
		RequesterPersonID: turnRequest.RequesterPersonID,
		ConversationID:    turnRequest.ConversationID,
		Prompt:            turnRequest.Prompt,
		ToolSet:           turnRequest.ToolSet,
	})
}

func (runningAgent *agent) session(sessionID acp.SessionId) (*session, bool) {
	runningAgent.mutex.Lock()
	defer runningAgent.mutex.Unlock()
	openSession, isKnown := runningAgent.sessionsByID[sessionID]
	return openSession, isKnown
}

func (runningAgent *agent) CloseSession(_ context.Context, request acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
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

func promptText(contentBlocks []acp.ContentBlock) string {
	segments := []string{}
	for _, contentBlock := range contentBlocks {
		if contentBlock.Text != nil {
			segments = append(segments, contentBlock.Text.Text)
		}
	}
	return strings.TrimSpace(strings.Join(segments, "\n"))
}

func (runningAgent *agent) Cancel(_ context.Context, notification acp.CancelNotification) error {
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

func (runningAgent *agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (runningAgent *agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (runningAgent *agent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, errors.New("bluecollar keeps no conversation history of its own; the host owns it")
}

func (runningAgent *agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, errors.New("bluecollar keeps no session list of its own; the host owns it")
}

func (runningAgent *agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (runningAgent *agent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}
