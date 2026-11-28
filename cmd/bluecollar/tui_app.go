package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type tuiApprovalPrompt struct {
	hold    holdrecord.Hold
	replyTo chan approval.Answer
}

type tuiTurnOutcome struct {
	result agentcontract.AgentTurnResult
	err    error
}

type tuiApprovalAsker struct {
	prompts chan<- tuiApprovalPrompt
}

func (asker tuiApprovalAsker) Ask(ctx context.Context, hold holdrecord.Hold) approval.Answer {
	replyChannel := make(chan approval.Answer, 1)
	select {
	case asker.prompts <- tuiApprovalPrompt{hold: hold, replyTo: replyChannel}:
	case <-ctx.Done():
		return approval.NoAnswer
	}
	select {
	case answer := <-replyChannel:
		return answer
	case <-ctx.Done():
		return approval.NoAnswer
	}
}

type tuiLoginOutcome struct {
	credentials codexCredentials
	err         error
}

type tuiApp struct {
	session          *conversationSession
	output           io.Writer
	width            int
	height           int
	editor           *composerEditor
	items            []tuiTranscriptItem
	activeTurn       *tuiTurnEntry
	cancelActiveTurn context.CancelFunc
	queuedPrompts    []string
	pendingApproval  *tuiApprovalPrompt
	showFullLedger   bool
	scrollOffset     int
	maxScrollOffset  int
	spinnerIndex     int
	statusFlash      string
	lastCtrlCAt      time.Time
	ledgerEvents     chan taskstate.RawTurnEvent
	turnOutcomes     chan tuiTurnOutcome
	approvalPrompts  chan tuiApprovalPrompt
	loginURLs        chan string
	loginOutcomes    chan tuiLoginOutcome
	cancelLogin      context.CancelFunc
	nextTurnNumber   int
}

