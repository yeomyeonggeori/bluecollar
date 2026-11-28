package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

var tuiSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	tuiBorderDim     = "\x1b[38;5;239m"
	tuiBorderActive  = "\x1b[38;5;68m"
	tuiBorderWarning = "\x1b[38;5;222m"
	tuiAccentBlue    = "\x1b[38;5;68m"
	tuiSurfaceMuted  = "\x1b[38;5;245m"
	tuiMaxInputLines = 6
)

type tuiLedgerItem struct {
	eventName string
	line      string
	isVerbose bool
}

type tuiTurnEntry struct {
	index            int
	prompt           string
	startedAt        time.Time
	duration         time.Duration
	isRunning        bool
	activePhase      string
	ledgerItems      []tuiLedgerItem
	reply            string
	status           agentcontract.TaskStatus
	failureReason    string
	promptTokens     int64
	completionTokens int64
}

type tuiNoticeEntry struct {
	title string
	body  string
	isErr bool
}

type tuiTranscriptItem struct {
	turn   *tuiTurnEntry
	notice *tuiNoticeEntry
}

type tuiSnapshot struct {
	width                 int
	height                int
	modelName             string
	endpointURL           string
	workspacePath         string
	skillsCount           int
	turnCount             int
	totalPromptTokens     int64
	totalCompletionTokens int64
	showFullLedger        bool
	isRunning             bool
	runningStartedAt      time.Time
	spinnerIndex          int
	items                 []tuiTranscriptItem
	queuedPrompts         []string
	pendingApproval       *holdrecord.Hold
	editor                *composerEditor
	scrollOffset          int
	statusFlash           string
}

type renderedFrame struct {
	ansi            string
	plainLines      []string
	cursorRow       int
	cursorColumn    int
	maxScrollOffset int
}

