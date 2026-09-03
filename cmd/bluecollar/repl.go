package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ergochat/readline"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const conversationHistoryLimit = 12

type conversationSession struct {
	options        runOptions
	kernel         *loop.AgentKernel
	taskRunService *taskstate.TaskRunService
	languageModel  model.LanguageModelProvider
	closeTape      func()
	runningShell   shell
	workspacePath  string
	history        []agentcontract.VisibleContextMessage
}

func newConversationSession(ctx context.Context, options runOptions) (*conversationSession, error) {
	endpointModel := openaicompatible.NewProvider(options.endpointURL, options.apiKey, options.modelName)
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	kernel := loop.NewAgentKernel(taskRunService, taskstate.NewTaskStepService())

	languageModel, closeTape, tapeError := turnLanguageModel(options, endpointModel)
	if tapeError != nil {
		return nil, tapeError
	}
	kernel.UseLanguageModelProvider(languageModel)
	kernel.UseTurnOptions(agentcontract.TurnOptions{ContextWindowTokens: contextWindowTokens(ctx, options, endpointModel)})

	runningShell := turnShellWithInterpreter(ctx, options)
	return &conversationSession{
		options:        options,
		kernel:         kernel,
		taskRunService: taskRunService,
		languageModel:  languageModel,
		closeTape:      closeTape,
		runningShell:   runningShell,
		workspacePath:  runningShell.resolvedWorkingDirectoryPath(ctx),
	}, nil
}

func (session *conversationSession) runPrompt(ctx context.Context, prompt string) (agentcontract.AgentTurnResult, error) {
	request := agentcontract.AgentTurnRequest{
		RequesterPersonID:    "person-local",
		RequesterName:        currentUserName(),
		ConversationID:       "conversation-local",
		Prompt:               prompt,
		AgentIdentity:        agentcontract.AgentIdentity{Name: session.options.agentName},
		WorkspaceRootPath:    session.workspacePath,
		EnvironmentNow:       session.options.environmentNow,
		WorkspaceDefaultPath: session.workspacePath,
		ToolSet:              turnToolSet(session.options, session.runningShell),
		VisibleContext:       agentcontract.VisibleContext{Messages: session.history},
	}

	turnDecision := decideTurn(ctx, session.languageModel, request, session.options)
	request.PrecomputedTurnDecision = &turnDecision

	taskRun := session.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
	}, prompt)
	request.ExistingTaskRunID = taskRun.TaskRunID
	unregisterLedgerPrinter := session.taskRunService.RegisterTaskRunObserver(taskRun.TaskRunID, printLedgerEvent)
	defer unregisterLedgerPrinter()
	printLedgerEvent(taskstate.RawTurnEvent{TaskRunID: taskRun.TaskRunID, Name: taskstate.TaskEventTaskCreated, Body: prompt})

	result, errorValue := session.kernel.RunTurn(ctx, request)
	writeMetrics(session.options.metricsPath, session.taskRunService, result.TaskRun.TaskRunID)
	writeTrace(session.options.tracePath, session.taskRunService, result)
	session.remember(prompt, result)
	return result, errorValue
}

func (session *conversationSession) remember(prompt string, result agentcontract.AgentTurnResult) {
	now := time.Now()
	session.history = append(session.history,
		agentcontract.VisibleContextMessage{Speaker: currentUserName(), Text: prompt, SentAt: now},
	)
	if reply := strings.TrimSpace(firstNonEmpty(result.FinishMessage, result.UserNotice)); reply != "" {
		session.history = append(session.history,
			agentcontract.VisibleContextMessage{Speaker: session.options.agentName, Text: reply, SentAt: now},
		)
	}
	if len(session.history) > conversationHistoryLimit {
		session.history = session.history[len(session.history)-conversationHistoryLimit:]
	}
}

func runInteractive(options runOptions) error {
	setupContext, cancelSetup := context.WithTimeout(context.Background(), time.Minute)
	session, sessionError := newConversationSession(setupContext, options)
	cancelSetup()
	if sessionError != nil {
		return sessionError
	}
	defer session.closeTape()

	printWelcome(options.modelName, session.workspacePath)
	reader, readerError := readline.NewFromConfig(&readline.Config{
		Prompt: styleBold + "❯ " + styleReset,
	})
	if readerError != nil {
		return readerError
	}
	defer reader.Close()
	for {
		fmt.Fprintln(os.Stderr)
		line, lineError := reader.ReadLine()
		if lineError == readline.ErrInterrupt {
			continue
		}
		if lineError != nil {
			fmt.Fprintln(os.Stderr)
			return nil
		}
		prompt := strings.TrimSpace(line)
		switch prompt {
		case "":
			continue
		case "/exit", "/quit":
			return nil
		}
		turnContext, cancelTurn := context.WithTimeout(context.Background(), options.timeout)
		result, errorValue := session.runPrompt(turnContext, prompt)
		cancelTurn()
		if errorValue != nil {
			fmt.Fprintln(os.Stderr, "bluecollar:", errorValue)
			continue
		}
		printResult(result)
	}
}
