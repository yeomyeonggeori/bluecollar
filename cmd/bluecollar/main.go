package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/bench"
	"github.com/yeomyeonggeori/bluecollar/decisionconfig"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/model/tape"
	"github.com/yeomyeonggeori/bluecollar/trace"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func main() {
	endpointURL := flag.String("endpoint", "", "OpenAI-compatible base URL, 'codex', 'claude', or 'gemini-api'")
	apiKey := flag.String("api-key", "", "bearer token for the endpoint, when it needs one")
	modelName := flag.String("model", "", "model to ask (default: env, saved config, or auto-discovered)")
	loginProvider := flag.String("login", "", "sign in to a provider (codex, claude)")
	agentName := flag.String("agent-name", "the assistant", "what the agent calls itself")
	timeout := flag.Duration("timeout", 5*time.Minute, "how long one turn may run")
	workspacePath := flag.String("workspace", ".", "directory the agent's shell commands run in")
	environmentNow := flag.String("environment-now", "", "the date the environment being worked on keeps, RFC3339 or 2006-01-02, when it is not the machine's")
	withoutTools := flag.Bool("without-tools", false, "answer from reasoning alone, giving the agent no shell")
	execPrefix := flag.String("exec-prefix", "", "run every shell command through this wrapper, such as \"docker exec -i <container>\"")
	metricsPath := flag.String("metrics", "", "write what this turn cost, as JSON, to this path")
	withoutIntake := flag.Bool("without-intake", false, "skip the intake classifier, losing the outcome contract it builds")
	recordTapePath := flag.String("record-tape", "", "write every model request and answer of this turn to this path, so the same turn can be replayed without a model")
	replayTapePath := flag.String("replay-tape", "", "answer every model call from this tape instead of an endpoint; never evidence that the agent works, only that the loop still walks the same way")
	tracePath := flag.String("trace", "", "write the whole run - request, reply, cost and every ledger entry - to this path, as JSON when it ends in .json and Markdown otherwise")
	plainMode := flag.Bool("plain", false, "use the plain line-by-line REPL instead of the full-screen TUI")
	flag.Parse()

	configPath := configFilePath()
	if target := strings.TrimSpace(*loginProvider); target != "" {
		if isAnthropicEndpoint(target) {
			credential := resolveAnthropicCredential(*apiKey)
			if credential == "" {
				fmt.Fprintln(os.Stderr, "bluecollar: to use Claude Extra Usage, run `claude setup-token` and pass it via `export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-...` or `/apikey <token>` (or set `ANTHROPIC_API_KEY`).")
				os.Exit(2)
			}
			_ = writeSavedConfig(configPath, savedConfig{
				EndpointURL: anthropicEndpointName,
				ModelName:   firstNonEmpty(*modelName, defaultAnthropicModelName),
				APIKey:      *apiKey,
			})
			if isAnthropicOAuthToken(credential) {
				fmt.Fprintln(os.Stderr, "Configured Claude/Anthropic via Extra Usage OAuth token (billed per token from claude.ai/settings/usage). Run `bluecollar` to start.")
			} else {
				fmt.Fprintln(os.Stderr, "Configured Claude/Anthropic via API key. Run `bluecollar` to start.")
			}
			return
		}
		if !isCodexEndpoint(target) {
			fmt.Fprintf(os.Stderr, "bluecollar: unsupported login provider %q (supported: codex, claude)\n", target)
			os.Exit(2)
		}
		credentials, loginError := runCodexOAuthLogin(context.Background(), func(authURL string) {
			fmt.Fprintln(os.Stderr, "Opening browser for ChatGPT sign-in:")
			fmt.Fprintln(os.Stderr, authURL)
		})
		if loginError != nil {
			fmt.Fprintln(os.Stderr, "bluecollar: login failed:", loginError)
			os.Exit(1)
		}
		_ = writeSavedConfig(configPath, savedConfig{
			EndpointURL: codexEndpointName,
			ModelName:   firstNonEmpty(*modelName, defaultCodexModelName),
		})
		fmt.Fprintf(os.Stderr, "Signed in to ChatGPT/Codex (account %s). Run `bluecollar` to start.\n", firstNonEmpty(credentials.AccountID, "ready"))
		return
	}

	saved := loadSavedConfig(configPath)
	resolvedEndpoint, resolvedAPIKey, resolvedModel := resolveConnectionDefaults(*endpointURL, *apiKey, *modelName, saved, os.Getenv)
	if resolvedModel == "" && strings.TrimSpace(*replayTapePath) != "" {
		resolvedModel = "tape"
	}
	if resolvedModel == "" && strings.TrimSpace(*replayTapePath) == "" {
		discoverContext, cancelDiscover := context.WithTimeout(context.Background(), modelDiscoveryTimeout)
		resolvedModel = autoSelectDiscoveredModel(discoverContext, resolvedEndpoint, resolvedAPIKey)
		cancelDiscover()
	}

	prompt := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if prompt == "" && stdinIsPiped(os.Stdin) {
		prompt = readPipedPrompt(os.Stdin)
	}
	options := runOptions{
		endpointURL:    resolvedEndpoint,
		apiKey:         resolvedAPIKey,
		modelName:      resolvedModel,
		configPath:     configPath,
		agentName:      *agentName,
		prompt:         prompt,
		timeout:        *timeout,
		workspacePath:  *workspacePath,
		environmentNow: parseEnvironmentNow(*environmentNow),
		withoutTools:   *withoutTools,
		execPrefix:     *execPrefix,
		metricsPath:    *metricsPath,
		withoutIntake:  *withoutIntake,
		tracePath:      *tracePath,
		recordTapePath: *recordTapePath,
		replayTapePath: *replayTapePath,
		plainMode:      *plainMode,
	}

	if prompt == "" {
		isInteractiveTerminal := isTerminalFile(os.Stdin) || stderrWantsStyle()
		if !isInteractiveTerminal {
			fmt.Fprintln(os.Stderr, "usage: bluecollar [flags] <what you want done>")
			os.Exit(2)
		}
		options.isConversation = true
		if !options.plainMode && isTerminalFile(os.Stdin) && isTerminalFile(os.Stdout) {
			options.isTUI = true
			if errorValue := runTUI(options); errorValue != nil {
				fmt.Fprintln(os.Stderr, "bluecollar:", errorValue)
				os.Exit(1)
			}
			return
		}
		if errorValue := runInteractive(options); errorValue != nil {
			fmt.Fprintln(os.Stderr, "bluecollar:", errorValue)
			os.Exit(1)
		}
		return
	}

	if strings.TrimSpace(options.modelName) == "" {
		fmt.Fprintln(os.Stderr, "bluecollar: no model configured; pass --model, set BLUECOLLAR_MODEL, or run bluecollar to choose one")
		os.Exit(2)
	}

	options.prompt = prompt
	result, errorValue := runOneTurn(options)
	if errorValue != nil {
		fmt.Fprintln(os.Stderr, "bluecollar:", errorValue)
		os.Exit(1)
	}
	printResult(result)
}

