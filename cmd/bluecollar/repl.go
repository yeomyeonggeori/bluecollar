package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/ergochat/readline"
	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/bluecollar/attribution"
	"github.com/yeomyeonggeori/bluecollar/bench"
	"github.com/yeomyeonggeori/bluecollar/decisionconfig"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/bluecollar/turnoptions"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

const conversationHistoryLimit = 12

type conversationSession struct {
	options               runOptions
	toolSelector          agentcontract.ToolSelector
	kernel                *loop.AgentKernel
	taskRunService        *taskstate.TaskRunService
	languageModel         model.LanguageModelProvider
	closeTape             func()
	runningShell          shell
	workspacePath         string
	instructions          workspaceInstructions
	history               []agentcontract.VisibleContextMessage
	asker                 approval.Asker
	turnCount             int
	totalPromptTokens     int64
	totalCompletionTokens int64
	lastResult            agentcontract.AgentTurnResult
	hasLastResult         bool
	onLedgerEvent         func(taskstate.RawTurnEvent)
	activeTaskRunMutex    sync.Mutex
	activeTaskRunID       string
}

func buildEndpointProvider(options runOptions) model.LanguageModelProvider {
	if isGeminiCLIEndpoint(options.endpointURL) {
		return newGeminiCLIProvider(options.modelName, options.apiKey)
	}
	if isCodexEndpoint(options.endpointURL) {
		return newCodexProvider(options.modelName)
	}
	if isAnthropicEndpoint(options.endpointURL) {
		return newAnthropicProvider(options.modelName, options.apiKey)
	}
	return openaicompatible.NewProvider(options.endpointURL, options.apiKey, options.modelName).WithAttribution(attribution.Self)
}

func newConversationSession(ctx context.Context, options runOptions) (*conversationSession, error) {
	endpointModel := buildEndpointProvider(options)
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	kernel := loop.NewAgentKernel(taskRunService, taskstate.NewTaskStepService())

	languageModel, closeTape, tapeError := turnLanguageModel(options, endpointModel)
	if tapeError != nil {
		return nil, tapeError
	}
	kernel.UseLanguageModelProvider(languageModel)
	diagnosticOutput := io.Writer(os.Stderr)
	if options.isTUI {
		diagnosticOutput = io.Discard
	}
	kernel.UseDecisionModel(decisionconfig.ConfiguredDecisionModel(diagnosticOutput))
	toolSelector := configuredToolSelector(diagnosticOutput)
	if toolSelector != nil {
		kernel.UseToolSelector(toolSelector)
	}
	if options.modelName != "" {
		kernel.UseTurnOptions(turnoptions.TurnOptions{ContextWindowTokens: contextWindowTokens(ctx, options, endpointModel)})
	}

	runningShell := turnShellWithInterpreter(ctx, options)
	kernel.UseToolResultSpillStore(shellSpillStore{runningShell: runningShell})
	resolvedWorkspacePath := runningShell.resolvedWorkingDirectoryPath(ctx)
	instructions := workspaceInstructions{}
	if strings.TrimSpace(options.execPrefix) == "" {
		instructions = loadWorkspaceInstructions(resolvedWorkspacePath)
	}
	workspaceGuidanceBlock := strings.Join([]string{
		"Workspace execution environment:",
		"- Current workspace directory: " + resolvedWorkspacePath,
		"- All file tools (file_read, write, edit) and bash run directly in " + resolvedWorkspacePath + ".",
		"- Prefer relative paths inside this workspace (such as hello.c or src/main.c). Never invent virtual sandbox paths like /mnt/data or /workspace.",
	}, "\n")
	combinedPrompt := strings.TrimSpace(strings.Join(withoutEmptyStrings([]string{workspaceGuidanceBlock, instructions.prompt}), "\n\n"))
	kernel.UseInstructionBundleLoader(func() loop.InstructionBundle {
		return loop.InstructionBundle{
			Prompt:  combinedPrompt,
			Skills:  append([]agentcontract.SkillInstruction{}, instructions.skills...),
			Sources: append([]agentcontract.InstructionSource{}, instructions.sources...),
		}
	})
	return &conversationSession{
		options:        options,
		toolSelector:   toolSelector,
		kernel:         kernel,
		taskRunService: taskRunService,
		languageModel:  languageModel,
		closeTape:      closeTape,
		runningShell:   runningShell,
		workspacePath:  resolvedWorkspacePath,
		instructions:   instructions,
	}, nil
}