func renderTUISnapshot(snapshot tuiSnapshot) renderedFrame {
	width := max(snapshot.width, 40)
	height := max(snapshot.height, 14)

	headerLines := renderHeaderLines(snapshot, width)
	approvalLines := renderApprovalCard(snapshot.pendingApproval, width)
	slashHintLines := renderSlashHintLines(snapshot.editor, snapshot.pendingApproval != nil, width)
	var composerLines []string
	cursorOffsetRow := 0
	cursorColumn := 1
	if snapshot.pendingApproval == nil {
		composerLines, cursorOffsetRow, cursorColumn = renderComposerBox(snapshot, width)
	}
	footerLine := renderFooterLine(snapshot, width)

	bottomChromeHeight := len(approvalLines) + len(slashHintLines) + len(composerLines) + 1
	viewportHeight := max(height-len(headerLines)-bottomChromeHeight, 3)

	contentLines := renderTranscriptLines(snapshot, width)
	maxScroll := max(len(contentLines)-viewportHeight, 0)
	scrollOffset := min(max(snapshot.scrollOffset, 0), maxScroll)

	visibleLines := sliceViewportLines(contentLines, viewportHeight, scrollOffset)
	if scrollOffset > 0 && len(visibleLines) > 0 {
		badge := tuiAccentBlue + " ↑ scrolled (" + strconv.Itoa(scrollOffset) + " lines) · End to follow " + styleReset
		visibleLines[len(visibleLines)-1] = alignColumns(visibleLines[len(visibleLines)-1], badge, width)
	}

	screenLines := make([]string, 0, height)
	screenLines = append(screenLines, headerLines...)
	screenLines = append(screenLines, visibleLines...)
	screenLines = append(screenLines, approvalLines...)
	screenLines = append(screenLines, slashHintLines...)
	composerStartRow := len(screenLines) + 1
	screenLines = append(screenLines, composerLines...)
	screenLines = append(screenLines, footerLine)

	for len(screenLines) < height {
		screenLines = append(screenLines, "")
	}
	if len(screenLines) > height {
		screenLines = screenLines[:height]
	}

	var frame strings.Builder
	frame.WriteString("\x1b[?2026h\x1b[?25l\x1b[H")
	for index, line := range screenLines {
		sanitized := singleLineScreenRow(line)
		screenLines[index] = sanitized
		frame.WriteString("\x1b[2K")
		frame.WriteString(truncateToWidth(sanitized, width))
		if index < len(screenLines)-1 {
			frame.WriteString("\r\n")
		}
	}
	cursorRow := composerStartRow + cursorOffsetRow
	if snapshot.pendingApproval == nil {
		frame.WriteString(fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", cursorRow, cursorColumn))
	}
	frame.WriteString("\x1b[?2026l")

	return renderedFrame{
		ansi:            frame.String(),
		plainLines:      screenLines,
		cursorRow:       cursorRow,
		cursorColumn:    cursorColumn,
		maxScrollOffset: maxScroll,
	}
}

func renderHeaderLines(snapshot tuiSnapshot, width int) []string {
	brand := tuiAccentBlue + styleBold + "◆ bluecollar" + styleReset
	left := brand
	if width >= 76 {
		workspace := abbreviatedHomePath(snapshot.workspacePath)
		left += "  " + inkFaint + workspace + styleReset
	}
	status := headerStatusBadge(snapshot)
	displayModel := firstNonEmpty(snapshot.modelName, "no model")
	modelText := tuiSurfaceMuted + displayModel + styleReset
	right := modelText + "  " + status
	if width < 64 {
		right = truncateToWidth(modelText, max(width-displayWidth(brand)-displayWidth(status)-4, 8)) + "  " + status
	}
	divider := tuiBorderDim + strings.Repeat("─", width) + styleReset
	return []string{
		" " + alignColumns(left, right, max(width-2, 10)) + " ",
		divider,
	}
}

func headerStatusBadge(snapshot tuiSnapshot) string {
	if snapshot.pendingApproval != nil {
		return inkGate + styleBold + "● approval" + styleReset
	}
	if snapshot.isRunning {
		spinner := tuiSpinnerFrames[snapshot.spinnerIndex%len(tuiSpinnerFrames)]
		elapsed := ""
		if !snapshot.runningStartedAt.IsZero() {
			elapsed = fmt.Sprintf(" · %.1fs", time.Since(snapshot.runningStartedAt).Seconds())
		}
		return tuiAccentBlue + styleBold + spinner + " working" + elapsed + styleReset
	}
	if snapshot.statusFlash != "" {
		return inkTool + snapshot.statusFlash + styleReset
	}
	if strings.TrimSpace(snapshot.modelName) == "" {
		return inkGate + "● setup" + styleReset
	}
	return inkFaint + "● ready" + styleReset
}

func renderTranscriptLines(snapshot tuiSnapshot, width int) []string {
	innerWidth := max(width-4, 24)
	lines := []string{""}
	if len(snapshot.items) == 0 {
		lines = append(lines, renderWelcomeLines(snapshot, width)...)
		return lines
	}
	for index, item := range snapshot.items {
		if index > 0 {
			lines = append(lines, "")
		}
		if item.notice != nil {
			lines = append(lines, renderNoticeLines(item.notice, innerWidth)...)
			continue
		}
		if item.turn != nil {
			lines = append(lines, renderTurnLines(item.turn, snapshot.showFullLedger, snapshot.spinnerIndex, innerWidth)...)
		}
	}
	if len(snapshot.queuedPrompts) > 0 {
		lines = append(lines, "")
		for index, queued := range snapshot.queuedPrompts {
			label := fmt.Sprintf("queued %d", index+1)
			lines = append(lines, "  "+inkGate+"↳ "+label+styleReset+"  "+inkFaint+truncateToWidth(collapsedWhitespace(queued), max(innerWidth-14, 12))+styleReset)
		}
	}
	lines = append(lines, "")
	return lines
}

func renderWelcomeLines(snapshot tuiSnapshot, width int) []string {
	boxWidth := min(welcomeInnerWidth, max(width-6, 30))
	leftMargin := max((width-(boxWidth+2))/2, 1)
	margin := strings.Repeat(" ", leftMargin)

	title := " bluecollar "
	topFill := max(boxWidth-len(title)-1, 1)
	boxLine := func(content string, centered bool) string {
		trimmed := truncateToWidth(content, max(boxWidth-2, 4))
		padding := max(boxWidth-displayWidth(trimmed), 0)
		left := 1
		if centered {
			left = padding / 2
		}
		right := max(padding-left, 0)
		return margin + welcomeInk() + "│" + styleReset + strings.Repeat(" ", left) + trimmed + strings.Repeat(" ", right) + welcomeInk() + "│" + styleReset
	}
	detailLine := func(label string, value string) string {
		return boxLine(styleDim+fmt.Sprintf("%-*s", welcomeLabelWidth, label)+styleReset+value, false)
	}

	lines := []string{
		margin + welcomeInk() + "╭─" + styleBold + title + styleReset + welcomeInk() + strings.Repeat("─", topFill) + "╮" + styleReset,
		boxLine("", false),
	}
	for _, mascotLine := range mascotLines() {
		lines = append(lines, boxLine(mascotLine, true))
	}
	modelSummary := firstNonEmpty(snapshot.modelName, "not set (run /models)")
	lines = append(lines,
		boxLine("", false),
		boxLine(styleBold+"Welcome to the shift."+styleReset, true),
		boxLine("", false),
		detailLine("model", modelSummary),
	)
	if strings.TrimSpace(snapshot.modelName) == "" && strings.TrimSpace(snapshot.endpointURL) != "" {
		lines = append(lines, detailLine("endpoint", snapshot.endpointURL))
	}
	lines = append(lines, detailLine("workspace", abbreviatedHomePath(snapshot.workspacePath)))
	if snapshot.skillsCount > 0 {
		lines = append(lines, detailLine("skills", strconv.Itoa(snapshot.skillsCount)+" loaded"))
	}
	lines = append(lines,
		detailLine("ledger", "Ctrl+O compact/full"),
		detailLine("commands", "/model /status /help /exit"),
		boxLine("", false),
		margin+welcomeInk()+"╰"+strings.Repeat("─", boxWidth)+"╯"+styleReset,
	)
	return lines
}

func renderNoticeLines(notice *tuiNoticeEntry, innerWidth int) []string {
	accent := tuiAccentBlue
	if notice.isErr {
		accent = inkFault
	}
	lines := []string{"  " + accent + styleBold + "· " + notice.title + styleReset}
	for _, line := range wrapPlainText(notice.body, max(innerWidth-2, 16)) {
		lines = append(lines, "    "+tuiSurfaceMuted+line+styleReset)
	}
	return lines
}

func renderTurnLines(turn *tuiTurnEntry, showFullLedger bool, spinnerIndex int, innerWidth int) []string {
	lines := []string{}
	promptWidth := max(innerWidth-4, 16)
	wrappedPrompt := wrapPlainText(turn.prompt, promptWidth)
	for index, line := range wrappedPrompt {
		bar := tuiAccentBlue + "▎" + styleReset
		text := styleBold + line + styleReset
		if index == 0 && innerWidth >= 56 {
			tag := inkFaint + "#" + strconv.Itoa(turn.index) + styleReset
			lines = append(lines, "  "+bar+" "+alignColumns(text, tag, innerWidth-2))
		} else {
			lines = append(lines, "  "+bar+" "+text)
		}
	}

	visibleLedger := []tuiLedgerItem{}
	hiddenCount := 0
	for _, item := range turn.ledgerItems {
		if item.isVerbose && !showFullLedger {
			hiddenCount++
			continue
		}
		visibleLedger = append(visibleLedger, item)
	}
	if len(visibleLedger) > 0 || hiddenCount > 0 {
		lines = append(lines, "")
	}
	for _, item := range visibleLedger {
		lines = append(lines, "  "+truncateToWidth(item.line, innerWidth))
	}
	if hiddenCount > 0 && !turn.isRunning {
		lines = append(lines, "  "+inkFaint+"  … "+strconv.Itoa(hiddenCount)+" trace event(s) hidden (Ctrl+O to expand)"+styleReset)
	}

	if turn.isRunning {
		spinner := tuiSpinnerFrames[spinnerIndex%len(tuiSpinnerFrames)]
		phase := turn.activePhase
		if phase == "" {
			phase = "thinking"
		}
		elapsed := fmt.Sprintf("%.1fs", time.Since(turn.startedAt).Seconds())
		lines = append(lines, "  "+tuiAccentBlue+spinner+" "+phase+styleReset+"  "+inkFaint+elapsed+styleReset)
		return lines
	}

	if strings.TrimSpace(turn.reply) != "" {
		lines = append(lines, "")
		for _, markdownLine := range renderMarkdownLines(turn.reply, max(innerWidth-2, 16)) {
			lines = append(lines, "  "+markdownLine)
		}
	}
	lines = append(lines, "", "  "+renderTurnSummaryLine(turn))
	return lines
}

func renderTurnSummaryLine(turn *tuiTurnEntry) string {
	statusBadge := renderStatusPill(turn.status)
	parts := []string{statusBadge}
	if turn.duration > 0 {
		parts = append(parts, inkFaint+fmt.Sprintf("%.1fs", turn.duration.Seconds())+styleReset)
	}
	if turn.promptTokens > 0 || turn.completionTokens > 0 {
		parts = append(parts, inkFaint+formatTokenPair(turn.promptTokens, turn.completionTokens)+styleReset)
	}
	if reason := collapsedWhitespace(turn.failureReason); reason != "" {
		parts = append(parts, inkFault+clippedTo(reason, 60)+styleReset)
	}
	return strings.Join(parts, "  "+inkFaint+"·"+styleReset+"  ")
}

func singleLineScreenRow(line string) string {
	if !strings.ContainsAny(line, "\r\n") {
		return line
	}
	line = strings.ReplaceAll(line, "\r\n", " ")
	line = strings.ReplaceAll(line, "\r", " ")
	return strings.ReplaceAll(line, "\n", " ")
}

func renderStatusPill(status agentcontract.TaskStatus) string {
	switch status {
	case agentcontract.TaskStatusCompleted:
		return inkTool + styleBold + "✓ completed" + styleReset
	case agentcontract.TaskStatusFailed:
		return inkFault + styleBold + "✗ failed" + styleReset
	case agentcontract.TaskStatusBlocked:
		return inkGate + styleBold + "● blocked" + styleReset
	case agentcontract.TaskStatusWaitingUserInput:
		return inkGate + styleBold + "? waiting" + styleReset
	case agentcontract.TaskStatusCancelled:
		return inkFaint + styleBold + "○ cancelled" + styleReset
	default:
		return inkFaint + string(status) + styleReset
	}
}

func renderApprovalCard(pending *holdrecord.Hold, width int) []string {
	if pending == nil {
		return nil
	}
	innerWidth := max(width-4, 24)
	header := " Approval required "
	topFill := max(innerWidth-len(header)-1, 1)
	lines := []string{
		" " + tuiBorderWarning + "╭─" + styleBold + header + styleReset + tuiBorderWarning + strings.Repeat("─", topFill) + "╮" + styleReset,
	}
	confirmation := strings.TrimSpace(pending.Call.Confirmation)
	if confirmation == "" {
		confirmation = "Allow tool call " + pending.Call.ToolName + "?"
	}
	for _, line := range wrapPlainText(confirmation, max(innerWidth-2, 16)) {
		lines = append(lines, " "+tuiBorderWarning+"│"+styleReset+" "+padRightToWidth(line, innerWidth-2)+" "+tuiBorderWarning+"│"+styleReset)
	}
	actions := inkTool + styleBold + "[1 / y] Allow" + styleReset + "    " + inkFault + styleBold + "[2 / n / Esc] Reject" + styleReset
	lines = append(lines,
		" "+tuiBorderWarning+"│"+styleReset+" "+padRightToWidth(actions, innerWidth-2)+" "+tuiBorderWarning+"│"+styleReset,
		" "+tuiBorderWarning+"╰"+strings.Repeat("─", innerWidth)+"╯"+styleReset,
	)
	return lines
}

func renderSlashHintLines(editor *composerEditor, hasApproval bool, width int) []string {
	if hasApproval || editor == nil {
		return nil
	}
	matches := editor.matchingSlashCommands()
	if len(matches) == 0 {
		return nil
	}
	parts := make([]string, 0, min(len(matches), 4))
	for index := 0; index < len(matches) && index < 4; index++ {
		command := matches[index]
		parts = append(parts, tuiAccentBlue+command.usage+styleReset+" "+inkFaint+command.description+styleReset)
	}
	return []string{"  " + truncateToWidth(strings.Join(parts, "   "), max(width-4, 16))}
}

func renderComposerBox(snapshot tuiSnapshot, width int) ([]string, int, int) {
	innerWidth := max(width-4, 24)
	contentWidth := max(innerWidth-4, 12)
	borderColor := tuiBorderDim
	title := ""
	placeholder := "Ask bluecollar to do a task, or type /help"
	if strings.TrimSpace(snapshot.modelName) == "" {
		placeholder = "Run /models to discover models, or /model <name> to select one"
	}
	if snapshot.isRunning {
		borderColor = tuiBorderActive
		title = " steer "
		placeholder = "Type guidance to steer the running task…"
	}

	var layout composerVisualLayout
	if snapshot.editor != nil {
		layout = snapshot.editor.layout(contentWidth, tuiMaxInputLines)
	} else {
		layout = composerVisualLayout{lines: []string{""}}
	}

	topLine := " " + borderColor + "╭" + strings.Repeat("─", innerWidth) + "╮" + styleReset
	if title != "" && displayWidth(title)+4 < innerWidth {
		topFill := max(innerWidth-displayWidth(title)-1, 1)
		topLine = " " + borderColor + "╭─" + styleReset + tuiSurfaceMuted + title + styleReset + borderColor + strings.Repeat("─", topFill) + "╮" + styleReset
	}

	lines := []string{topLine}
	isEmpty := snapshot.editor == nil || len(snapshot.editor.runes) == 0
	for index, line := range layout.lines {
		gutter := "  "
		if index == 0 {
			gutter = tuiAccentBlue + styleBold + "❯ " + styleReset
		}
		displayText := line
		if isEmpty && index == 0 {
			displayText = inkFaint + truncateToWidth(placeholder, contentWidth) + styleReset
		}
		padded := padRightToWidth(displayText, contentWidth)
		lines = append(lines, " "+borderColor+"│"+styleReset+" "+gutter+padded+" "+borderColor+"│"+styleReset)
	}
	lines = append(lines, " "+borderColor+"╰"+strings.Repeat("─", innerWidth)+"╯"+styleReset)

	cursorRowOffset := 1 + layout.cursorRow
	cursorColumn := 6 + layout.cursorColumn
	return lines, cursorRowOffset, cursorColumn
}

func renderFooterLine(snapshot tuiSnapshot, width int) string {
	ledgerLabel := "compact"
	if snapshot.showFullLedger {
		ledgerLabel = "full"
	}
	leftParts := []string{
		formatTokenPair(snapshot.totalPromptTokens, snapshot.totalCompletionTokens),
		strconv.Itoa(snapshot.turnCount) + "t",
	}
	if width >= 72 {
		leftParts = append(leftParts, "ledger "+ledgerLabel)
	}
	if snapshot.skillsCount > 0 && width >= 92 {
		leftParts = append(leftParts, strconv.Itoa(snapshot.skillsCount)+" skills")
	}
	left := inkFaint + strings.Join(leftParts, " · ") + styleReset

	rightHint := footerKeyHint(snapshot, width)
	right := inkFaint + rightHint + styleReset
	return " " + alignColumns(left, right, max(width-2, 10)) + " "
}

func footerKeyHint(snapshot tuiSnapshot, width int) string {
	if snapshot.pendingApproval != nil {
		return "1/y allow · 2/n/Esc reject"
	}
	if snapshot.isRunning {
		if width >= 92 {
			return "Enter steer · Alt+Enter queue · Esc cancel"
		}
		return "Enter steer · Esc cancel"
	}
	if width >= 92 {
		return "Enter send · ^J newline · ^O ledger · /help"
	}
	if width >= 72 {
		return "Enter send · ^O ledger · /help"
	}
	return "^O ledger · /help"
}

func sliceViewportLines(lines []string, height int, scrollOffset int) []string {
	if len(lines) <= height {
		padded := make([]string, len(lines), height)
		copy(padded, lines)
		for len(padded) < height {
			padded = append(padded, "")
		}
		return padded
	}
	end := len(lines) - scrollOffset
	if end < height {
		end = height
	}
	start := end - height
	return append([]string{}, lines[start:end]...)
}

func formatTokenPair(promptTokens int64, completionTokens int64) string {
	return formatCompactNumber(promptTokens) + "↑ " + formatCompactNumber(completionTokens) + "↓"
}

func formatCompactNumber(value int64) string {
	if value >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(value)/1000000)
	}
	if value >= 1000 {
		return fmt.Sprintf("%.1fk", float64(value)/1000)
	}
	return strconv.FormatInt(value, 10)
}