type runOptions struct {
	endpointURL    string
	apiKey         string
	modelName      string
	configPath     string
	agentName      string
	prompt         string
	timeout        time.Duration
	workspacePath  string
	environmentNow time.Time
	withoutTools   bool
	execPrefix     string
	metricsPath    string
	withoutIntake  bool
	isConversation bool
	tracePath      string
	recordTapePath string
	replayTapePath string
	plainMode      bool
	isTUI          bool
}

func runOneTurn(options runOptions) (agentcontract.AgentTurnResult, error) {
	turnContext, cancel := context.WithTimeout(context.Background(), options.timeout)
	defer cancel()
	session, sessionError := newConversationSession(turnContext, options)
	if sessionError != nil {
		return agentcontract.AgentTurnResult{}, sessionError
	}
	defer session.closeTape()
	return session.runPrompt(turnContext, options.prompt)
}

func printLedgerEvent(rawTurnEvent taskstate.RawTurnEvent) {
	taskEvent := agentcontract.TaskEvent{Name: rawTurnEvent.Name, Body: rawTurnEvent.Body}
	if !stderrWantsStyle() {
		fmt.Fprintf(os.Stderr, "  %s  %s\n", taskEvent.Name, collapsedWhitespace(taskEvent.Body))
		return
	}
	if line := prettyLedgerLine(taskEvent); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
}

func collapsedWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func routeTurn(ctx context.Context, languageModel model.LanguageModelProvider, request agentcontract.AgentTurnRequest, diagnosticOutput io.Writer) (turnclassification.TurnDecision, error) {
	router := intake.NewTurnRouter(languageModel, intake.NewDecisionPlanner(decisionconfig.ConfiguredDecisionModel(diagnosticOutput), nil), agentcontract.IntakeOptions{IsEnabled: true})
	return router.Plan(ctx, agentcontract.AgentRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{
			RequesterPersonID: request.RequesterPersonID,
			RequesterName:     request.RequesterName,
		},
		ConversationID:    request.ConversationID,
		Prompt:            request.Prompt,
		WorkspaceRootPath: request.WorkspaceRootPath,
		ToolSet:           request.ToolSet,
	})
}

func printResult(result agentcontract.AgentTurnResult) {
	fmt.Println(strings.TrimSpace(firstNonEmpty(result.FinishMessage, result.UserNotice, "(no reply)")))
	fmt.Fprintf(os.Stderr, "\nstatus: %s\n", styledStatus(result.TaskRun.Status))
	if reason := strings.TrimSpace(result.TaskRun.FailureReason); reason != "" {
		fmt.Fprintln(os.Stderr, "reason:", reason)
	}
}

func currentUserName() string {
	if name := strings.TrimSpace(os.Getenv("USER")); name != "" {
		return name
	}
	return "local"
}

func envOrDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseEnvironmentNow(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, errorValue := time.Parse(layout, trimmed); errorValue == nil {
			return parsed
		}
	}
	return time.Time{}
}

func turnShell(options runOptions) shell {
	return shell{
		workingDirectoryPath: options.workspacePath,
		commandPrefix:        strings.Fields(options.execPrefix),
	}
}

func turnShellWithInterpreter(ctx context.Context, options runOptions) shell {
	return turnShell(options).withInterpreterFound(ctx)
}

func turnToolSet(options runOptions, runningShell shell, toolSelector agentcontract.ToolSelector) *toolcontract.ToolSet {
	if options.withoutTools {
		return nil
	}
	return newWorkspaceToolSet(runningShell, toolSelector)
}

func writeTrace(tracePath string, taskRunService *taskstate.TaskRunService, result agentcontract.AgentTurnResult) {
	writeTraceTo(tracePath, taskRunService, result, os.Stderr)
}

func writeTraceTo(tracePath string, taskRunService *taskstate.TaskRunService, result agentcontract.AgentTurnResult, output io.Writer) {
	if strings.TrimSpace(tracePath) == "" {
		return
	}
	bundle := trace.Build(result.TaskRun, taskRunService.ListTaskEvent(result.TaskRun.TaskRunID),
		firstNonEmpty(result.FinishMessage, result.UserNotice))
	document, errorValue := renderTrace(tracePath, bundle)
	if errorValue != nil {
		fmt.Fprintln(output, "bluecollar: could not render the trace:", errorValue)
		return
	}
	if writeError := os.WriteFile(tracePath, document, 0o600); writeError != nil {
		fmt.Fprintln(output, "bluecollar: could not write the trace:", writeError)
		return
	}
	fmt.Fprintln(output, "trace:", tracePath)
	fmt.Fprintln(output, trace.PrivacyNotice)
}

func renderTrace(tracePath string, bundle trace.Bundle) ([]byte, error) {
	if strings.HasSuffix(strings.ToLower(tracePath), ".json") {
		return bundle.JSON()
	}
	return []byte(bundle.Markdown()), nil
}