func (session *conversationSession) switchModel(ctx context.Context, modelName string) {
	trimmed := strings.TrimSpace(modelName)
	if trimmed == "" {
		return
	}
	session.options.modelName = trimmed
	session.refreshLanguageModel(ctx)
	session.persistConfig()
}

func (session *conversationSession) switchEndpoint(ctx context.Context, endpointURL string) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(endpointURL), "/")
	if trimmed == "" {
		return
	}
	if isGeminiCLIEndpoint(trimmed) {
		trimmed = geminiCLIEndpointName
		lowerModel := strings.ToLower(strings.TrimSpace(session.options.modelName))
		if lowerModel == "" || strings.HasPrefix(lowerModel, "gpt-") || strings.HasPrefix(lowerModel, "claude-") {
			session.options.modelName = defaultGeminiCLIModelName
		}
	} else if isCodexEndpoint(trimmed) {
		trimmed = codexEndpointName
		lowerModel := strings.ToLower(strings.TrimSpace(session.options.modelName))
		if lowerModel == "" || strings.HasPrefix(lowerModel, "gemini-") || strings.HasPrefix(lowerModel, "claude-") || lowerModel == "gpt-5.4" {
			session.options.modelName = defaultCodexModelName
		}
	} else if isAnthropicEndpoint(trimmed) {
		trimmed = anthropicEndpointName
		lowerModel := strings.ToLower(strings.TrimSpace(session.options.modelName))
		if lowerModel == "" || strings.HasPrefix(lowerModel, "gemini-") || strings.HasPrefix(lowerModel, "gpt-") {
			session.options.modelName = defaultAnthropicModelName
		}
	}
	session.options.endpointURL = trimmed
	session.refreshLanguageModel(ctx)
	session.persistConfig()
}

func (session *conversationSession) setAPIKey(ctx context.Context, apiKey string) {
	session.options.apiKey = strings.TrimSpace(apiKey)
	session.refreshLanguageModel(ctx)
	session.persistConfig()
}

func (session *conversationSession) refreshLanguageModel(ctx context.Context) {
	endpointModel := buildEndpointProvider(session.options)
	session.languageModel = endpointModel
	if session.kernel != nil {
		session.kernel.UseLanguageModelProvider(endpointModel)
		if session.options.modelName != "" {
			session.kernel.UseTurnOptions(turnoptions.TurnOptions{ContextWindowTokens: contextWindowTokens(ctx, session.options, endpointModel)})
		}
	}
}

func (session *conversationSession) persistConfig() {
	if strings.TrimSpace(session.options.configPath) == "" {
		return
	}
	_ = writeSavedConfig(session.options.configPath, savedConfig{
		EndpointURL: session.options.endpointURL,
		ModelName:   session.options.modelName,
		APIKey:      session.options.apiKey,
	})
}

func (session *conversationSession) runPrompt(ctx context.Context, prompt string) (agentcontract.AgentTurnResult, error) {
	request := agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{
			RequesterPersonID: "person-local",
			RequesterName:     currentUserName(),
		},
		ConversationID:       "conversation-local",
		Prompt:               prompt,
		AgentIdentity:        agentcontract.AgentIdentity{Name: session.options.agentName},
		WorkspaceRootPath:    session.workspacePath,
		EnvironmentNow:       session.options.environmentNow,
		WorkspaceDefaultPath: session.workspacePath,
		WorkspaceGuidance: []string{
			"All file tools (file_read, write, edit) and bash run directly in " + session.workspacePath + ".",
			"Prefer relative paths inside this workspace (such as hello.c or src/main.c). Never invent virtual sandbox paths like /mnt/data or /workspace.",
		},
		InstructionPrompt:  session.instructions.prompt,
		AvailableSkills:    session.instructions.skills,
		InstructionSources: session.instructions.sources,
		ToolSet:            turnToolSet(session.options, session.runningShell, session.toolSelector),
		VisibleContext:     agentcontract.VisibleContext{Messages: session.history},
	}

	turnDecision := decideTurn(ctx, session.languageModel, request, session.options)
	if request.ToolSet != nil {
		request.ToolSet.UseToolCallGate(approval.New(session.taskRunService, session.languageModel, session.asker).TurnGate(approval.Turn{
			ResponseLanguage: turnDecision.ResponseLanguage,
			Prompt:           prompt,
		}))
	}

	taskRun := session.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
	}, prompt)
	request.ExistingTaskRunID = taskRun.TaskRunID
	session.setActiveTaskRunID(taskRun.TaskRunID)
	defer session.setActiveTaskRunID("")
	observer := session.ledgerObserver()
	unregisterLedgerPrinter := session.taskRunService.RegisterTaskRunObserver(taskRun.TaskRunID, observer)
	defer unregisterLedgerPrinter()
	observer(taskstate.RawTurnEvent{TaskRunID: taskRun.TaskRunID, Name: agentcontract.TaskEventTaskCreated, Body: prompt})

	result, errorValue := session.kernel.RunPlannedTurn(ctx, request, turnclassification.Routing{Decision: &turnDecision})
	session.recordTurnStats(result)
	writeMetrics(session.options.metricsPath, session.taskRunService, result.TaskRun.TaskRunID)
	traceOutput := io.Writer(os.Stderr)
	if session.options.isTUI {
		traceOutput = io.Discard
	}
	writeTraceTo(session.options.tracePath, session.taskRunService, result, traceOutput)
	session.remember(prompt, result)
	return result, errorValue
}