func runTUI(options runOptions) error {
	stdinFD := int(os.Stdin.Fd())
	stdoutFD := int(os.Stdout.Fd())
	savedState, rawError := enterRawTerminal(stdinFD)
	if rawError != nil {
		return runInteractive(options)
	}
	defer restoreTerminal(stdinFD, savedState)

	setupContext, cancelSetup := context.WithTimeout(context.Background(), time.Minute)
	session, sessionError := newConversationSession(setupContext, options)
	cancelSetup()
	if sessionError != nil {
		return sessionError
	}
	defer session.closeTape()

	width, height := terminalWindowSize(stdoutFD)
	app := newTUIApp(session, os.Stdout, width, height)
	session.onLedgerEvent = app.enqueueLedgerEvent
	session.asker = tuiApprovalAsker{prompts: app.approvalPrompts}

	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?2004h\x1b[?1000h\x1b[?1006h")
	defer fmt.Fprint(os.Stdout, "\x1b[?25h\x1b[?1006l\x1b[?1000l\x1b[?2004l\x1b[?1049l")

	return app.run(os.Stdin, stdoutFD)
}

func newTUIApp(session *conversationSession, output io.Writer, width int, height int) *tuiApp {
	return &tuiApp{
		session:         session,
		output:          output,
		width:           width,
		height:          height,
		editor:          newComposerEditor(),
		ledgerEvents:    make(chan taskstate.RawTurnEvent, 256),
		turnOutcomes:    make(chan tuiTurnOutcome, 4),
		approvalPrompts: make(chan tuiApprovalPrompt, 4),
		loginURLs:       make(chan string, 2),
		loginOutcomes:   make(chan tuiLoginOutcome, 2),
	}
}

func (app *tuiApp) enqueueLedgerEvent(event taskstate.RawTurnEvent) {
	select {
	case app.ledgerEvents <- event:
	default:
	}
}

func (app *tuiApp) run(input io.Reader, stdoutFD int) error {
	keys := make(chan keyEvent, 64)
	keyErrors := make(chan error, 1)
	go func() {
		reader := newKeyReader(input)
		for {
			event, errorValue := reader.readKey()
			if errorValue != nil {
				keyErrors <- errorValue
				return
			}
			keys <- event
		}
	}()

	resizeSignals := make(chan os.Signal, 4)
	subscribeWindowResize(resizeSignals)
	defer signal.Stop(resizeSignals)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	app.render()
	for {
		select {
		case event := <-keys:
			if event.kind == keyIgnore {
				continue
			}
			if app.handleKey(event) {
				app.cancelIfRunning()
				return nil
			}
			app.render()
		case <-keyErrors:
			app.cancelIfRunning()
			return nil
		case rawEvent := <-app.ledgerEvents:
			app.handleLedgerEvent(rawEvent)
			app.drainBufferedLedgerEvents()
			app.render()
		case prompt := <-app.approvalPrompts:
			promptCopy := prompt
			app.pendingApproval = &promptCopy
			app.scrollOffset = 0
			app.render()
		case outcome := <-app.turnOutcomes:
			app.handleTurnOutcome(outcome)
			app.render()
		case authURL := <-app.loginURLs:
			app.appendNotice("login", "Opening browser for ChatGPT sign-in:\n"+authURL, false)
			app.render()
		case loginResult := <-app.loginOutcomes:
			app.cancelLogin = nil
			if loginResult.err != nil {
				if strings.Contains(loginResult.err.Error(), "context canceled") {
					app.appendNotice("login", "ChatGPT sign-in cancelled.", false)
				} else {
					app.appendNotice("login", "ChatGPT sign-in failed: "+loginResult.err.Error(), true)
				}
			} else {
				app.session.switchEndpoint(context.Background(), codexEndpointName)
				app.appendNotice("login", "Signed in to ChatGPT/Codex (account "+firstNonEmpty(loginResult.credentials.AccountID, "ready")+"). Switched to "+app.session.options.endpointURL+" ("+app.session.options.modelName+").", false)
				app.statusFlash = "signed in to codex"
			}
			app.render()
		case <-resizeSignals:
			app.width, app.height = terminalWindowSize(stdoutFD)
			app.render()
		case <-ticker.C:
			if app.activeTurn != nil {
				app.spinnerIndex++
				app.render()
			}
		}
	}
}

func (app *tuiApp) drainBufferedLedgerEvents() {
	for {
		select {
		case rawEvent := <-app.ledgerEvents:
			app.handleLedgerEvent(rawEvent)
		default:
			return
		}
	}
}

func (app *tuiApp) snapshot() tuiSnapshot {
	var hold *holdrecord.Hold
	if app.pendingApproval != nil {
		holdCopy := app.pendingApproval.hold
		hold = &holdCopy
	}
	var runningStarted time.Time
	if app.activeTurn != nil {
		runningStarted = app.activeTurn.startedAt
	}
	return tuiSnapshot{
		width:                 app.width,
		height:                app.height,
		modelName:             app.session.options.modelName,
		endpointURL:           app.session.options.endpointURL,
		workspacePath:         app.session.workspacePath,
		skillsCount:           len(app.session.instructions.skills),
		turnCount:             app.session.turnCount,
		totalPromptTokens:     app.session.totalPromptTokens + app.activeTurnPromptTokens(),
		totalCompletionTokens: app.session.totalCompletionTokens + app.activeTurnCompletionTokens(),
		showFullLedger:        app.showFullLedger,
		isRunning:             app.activeTurn != nil,
		runningStartedAt:      runningStarted,
		spinnerIndex:          app.spinnerIndex,
		items:                 app.items,
		queuedPrompts:         app.queuedPrompts,
		pendingApproval:       hold,
		editor:                app.editor,
		scrollOffset:          app.scrollOffset,
		statusFlash:           app.statusFlash,
	}
}

func (app *tuiApp) activeTurnPromptTokens() int64 {
	if app.activeTurn == nil {
		return 0
	}
	return app.activeTurn.promptTokens
}

func (app *tuiApp) activeTurnCompletionTokens() int64 {
	if app.activeTurn == nil {
		return 0
	}
	return app.activeTurn.completionTokens
}

func (app *tuiApp) render() {
	frame := renderTUISnapshot(app.snapshot())
	app.maxScrollOffset = frame.maxScrollOffset
	if app.scrollOffset > app.maxScrollOffset {
		app.scrollOffset = app.maxScrollOffset
	}
	_, _ = io.WriteString(app.output, frame.ansi)
}

func (app *tuiApp) handleKey(event keyEvent) bool {
	app.statusFlash = ""
	if app.pendingApproval != nil {
		return app.handleApprovalKey(event)
	}
	switch event.kind {
	case keyCtrlC:
		return app.handleCtrlC()
	case keyCtrlD:
		if len(app.editor.runes) == 0 && app.activeTurn == nil {
			return true
		}
		app.editor.deleteForward()
	case keyEscape:
		if app.activeTurn != nil {
			app.cancelIfRunning()
			app.statusFlash = "cancelling turn…"
		} else if app.cancelLogin != nil {
			app.cancelLogin()
			app.statusFlash = "cancelling sign-in…"
		} else if len(app.editor.runes) > 0 {
			app.editor.clear()
		}
	case keyCtrlL:
		return false
	case keyCtrlO:
		app.showFullLedger = !app.showFullLedger
		if app.showFullLedger {
			app.statusFlash = "ledger: full"
		} else {
			app.statusFlash = "ledger: compact"
		}
	case keyEnter:
		return app.handleSubmit(false)
	case keyAltEnter:
		return app.handleSubmit(true)
	case keyShiftEnter, keyCtrlJ:
		app.editor.insertRune('\n')
	case keyTab:
		app.editor.completeSlashCommand()
	case keyBackspace:
		app.editor.backspace()
	case keyDelete:
		app.editor.deleteForward()
	case keyCtrlW:
		app.editor.deleteWordBackward()
	case keyCtrlK:
		app.editor.killToLineEnd()
	case keyCtrlU:
		app.editor.killToLineStart()
	case keyCtrlA, keyHome:
		app.editor.moveLineStart()
	case keyCtrlE:
		app.editor.moveLineEnd()
	case keyEnd:
		if len(app.editor.runes) == 0 || app.scrollOffset > 0 {
			app.scrollOffset = 0
		} else {
			app.editor.moveLineEnd()
		}
	case keyLeft:
		app.editor.moveLeft()
	case keyRight:
		app.editor.moveRight()
	case keyUp:
		app.editor.historyPrevious()
	case keyDown:
		app.editor.historyNext()
	case keyPageUp:
		app.scrollUp(max(app.height/2, 6))
	case keyPageDown:
		app.scrollDown(max(app.height/2, 6))
	case keyScrollUp:
		app.scrollUp(3)
	case keyScrollDown:
		app.scrollDown(3)
	case keyPaste:
		app.editor.insertText(event.text)
	case keyRune:
		app.editor.insertRune(event.char)
	}
	return false
}

func (app *tuiApp) handleApprovalKey(event keyEvent) bool {
	switch event.kind {
	case keyCtrlC:
		app.resolveApproval(approval.Rejected)
		app.cancelIfRunning()
		return false
	case keyEscape:
		app.resolveApproval(approval.Rejected)
		return false
	case keyRune:
		switch event.char {
		case '1', 'y', 'Y':
			app.resolveApproval(approval.Approved)
		case '2', 'n', 'N':
			app.resolveApproval(approval.Rejected)
		}
	}
	return false
}

func (app *tuiApp) resolveApproval(answer approval.Answer) {
	if app.pendingApproval == nil {
		return
	}
	app.pendingApproval.replyTo <- answer
	if app.activeTurn != nil {
		label := "approval rejected"
		ink := inkFault
		if answer == approval.Approved {
			label = "approval granted"
			ink = inkTool
		}
		app.activeTurn.ledgerItems = append(app.activeTurn.ledgerItems, tuiLedgerItem{
			eventName: "approval.decision",
			line:      ledgerLine(ink, "●", "approval", ink+label+styleReset),
		})
	}
	app.pendingApproval = nil
}

func (app *tuiApp) handleCtrlC() bool {
	if app.activeTurn != nil {
		app.cancelIfRunning()
		app.statusFlash = "cancelling turn…"
		return false
	}
	if app.cancelLogin != nil {
		app.cancelLogin()
		app.statusFlash = "cancelling sign-in…"
		return false
	}
	if len(app.editor.runes) > 0 {
		app.editor.clear()
		app.statusFlash = "input cleared"
		return false
	}
	now := time.Now()
	if now.Sub(app.lastCtrlCAt) < 2*time.Second {
		return true
	}
	app.lastCtrlCAt = now
	app.statusFlash = "Ctrl+C again to exit"
	return false
}

func (app *tuiApp) handleSubmit(forceQueue bool) bool {
	text := strings.TrimSpace(app.editor.text())
	if text == "" {
		return false
	}
	app.editor.recordHistory(text)
	app.editor.clear()
	app.scrollOffset = 0

	if strings.HasPrefix(text, "/") {
		return app.handleSlashCommand(text)
	}
	if strings.TrimSpace(app.session.options.modelName) == "" {
		app.appendNotice("model not set", "Run /models to list models on "+app.session.options.endpointURL+", or /model <name> to select one. Use /endpoint <url> to change endpoints.", true)
		return false
	}
	if app.activeTurn != nil {
		if forceQueue {
			app.queuedPrompts = append(app.queuedPrompts, text)
			app.statusFlash = "queued follow-up"
			return false
		}
		if app.session.steerActiveTurn(text) {
			app.activeTurn.ledgerItems = append(app.activeTurn.ledgerItems, tuiLedgerItem{
				eventName: agentcontract.TaskEventAgentSteerReceived,
				line:      ledgerLine(inkGate, "↳", "steer", clippedTo(collapsedWhitespace(text), 88)),
			})
			app.statusFlash = "steering sent"
			return false
		}
		app.queuedPrompts = append(app.queuedPrompts, text)
		app.statusFlash = "queued follow-up"
		return false
	}
	app.startTurn(text)
	return false
}

func (app *tuiApp) handleSlashCommand(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/exit", "/quit":
		return true
	case "/clear", "/new":
		app.session.history = nil
		app.items = nil
		app.queuedPrompts = nil
		app.scrollOffset = 0
		app.statusFlash = "cleared"
		return false
	case "/ledger":
		if len(fields) >= 2 {
			switch strings.ToLower(fields[1]) {
			case "full":
				app.showFullLedger = true
			case "compact":
				app.showFullLedger = false
			}
		} else {
			app.showFullLedger = !app.showFullLedger
		}
		if app.showFullLedger {
			app.statusFlash = "ledger: full"
		} else {
			app.statusFlash = "ledger: compact"
		}
		return false
	case "/queue":
		queued := strings.TrimSpace(strings.TrimPrefix(line, "/queue"))
		if queued == "" {
			app.appendNotice("queue", "usage: /queue <prompt>", false)
			return false
		}
		if app.activeTurn == nil {
			app.startTurn(queued)
			return false
		}
		app.queuedPrompts = append(app.queuedPrompts, queued)
		app.statusFlash = "queued follow-up"
		return false
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
			credential := resolveAnthropicCredential(app.session.options.apiKey)
			if credential == "" {
				app.appendNotice("login", "To use Claude Extra Usage:\n1. Run `claude setup-token` in a terminal\n2. Run `/apikey sk-ant-oat01-...` here (or export CLAUDE_CODE_OAUTH_TOKEN / ANTHROPIC_API_KEY)\n3. Run `/endpoint claude`", false)
				return false
			}
			app.session.switchEndpoint(context.Background(), anthropicEndpointName)
			mode := "Anthropic API key"
			if isAnthropicOAuthToken(credential) {
				mode = "Extra Usage OAuth token (billed per token from claude.ai/settings/usage)"
			}
			app.appendNotice("login", "Switched to "+app.session.options.endpointURL+" ("+app.session.options.modelName+") using "+mode+".", false)
			app.statusFlash = "switched to claude"
			return false
		}
		if !forceLogin {
			if existing, errorValue := loadCodexCredentials(); errorValue == nil {
				app.session.switchEndpoint(context.Background(), codexEndpointName)
				app.appendNotice("login", "Using existing ChatGPT/Codex session (account "+firstNonEmpty(existing.AccountID, "ready")+"). Switched to "+app.session.options.endpointURL+" ("+app.session.options.modelName+"). Run /login codex --force to re-authenticate.", false)
				app.statusFlash = "switched to codex"
				return false
			}
		}
		if app.cancelLogin != nil {
			app.cancelLogin()
		}
		loginContext, cancelLogin := context.WithTimeout(context.Background(), codexOAuthLoginTimeout)
		app.cancelLogin = cancelLogin
		app.statusFlash = "waiting for browser sign-in…"
		go func() {
			credentials, loginError := runCodexOAuthLogin(loginContext, func(authURL string) {
				select {
				case app.loginURLs <- authURL:
				default:
				}
			})
			cancelLogin()
			app.loginOutcomes <- tuiLoginOutcome{credentials: credentials, err: loginError}
		}()
		return false
	case "/help":
		helpLines := []string{
			"Commands:",
			"  /login [codex|claude]   sign in to Codex or configure Claude Extra Usage",
			"  /models                 list models available on current endpoint",
			"  /model [name]           show or switch model (saved automatically)",
			"  /endpoint [url|codex|claude|gemini-api]  switch provider",
			"  /apikey <key>           set endpoint API key",
			"  /ledger [full|compact]  toggle ledger detail (or Ctrl+O)",
			"  /queue <prompt>         queue follow-up after current turn",
			"  /status                 show model, workspace, turns and tokens",
			"  /trace <path>           export last turn trace (.json or .md)",
			"  /clear                  reset conversation and clear screen",
			"  /exit                   exit bluecollar",
			"",
			"Shortcuts:",
			"  Enter                   send prompt (or steer while running)",
			"  Alt+Enter               queue follow-up while running",
			"  Shift+Enter / Ctrl+J    insert newline",
			"  Ctrl+O                  toggle compact/full ledger",
			"  PgUp / PgDn / Wheel     scroll viewport (End jumps to bottom)",
			"  Esc / Ctrl+C            cancel running turn",
		}
		app.appendNotice("help", strings.Join(helpLines, "\n"), false)
		return false
	default:
		buffer := &bytes.Buffer{}
		commandContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		handled, shouldExit := handleReplCommand(commandContext, app.session, line, buffer)
		cancel()
		if shouldExit {
			return true
		}
		if handled {
			app.appendNotice(strings.TrimPrefix(fields[0], "/"), strings.TrimSpace(buffer.String()), false)
			return false
		}
		app.appendNotice("unknown command", fields[0]+" (type /help to see commands)", true)
		return false
	}
}