func classifyLedgerItem(rawEvent taskstate.RawTurnEvent) (tuiLedgerItem, bool) {
	taskEvent := agentcontract.TaskEvent{Name: rawEvent.Name, Body: rawEvent.Body}
	line := prettyLedgerLine(taskEvent)
	if line == "" {
		return tuiLedgerItem{}, false
	}
	isVerbose := true
	if _, isToolRequest := agentcontract.ToolTaskEventToolName(taskEvent.Name, agentcontract.ToolTaskEventRequestedSuffix); isToolRequest {
		isVerbose = false
	} else if _, isToolResult := agentcontract.ToolTaskEventToolName(taskEvent.Name, agentcontract.ToolTaskEventResultSuffix); isToolResult {
		isVerbose = false
	} else {
		switch taskEvent.Name {
		case agentcontract.TaskEventAgentEvidenceMissing,
			agentcontract.TaskEventAgentCompletionRequired,
			agentcontract.TaskEventCompletionChangeCheck,
			agentcontract.TaskEventAgentBudgetExtendedOneLevel,
			agentcontract.TaskEventAgentRecoveryAttempt,
			agentcontract.TaskEventAgentSteerReceived,
			agentcontract.TaskEventTaskCompleted,
			agentcontract.TaskEventTaskFailed:
			isVerbose = false
		}
	}
	return tuiLedgerItem{
		eventName: taskEvent.Name,
		line:      line,
		isVerbose: isVerbose,
	}, true
}

func activePhaseFromEvent(rawEvent taskstate.RawTurnEvent) string {
	if toolName, isToolRequest := agentcontract.ToolTaskEventToolName(rawEvent.Name, agentcontract.ToolTaskEventRequestedSuffix); isToolRequest {
		return "running " + toolName
	}
	if _, isToolResult := agentcontract.ToolTaskEventToolName(rawEvent.Name, agentcontract.ToolTaskEventResultSuffix); isToolResult {
		return "thinking"
	}
	switch rawEvent.Name {
	case agentcontract.TaskEventLLMCall:
		return "thinking"
	case agentcontract.TaskEventCompletionChangeCheck:
		return "verifying completion"
	case agentcontract.TaskEventAgentSteerReceived:
		return "applying steering"
	}
	return ""
}