func (session *conversationSession) ledgerObserver() func(taskstate.RawTurnEvent) {
	if session.onLedgerEvent != nil {
		return session.onLedgerEvent
	}
	return printLedgerEvent
}

func (session *conversationSession) setActiveTaskRunID(taskRunID string) {
	session.activeTaskRunMutex.Lock()
	defer session.activeTaskRunMutex.Unlock()
	session.activeTaskRunID = taskRunID
}

func (session *conversationSession) steerActiveTurn(instruction string) bool {
	session.activeTaskRunMutex.Lock()
	taskRunID := session.activeTaskRunID
	session.activeTaskRunMutex.Unlock()
	if taskRunID == "" || session.taskRunService == nil {
		return false
	}
	body, errorValue := json.Marshal(map[string]string{
		"instruction": strings.TrimSpace(instruction),
	})
	if errorValue != nil {
		return false
	}
	session.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAgentSteerReceived, string(body))
	return true
}

func (session *conversationSession) recordTurnStats(result agentcontract.AgentTurnResult) {
	session.turnCount++
	session.lastResult = result
	session.hasLastResult = result.TaskRun.TaskRunID != ""
	if session.taskRunService == nil || result.TaskRun.TaskRunID == "" {
		return
	}
	metrics := bench.MeasureTaskRun(result.TaskRun.TaskRunID, session.taskRunService.ListTaskEvent(result.TaskRun.TaskRunID))
	session.totalPromptTokens += metrics.PromptTokens
	session.totalCompletionTokens += metrics.CompletionTokens
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

func handleReplCommand(ctx context.Context, session *conversationSession, line string, output io.Writer) (bool, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return false, false
	}
	switch fields[0] {
	case "/exit", "/quit":
		return true, true
	case "/help":
		fmt.Fprintln(output, "commands: /login [codex|claude], /models, /model [name], /endpoint [url|codex|claude|gemini-api], /apikey <key>, /clear, /status, /trace <path>, /help, /exit")
		return true, false
	case "/login":
		forceLogin := false
		targetProvider := codexEndpointName
		for _, field := range fields[1:] {
			if field == "--force" {
				forceLogin = true
				continue
			}
			targetProvider = field
		}
		if isAnthropicEndpoint(targetProvider) {
			credential := resolveAnthropicCredential(session.options.apiKey)
			if credential == "" {
				fmt.Fprintln(output, "to use Claude Extra Usage: run `claude setup-token` in a terminal, then run `/apikey sk-ant-oat01-...` or export CLAUDE_CODE_OAUTH_TOKEN (or set ANTHROPIC_API_KEY).")
				return true, false
			}
			session.switchEndpoint(ctx, anthropicEndpointName)
			if isAnthropicOAuthToken(credential) {
				fmt.Fprintf(output, "switched to %s (%s) using Extra Usage OAuth token (billed per token from claude.ai/settings/usage)\n", session.options.endpointURL, session.options.modelName)
			} else {
				fmt.Fprintf(output, "switched to %s (%s) using Anthropic API key\n", session.options.endpointURL, session.options.modelName)
			}
			return true, false
		}
		if !forceLogin {
			if existing, errorValue := loadCodexCredentials(); errorValue == nil {
				session.switchEndpoint(ctx, codexEndpointName)
				fmt.Fprintf(output, "already signed in to ChatGPT/Codex (account %s); switched to %s (%s). Use /login codex --force to re-authenticate.\n",
					firstNonEmpty(existing.AccountID, "ready"), session.options.endpointURL, session.options.modelName)
				return true, false
			}
		}
		credentials, loginError := runCodexOAuthLogin(ctx, func(authURL string) {
			fmt.Fprintln(output, "Open this URL to sign in with ChatGPT:\n"+authURL)
		})
		if loginError != nil {
			fmt.Fprintf(output, "login failed: %v\n", loginError)
			return true, false
		}
		session.switchEndpoint(ctx, codexEndpointName)
		fmt.Fprintf(output, "signed in to ChatGPT/Codex (account %s); switched to %s (%s)\n",
			firstNonEmpty(credentials.AccountID, "ready"), session.options.endpointURL, session.options.modelName)
		return true, false
	case "/clear", "/new":
		session.history = nil
		fmt.Fprintln(output, "conversation history cleared")
		return true, false
	case "/models":
		models, errorValue := discoverEndpointModels(ctx, session.options.endpointURL, session.options.apiKey)
		if errorValue != nil {
			fmt.Fprintf(output, "could not list models on %s: %v\n", session.options.endpointURL, errorValue)
			return true, false
		}
		fmt.Fprintln(output, formatModelListForDisplay(session.options.endpointURL, models))
		return true, false
	case "/model":
		if len(fields) < 2 {
			fmt.Fprintf(output, "model: %s\n", firstNonEmpty(session.options.modelName, "(not set)"))
			return true, false
		}
		nextModel := strings.TrimSpace(strings.TrimPrefix(line, "/model"))
		session.switchModel(ctx, nextModel)
		fmt.Fprintf(output, "model switched to %s\n", session.options.modelName)
		return true, false
	case "/endpoint":
		if len(fields) < 2 {
			fmt.Fprintf(output, "endpoint: %s\n", session.options.endpointURL)
			return true, false
		}
		nextEndpoint := strings.TrimSpace(strings.TrimPrefix(line, "/endpoint"))
		session.switchEndpoint(ctx, nextEndpoint)
		fmt.Fprintf(output, "endpoint switched to %s\n", session.options.endpointURL)
		return true, false
	case "/apikey":
		if len(fields) < 2 {
			if session.options.apiKey == "" {
				fmt.Fprintln(output, "api key: (not set)")
			} else {
				fmt.Fprintln(output, "api key: (configured)")
			}
			return true, false
		}
		session.setAPIKey(ctx, strings.TrimSpace(strings.TrimPrefix(line, "/apikey")))
		fmt.Fprintln(output, "api key updated")
		return true, false
	case "/status":
		fmt.Fprintf(output, "model: %s\nendpoint: %s\nworkspace: %s\nturns: %d\ntokens: %d prompt, %d completion\nskills: %d\n",
			firstNonEmpty(session.options.modelName, "(not set)"), session.options.endpointURL, session.workspacePath, session.turnCount,
			session.totalPromptTokens, session.totalCompletionTokens, len(session.instructions.skills))
		return true, false
	case "/trace":
		if len(fields) < 2 {
			fmt.Fprintln(output, "usage: /trace <path>")
			return true, false
		}
		if !session.hasLastResult {
			fmt.Fprintln(output, "no completed turn to trace yet")
			return true, false
		}
		writeTraceTo(strings.TrimSpace(strings.TrimPrefix(line, "/trace")), session.taskRunService, session.lastResult, output)
		return true, false
	default:
		return false, false
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
	session.asker = terminalAsker{output: os.Stderr, readLine: func(prompt string) (string, error) { return readPrompted(reader, prompt) }}
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
		if prompt == "" {
			continue
		}
		commandContext, cancelCommand := context.WithTimeout(context.Background(), 15*time.Second)
		handled, shouldExit := handleReplCommand(commandContext, session, prompt, os.Stderr)
		cancelCommand()
		if shouldExit {
			return nil
		}
		if handled {
			continue
		}
		if strings.TrimSpace(session.options.modelName) == "" {
			fmt.Fprintln(os.Stderr, "bluecollar: no model selected; run /models to list models or /model <name> to select one")
			continue
		}
		turnContext, cancelTurn := context.WithTimeout(context.Background(), options.timeout)
		interruptContext, stopInterrupt := signal.NotifyContext(turnContext, os.Interrupt)
		result, errorValue := session.runPrompt(interruptContext, prompt)
		stopInterrupt()
		cancelTurn()
		if errorValue != nil {
			fmt.Fprintln(os.Stderr, "bluecollar:", errorValue)
			continue
		}
		printResult(result)
	}
}

func readPrompted(reader *readline.Instance, prompt string) (string, error) {
	reader.SetPrompt(prompt)
	defer reader.SetPrompt(styleBold + "❯ " + styleReset)
	line, lineError := reader.ReadLine()
	return strings.TrimSpace(line), lineError
}