func (app *tuiApp) appendNotice(title string, body string, isErr bool) {
	app.items = append(app.items, tuiTranscriptItem{
		notice: &tuiNoticeEntry{title: title, body: body, isErr: isErr},
	})
}

func (app *tuiApp) startTurn(prompt string) {
	app.nextTurnNumber++
	turn := &tuiTurnEntry{
		index:       app.nextTurnNumber,
		prompt:      prompt,
		startedAt:   time.Now(),
		isRunning:   true,
		activePhase: "planning",
	}
	app.activeTurn = turn
	app.items = append(app.items, tuiTranscriptItem{turn: turn})

	turnContext, cancelTurn := context.WithTimeout(context.Background(), app.session.options.timeout)
	app.cancelActiveTurn = cancelTurn
	go func() {
		result, errorValue := app.session.runPrompt(turnContext, prompt)
		cancelTurn()
		app.turnOutcomes <- tuiTurnOutcome{result: result, err: errorValue}
	}()
}

func (app *tuiApp) cancelIfRunning() {
	if app.cancelActiveTurn != nil {
		app.cancelActiveTurn()
	}
	if app.cancelLogin != nil {
		app.cancelLogin()
	}
}

func (app *tuiApp) handleLedgerEvent(rawEvent taskstate.RawTurnEvent) {
	if app.activeTurn == nil {
		return
	}
	if phase := activePhaseFromEvent(rawEvent); phase != "" {
		app.activeTurn.activePhase = phase
	}
	if rawEvent.Name == agentcontract.TaskEventLLMCall {
		var payload struct {
			PromptTokens     int64 `json:"promptTokens"`
			CompletionTokens int64 `json:"completionTokens"`
		}
		if json.Unmarshal([]byte(rawEvent.Body), &payload) == nil {
			app.activeTurn.promptTokens += payload.PromptTokens
			app.activeTurn.completionTokens += payload.CompletionTokens
		}
	}
	if item, keep := classifyLedgerItem(rawEvent); keep {
		app.activeTurn.ledgerItems = append(app.activeTurn.ledgerItems, item)
	}
}