func writeMetrics(metricsPath string, taskRunService *taskstate.TaskRunService, taskRunID string) {
	if strings.TrimSpace(metricsPath) == "" {
		return
	}
	metrics := bench.MeasureTaskRun(taskRunID, taskRunService.ListTaskEvent(taskRunID))
	document, errorValue := json.MarshalIndent(metrics, "", "  ")
	if errorValue != nil {
		fmt.Fprintln(os.Stderr, "bluecollar: could not measure the turn:", errorValue)
		return
	}
	if writeError := os.WriteFile(metricsPath, document, 0o644); writeError != nil {
		fmt.Fprintln(os.Stderr, "bluecollar: could not write the measurements:", writeError)
	}
}

type contextWindowProvider interface {
	ContextWindowTokens(context.Context) int
}

func turnLanguageModel(options runOptions, endpointModel model.LanguageModelProvider) (model.LanguageModelProvider, func(), error) {
	if replayPath := strings.TrimSpace(options.replayTapePath); replayPath != "" {
		file, errorValue := os.Open(replayPath)
		if errorValue != nil {
			return nil, func() {}, errorValue
		}
		defer file.Close()
		player, errorValue := tape.Read(file)
		if errorValue != nil {
			return nil, func() {}, errorValue
		}
		return player, func() {}, nil
	}
	recordPath := strings.TrimSpace(options.recordTapePath)
	if recordPath == "" {
		return endpointModel, func() {}, nil
	}
	file, errorValue := os.OpenFile(recordPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if errorValue != nil {
		return nil, func() {}, errorValue
	}
	return tape.NewRecorder(endpointModel, file), func() { file.Close() }, nil
}

func contextWindowTokens(ctx context.Context, options runOptions, endpointModel model.LanguageModelProvider) int {
	if strings.TrimSpace(options.replayTapePath) != "" {
		return 0
	}
	if provider, hasWindow := endpointModel.(contextWindowProvider); hasWindow {
		return provider.ContextWindowTokens(ctx)
	}
	return 0
}

func decideTurn(ctx context.Context, languageModel model.LanguageModelProvider, request agentcontract.AgentTurnRequest, options runOptions) turnclassification.TurnDecision {
	if options.withoutIntake {
		return boundedTaskDecision()
	}
	diagnosticOutput := io.Writer(os.Stderr)
	if options.isTUI {
		diagnosticOutput = io.Discard
	}
	turnDecision, routingError := routeTurn(ctx, languageModel, request, diagnosticOutput)
	if routingError != nil {
		fmt.Fprintln(diagnosticOutput, "bluecollar: classifying the request failed, doing it anyway:", routingError)
		return boundedTaskDecision()
	}
	if options.isConversation {
		return turnDecision
	}
	return startingTheTaskItWasGiven(turnDecision)
}

func startingTheTaskItWasGiven(turnDecision turnclassification.TurnDecision) turnclassification.TurnDecision {
	if turnDecision.Route == agentcontract.TurnRouteStartTask || turnDecision.Route == agentcontract.TurnRouteContinueTask {
		return turnDecision
	}
	fmt.Fprintf(os.Stderr, "bluecollar: the classifier answered %q; the runner was given a task, so it starts one\n", turnDecision.Route)
	turnDecision.Route = agentcontract.TurnRouteStartTask
	turnDecision.Classification = agentcontract.IntakeClassificationBoundedTask
	if turnDecision.TaskShape == agentcontract.TaskShapeImmediateReply || turnDecision.TaskShape == "" {
		turnDecision.TaskShape = agentcontract.TaskShapeMaintenanceTask
	}
	return turnDecision
}

func boundedTaskDecision() turnclassification.TurnDecision {
	return turnclassification.TurnDecision{
		Route:          agentcontract.TurnRouteStartTask,
		Classification: agentcontract.IntakeClassificationBoundedTask,
		TaskShape:      agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:      agentcontract.TaskLevelLow,
		Reason:         "the runner was given one thing to do",
	}
}

func stdinIsPiped(file *os.File) bool {
	if file == nil {
		return false
	}
	info, errorValue := file.Stat()
	if errorValue != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func readPipedPrompt(reader io.Reader) string {
	data, errorValue := io.ReadAll(reader)
	if errorValue != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
