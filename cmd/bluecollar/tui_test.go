package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func stripANSI(text string) string {
	var builder strings.Builder
	inEscape := false
	for _, character := range text {
		if inEscape {
			if character == 'm' {
				inEscape = false
			}
			continue
		}
		if character == '\x1b' {
			inEscape = true
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func TestTUIKeyReaderParsesUTF8ArrowsMouseAndBracketedPaste(t *testing.T) {
	sequence := "한\x1b[A\x1b[5~\x1b[<64;10;5M\x1b[<65;10;5M\x1b[13;2u\x1b\r\x1b[200~line1\r\nline2\x1b[201~\x0f"
	reader := newKeyReader(strings.NewReader(sequence))

	expected := []keyEvent{
		{kind: keyRune, char: '한'},
		{kind: keyUp},
		{kind: keyPageUp},
		{kind: keyScrollUp},
		{kind: keyScrollDown},
		{kind: keyShiftEnter},
		{kind: keyAltEnter},
		{kind: keyPaste, text: "line1\nline2"},
		{kind: keyCtrlO},
	}
	for index, want := range expected {
		got, errorValue := reader.readKey()
		if errorValue != nil {
			t.Fatalf("event %d failed: %v", index, errorValue)
		}
		if got != want {
			t.Fatalf("event %d: got %+v, want %+v", index, got, want)
		}
	}
}

func TestTUIDisplayWidthAndComposerCursorHandleKorean(t *testing.T) {
	if width := displayWidth(styleBold + "한글abc" + styleReset); width != 7 {
		t.Fatalf("expected display width 7 for 한글abc, got %d", width)
	}

	editor := newComposerEditor()
	editor.insertText("한글 test")
	layout := editor.layout(20, 4)
	if layout.cursorRow != 0 || layout.cursorColumn != 9 {
		t.Fatalf("expected cursor at (0,9), got (%d,%d)", layout.cursorRow, layout.cursorColumn)
	}

	editor.clear()
	editor.insertText("/mo")
	if !editor.completeSlashCommand() || editor.text() != "/model " {
		t.Fatalf("expected /mo to complete to '/model ', got %q", editor.text())
	}
}

func TestTUIAppHandlesSteeringQueueApprovalAndLedgerToggle(t *testing.T) {
	events := taskstate.NewTaskEventService()
	runs := taskstate.NewTaskRunService(events)
	taskRun := runs.CreateTaskRunWithOrigin("person-local", taskstate.TaskRunOrigin{ConversationID: "conversation-local"}, "initial")
	session := &conversationSession{
		options:        runOptions{modelName: "qwen3.5:4b", endpointURL: "http://127.0.0.1:11434/v1"},
		taskRunService: runs,
		workspacePath:  "/tmp/work",
	}
	session.setActiveTaskRunID(taskRun.TaskRunID)

	buffer := &bytes.Buffer{}
	app := newTUIApp(session, buffer, 100, 28)
	app.activeTurn = &tuiTurnEntry{
		index:       1,
		prompt:      "run tests",
		startedAt:   time.Now().Add(-2 * time.Second),
		isRunning:   true,
		activePhase: "running bash",
	}
	app.items = append(app.items, tuiTranscriptItem{turn: app.activeTurn})

	app.handleLedgerEvent(taskstate.RawTurnEvent{
		TaskRunID: taskRun.TaskRunID,
		Name:      agentcontract.TaskEventLLMCall,
		Body:      `{"kind":"action","model":"qwen3.5:4b","latencyMs":420,"promptTokens":1200,"completionTokens":80}`,
	})
	if app.activeTurn.promptTokens != 1200 || app.activeTurn.completionTokens != 80 {
		t.Fatalf("expected live token tracking, got %d/%d", app.activeTurn.promptTokens, app.activeTurn.completionTokens)
	}

	app.editor.setText("focus on loop package only")
	app.handleKey(keyEvent{kind: keyEnter})
	recorded := events.ListTaskEvent(taskRun.TaskRunID)
	foundSteer := false
	for _, event := range recorded {
		if event.Name == agentcontract.TaskEventAgentSteerReceived && strings.Contains(event.Body, "focus on loop package only") {
			foundSteer = true
		}
	}
	if !foundSteer {
		t.Fatalf("expected Enter while running to steer active task, got events %+v", recorded)
	}

	app.editor.setText("summarize failures")
	app.handleKey(keyEvent{kind: keyAltEnter})
	if len(app.queuedPrompts) != 1 || app.queuedPrompts[0] != "summarize failures" {
		t.Fatalf("expected Alt+Enter to queue follow-up, got %+v", app.queuedPrompts)
	}

	replyChannel := make(chan approval.Answer, 1)
	app.pendingApproval = &tuiApprovalPrompt{
		hold:    holdrecord.Hold{Call: agentcontract.HeldCall{ToolName: "bash", Confirmation: "rm -rf build?"}},
		replyTo: replyChannel,
	}
	app.handleKey(keyEvent{kind: keyRune, char: '1'})
	if answer := <-replyChannel; answer != approval.Approved {
		t.Fatalf("expected '1' to approve held call, got %q", answer)
	}
	if app.pendingApproval != nil {
		t.Fatal("expected approval card to close after answer")
	}

	app.handleKey(keyEvent{kind: keyCtrlO})
	if !app.showFullLedger {
		t.Fatal("expected Ctrl+O to toggle full ledger on")
	}
}

func TestTUIRenderFramesRespectBoundsAndLayout(t *testing.T) {
	editor := newComposerEditor()
	editor.insertText("테스트를 돌려줘")

	runningTurn := &tuiTurnEntry{
		index:       1,
		prompt:      "loop 패키지의 실패 원인을 찾고 고쳐줘",
		startedAt:   time.Now().Add(-4 * time.Second),
		isRunning:   true,
		activePhase: "running bash",
		ledgerItems: []tuiLedgerItem{
			{eventName: agentcontract.TaskEventLLMCall, line: prettyLedgerLine(agentcontract.TaskEvent{Name: agentcontract.TaskEventLLMCall, Body: `{"kind":"action","model":"qwen3.5:4b","latencyMs":850,"promptTokens":3420,"completionTokens":128}`}), isVerbose: true},
			{eventName: "tool.bash.requested", line: prettyLedgerLine(agentcontract.TaskEvent{Name: "tool.bash.requested", Body: `{"input":{"command":"go test ./loop/..."}}`})},
			{eventName: "tool.bash.result", line: prettyLedgerLine(agentcontract.TaskEvent{Name: "tool.bash.result", Body: `{"output":{"content":"ok github.com/yeomyeonggeori/bluecollar/loop 1.2s"}}`})},
		},
		promptTokens:     3420,
		completionTokens: 128,
	}

	completedTurn := &tuiTurnEntry{
		index:            1,
		prompt:           "bluecollar와 pi 차이를 요약해줘",
		startedAt:        time.Now().Add(-6 * time.Second),
		duration:         2400 * time.Millisecond,
		status:           agentcontract.TaskStatusCompleted,
		reply:            "## 핵심 요약\n\n- **완료 검증**: `completion.change_check`로 원장 기반 확인\n- **실패 보고**: 해결 불가 시 `fail` 액션으로 명시적 보고\n\n```go\nresult, err := kernel.RunTurn(ctx, req)\n```",
		promptTokens:     4810,
		completionTokens: 312,
		ledgerItems: []tuiLedgerItem{
			{eventName: "tool.file_read.requested", line: prettyLedgerLine(agentcontract.TaskEvent{Name: "tool.file_read.requested", Body: `{"input":{"path":"README.md"}}`})},
			{eventName: agentcontract.TaskEventCompletionChangeCheck, line: prettyLedgerLine(agentcontract.TaskEvent{Name: agentcontract.TaskEventCompletionChangeCheck, Body: `{"unmet":[]}`})},
		},
	}

	cases := []struct {
		name     string
		snapshot tuiSnapshot
	}{
		{
			name: "welcome-wide",
			snapshot: tuiSnapshot{
				width:         96,
				height:        26,
				modelName:     "qwen3.5:4b",
				workspacePath: "/Users/lee/Developer/work/bluecollar",
				skillsCount:   3,
				editor:        newComposerEditor(),
			},
		},
		{
			name: "running-with-queue",
			snapshot: tuiSnapshot{
				width:                 96,
				height:                26,
				modelName:             "qwen3.5:4b",
				workspacePath:         "/Users/lee/Developer/work/bluecollar",
				isRunning:             true,
				runningStartedAt:      runningTurn.startedAt,
				spinnerIndex:          3,
				totalPromptTokens:     3420,
				totalCompletionTokens: 128,
				items:                 []tuiTranscriptItem{{turn: runningTurn}},
				queuedPrompts:         []string{"끝나면 git diff 요약해줘"},
				editor:                editor,
			},
		},
		{
			name: "completed-korean-markdown",
			snapshot: tuiSnapshot{
				width:                 96,
				height:                26,
				modelName:             "qwen3.5:4b",
				workspacePath:         "/Users/lee/Developer/work/bluecollar",
				turnCount:             1,
				totalPromptTokens:     4810,
				totalCompletionTokens: 312,
				items:                 []tuiTranscriptItem{{turn: completedTurn}},
				editor:                newComposerEditor(),
			},
		},
		{
			name: "approval-modal",
			snapshot: tuiSnapshot{
				width:            96,
				height:           26,
				modelName:        "qwen3.5:4b",
				workspacePath:    "/Users/lee/Developer/work/bluecollar",
				isRunning:        true,
				runningStartedAt: runningTurn.startedAt,
				items:            []tuiTranscriptItem{{turn: runningTurn}},
				pendingApproval:  &holdrecord.Hold{Call: agentcontract.HeldCall{ToolName: "bash", Confirmation: "작업 디렉터리에서 `git reset --hard`를 실행할까요?"}},
				editor:           newComposerEditor(),
			},
		},
		{
			name: "narrow-screen",
			snapshot: tuiSnapshot{
				width:                 58,
				height:                20,
				modelName:             "google/gemini-3.7-flash",
				workspacePath:         "/Users/lee/Developer/work/bluecollar",
				turnCount:             1,
				totalPromptTokens:     4810,
				totalCompletionTokens: 312,
				items:                 []tuiTranscriptItem{{turn: completedTurn}},
				editor:                editor,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			frame := renderTUISnapshot(testCase.snapshot)
			if len(frame.plainLines) != testCase.snapshot.height {
				t.Fatalf("expected %d rows, got %d", testCase.snapshot.height, len(frame.plainLines))
			}
			var preview strings.Builder
			preview.WriteString("\n")
			for rowIndex, line := range frame.plainLines {
				width := displayWidth(line)
				if width > testCase.snapshot.width {
					t.Fatalf("row %d overflowed width %d (got %d): %q", rowIndex, testCase.snapshot.width, width, stripANSI(line))
				}
				preview.WriteString(stripANSI(line))
				preview.WriteString("\n")
			}
			t.Log(preview.String())
		})
	}
}

func TestPrettyLedgerAndCompactFiltering(t *testing.T) {
	writeReq, keepWrite := classifyLedgerItem(taskstate.RawTurnEvent{
		Name: "tool.write.requested",
		Body: `{"input":{"path":"hello.c","content":"#include <stdio.h>\nint main(void) {\n  puts(\"hi\");\n  return 0;\n}\n"}}`,
	})
	if !keepWrite || writeReq.isVerbose {
		t.Fatalf("expected write request visible in compact mode")
	}
	plainWrite := stripANSI(writeReq.line)
	if !strings.Contains(plainWrite, "hello.c") || !strings.Contains(plainWrite, "(5 lines)") || strings.Contains(plainWrite, `\u003c`) {
		t.Fatalf("expected clean write summary, got %q", plainWrite)
	}

	planReq, keepPlan := classifyLedgerItem(taskstate.RawTurnEvent{
		Name: "tool.plan.requested",
		Body: `{"input":{"goal":"C 파일 만들고 실행","steps":[{"title":"작성"},{"title":"실행"}]}}`,
	})
	if !keepPlan || planReq.isVerbose {
		t.Fatalf("expected plan request visible in compact mode")
	}
	plainPlan := stripANSI(planReq.line)
	if !strings.Contains(plainPlan, "C 파일 만들고 실행") || !strings.Contains(plainPlan, "(2 steps)") || strings.Contains(plainPlan, `{"goal"`) {
		t.Fatalf("expected clean plan summary, got %q", plainPlan)
	}

	planRes, _ := classifyLedgerItem(taskstate.RawTurnEvent{
		Name: "tool.plan.result",
		Body: `{"output":{"content":"{\"goal\":\"C 파일\",\"steps\":[{\"title\":\"작성\"},{\"title\":\"실행\"}]}"}}`,
	})
	plainPlanRes := stripANSI(planRes.line)
	if !strings.Contains(plainPlanRes, "1. 작성") || !strings.Contains(plainPlanRes, "2. 실행") {
		t.Fatalf("expected clean plan step list, got %q", plainPlanRes)
	}

	for _, eventName := range []string{
		agentcontract.TaskEventAgentPlanUpdated,
		agentcontract.TaskEventAgentFailureDebtCreated,
		agentcontract.TaskEventAgentRecoveryGuidance,
	} {
		item, keep := classifyLedgerItem(taskstate.RawTurnEvent{Name: eventName, Body: `{}`})
		if !keep || !item.isVerbose {
			t.Fatalf("expected %s to be verbose-only in compact mode", eventName)
		}
	}

	recoverItem, keepRecover := classifyLedgerItem(taskstate.RawTurnEvent{
		Name: agentcontract.TaskEventAgentRecoveryAttempt,
		Body: `{"tool":"bash","debt":{"latestFailure":{"tool":"write"}}}`,
	})
	if !keepRecover || recoverItem.isVerbose || !strings.Contains(stripANSI(recoverItem.line), "recovering from write via bash") {
		t.Fatalf("expected clean compact recovery line, got %+v (%q)", recoverItem, stripANSI(recoverItem.line))
	}
}

func TestMouseClicksAndFocusEventsNeverEmitEscape(t *testing.T) {
	sequence := "\x1b[<0;20;10M\x1b[<0;20;10m\x1b[I\x1b[O\x1b[99;5u\x1b[99~"
	reader := newKeyReader(strings.NewReader(sequence))
	for index := 0; index < 6; index++ {
		event, errorValue := reader.readKey()
		if errorValue != nil {
			t.Fatalf("event %d failed: %v", index, errorValue)
		}
		if event.kind != keyIgnore {
			t.Fatalf("event %d: got %v, want keyIgnore (must never masquerade as keyEscape)", index, event.kind)
		}
	}

	standaloneEscape, escapeError := newKeyReader(strings.NewReader("\x1b")).readKey()
	if escapeError != nil || standaloneEscape.kind != keyEscape {
		t.Fatalf("expected standalone ESC to emit keyEscape, got %+v (%v)", standaloneEscape, escapeError)
	}
}

func TestHandleTurnOutcomeMarksSuppressedRunningTurnAsCancelled(t *testing.T) {
	session := &conversationSession{options: runOptions{modelName: "gpt-6.2-sol"}}
	app := newTUIApp(session, &bytes.Buffer{}, 90, 24)
	turn := &tuiTurnEntry{index: 1, prompt: "investigate", isRunning: true, startedAt: time.Now()}
	app.activeTurn = turn
	app.items = append(app.items, tuiTranscriptItem{turn: turn})

	app.handleTurnOutcome(tuiTurnOutcome{
		result: agentcontract.AgentTurnResult{
			TaskRun:                agentcontract.TaskRun{Status: agentcontract.TaskStatusRunning},
			ReplySuppressed:        true,
			ReplySuppressionReason: "the model call was cancelled: context canceled",
		},
	})
	if turn.status != agentcontract.TaskStatusCancelled {
		t.Fatalf("expected cancelled status, got %q", turn.status)
	}
	if turn.reply != "" || !strings.Contains(turn.failureReason, "cancelled") {
		t.Fatalf("expected empty reply with cancellation reason, got reply=%q reason=%q", turn.reply, turn.failureReason)
	}
}