func (app *tuiApp) handleTurnOutcome(outcome tuiTurnOutcome) {
	if app.activeTurn != nil {
		app.activeTurn.isRunning = false
		app.activeTurn.duration = time.Since(app.activeTurn.startedAt)
		if outcome.err != nil {
			app.activeTurn.status = agentcontract.TaskStatusFailed
			app.activeTurn.failureReason = outcome.err.Error()
			app.activeTurn.reply = outcome.err.Error()
		} else {
			app.activeTurn.status = outcome.result.TaskRun.Status
			app.activeTurn.failureReason = strings.TrimSpace(firstNonEmpty(outcome.result.TaskRun.FailureReason, outcome.result.ReplySuppressionReason))
			reply := strings.TrimSpace(firstNonEmpty(outcome.result.FinishMessage, outcome.result.UserNotice))
			if outcome.result.ReplySuppressed && app.activeTurn.status == agentcontract.TaskStatusRunning {
				app.activeTurn.status = agentcontract.TaskStatusCancelled
				app.activeTurn.reply = reply
			} else {
				app.activeTurn.reply = firstNonEmpty(reply, "(no reply)")
			}
		}
	}
	app.activeTurn = nil
	app.cancelActiveTurn = nil
	if len(app.queuedPrompts) > 0 {
		nextPrompt := app.queuedPrompts[0]
		app.queuedPrompts = app.queuedPrompts[1:]
		app.startTurn(nextPrompt)
	}
}

func (app *tuiApp) scrollUp(delta int) {
	app.scrollOffset = min(app.scrollOffset+delta, app.maxScrollOffset)
}

func (app *tuiApp) scrollDown(delta int) {
	app.scrollOffset = max(app.scrollOffset-delta, 0)
}
