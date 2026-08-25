package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func TestPromptAssemblerIncludesTemporalContext(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:         "내일 오후 6시 회식 추가해줘",
		TurnStartedAt:  time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC),
		EnvironmentNow: time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC),
	}, nil, "base", "")
	body := joinMessageContent(messages)

	for _, expected := range []string{
		"Runtime:",
		"Now: 2026-05-12 (Tue) 17:32 +09:00 Asia/Seoul",
		"This week: Mon 05-11, Tue 05-12, Wed 05-13, Thu 05-14, Fri 05-15, Sat 05-16, Sun 05-17",
		"Resolve relative dates",
		"내일",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected temporal context %q, got %s", expected, body)
		}
	}
}

func TestBuildTurnMessagesKeepsStablePrefixClockInvariant(t *testing.T) {
	baseRequest := AgentTurnRequest{
		Prompt:               "이번 주 회의 일정 알려줘",
		RequesterPersonID:    "person-1",
		RequesterName:        "김테스트",
		WorkspaceRootPath:    "/workspace",
		WorkspaceDefaultPath: "/workspace/private/people/person-1",
		ResponseLanguage:     "ko",
	}
	baseInstruction := buildAgentSystemInstruction(baseRequest, TurnOptions{}).Text()
	toolDescription := "Available tool catalog:\n- calendar_list: List calendar events."

	earlyRequest := baseRequest
	earlyRequest.TurnStartedAt = time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC)
	lateRequest := baseRequest
	lateRequest.TurnStartedAt = time.Date(2026, 5, 12, 21, 5, 59, 0, time.UTC)

	earlyMessages := (PromptAssembler{}).BuildTurnMessages(earlyRequest, nil, baseInstruction, toolDescription)
	lateMessages := (PromptAssembler{}).BuildTurnMessages(lateRequest, nil, baseInstruction, toolDescription)

	if len(earlyMessages) < 2 || len(lateMessages) < 2 {
		t.Fatalf("expected a base instruction message and a context message, got %d and %d", len(earlyMessages), len(lateMessages))
	}
	if earlyMessages[0].Content != lateMessages[0].Content {
		t.Fatalf("expected the base instruction message to be clock invariant")
	}

	if earlyMessages[1].Content != lateMessages[1].Content {
		t.Fatalf("a prompt cache keys on a byte-identical prefix, and this message changing with the clock costs full price on every call:\n%s\nvs\n%s", earlyMessages[1].Content, lateMessages[1].Content)
	}
	if !strings.Contains(earlyMessages[2].Content, "Runtime:") {
		t.Fatalf("the clock belongs after the prefix it would otherwise break, got %s", earlyMessages[2].Content)
	}
}

func TestBuildTurnMessagesPlacesVolatileContentAfterStablePrefix(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:            "사이트 만들어줘",
		TurnStartedAt:     time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC),
		StepBudgetContext: "Step budget:\nTool calls: 10/24 used, 14 remaining.",
	}
	messages := (PromptAssembler{}).BuildTurnMessages(request, nil, "base instruction", "Available tool catalog:\n- file_read: Read a file.")
	if len(messages) < 2 {
		t.Fatalf("expected a context message, got %d messages", len(messages))
	}
	unchangingContext := messages[1].Content
	changingContext := messages[2].Content

	if !strings.Contains(unchangingContext, "Available tool catalog") {
		t.Fatalf("the tool catalogue is the same on every call and belongs in the cacheable prefix, got %s", unchangingContext)
	}
	runtimeIndex := strings.Index(changingContext, "Runtime:")
	stepBudgetIndex := strings.Index(changingContext, "Step budget:")
	if runtimeIndex < 0 || stepBudgetIndex < 0 {
		t.Fatalf("expected runtime and step budget in the changing context, got %s", changingContext)
	}
	if runtimeIndex > stepBudgetIndex {
		t.Fatalf("expected the runtime clock before the step budget, got %s", changingContext)
	}
}

func TestBuildTemporalContextDescriptionAnchorsWeeksAcrossCalendarBoundaries(t *testing.T) {
	testCases := []struct {
		name      string
		startedAt time.Time
		expected  string
	}{
		{
			name:      "month boundary",
			startedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, defaultTurnLocation()),
			expected:  "This week: Mon 07-27, Tue 07-28, Wed 07-29, Thu 07-30, Fri 07-31, Sat 08-01, Sun 08-02",
		},
		{
			name:      "year boundary",
			startedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, defaultTurnLocation()),
			expected:  "This week: Mon 12-29, Tue 12-30, Wed 12-31, Thu 01-01, Fri 01-02, Sat 01-03, Sun 01-04",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			description := buildTemporalContextDescription(testCase.startedAt)
			if !strings.Contains(description, testCase.expected) {
				t.Fatalf("expected week anchors %q, got %s", testCase.expected, description)
			}
		})
	}
}

func TestPromptAssemblerIncludesStepBudgetContext(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:            "사이트 만들어줘",
		TurnStartedAt:     time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC),
		StepBudgetContext: "Step budget:\nTool calls: 10/24 used, 14 remaining.",
	}, nil, "base", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Step budget:") || !strings.Contains(body, "Tool calls: 10/24 used") {
		t.Fatalf("expected step budget context, got %s", body)
	}
}

func TestPromptAssemblerIncludesScheduledRunContext(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "오늘의 주요 일정, 날씨, 할 일을 브리핑한다.",
		ScheduledRun: ScheduledRunContext{
			ScheduleID:     "schedule-1",
			Kind:           "cron",
			Cadence:        "daily at 08:00 Asia/Seoul",
			CronExpression: "0 8 * * *",
			TimeZone:       "Asia/Seoul",
			OccurrenceAt:   "2026-06-16T08:00:00+09:00",
		},
	}, nil, "base", "")
	body := joinMessageContent(messages)

	for _, expected := range []string{
		"Scheduled run:",
		"Scheduled task instruction:",
		`"scheduleID":"schedule-1"`,
		`"kind":"cron"`,
		`"cadence":"daily at 08:00 Asia/Seoul"`,
		`"cronExpression":"0 8 * * *"`,
		`"occurrenceAt":"2026-06-16T08:00:00+09:00"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected scheduled run context %q, got %s", expected, body)
		}
	}
}

func TestPromptAssemblerIncludesInputImagePart(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "이거 뭔지 알아?",
		InputParts: []AgentPart{{
			Type: AgentPartTypeImage,
			Image: &AgentImagePart{
				MimeType:   "image/png",
				DataBase64: "aW1hZ2U=",
				Filename:   "mascot.png",
			},
			File: &AgentFilePart{
				Path:        "/workspace/private/people/person-1/inbox/mattermost/post-1/mascot.png",
				Filename:    "mascot.png",
				ContentType: "image/png",
			},
		}},
	}, nil, "base", "")

	if !messagesContainImagePart(messages) {
		t.Fatalf("expected input image part in LLM messages, got %+v", messages)
	}
	body := joinMessageContent(messages)
	if !strings.Contains(body, "Attached file:") || !strings.Contains(body, "mascot.png") {
		t.Fatalf("expected image file context text, got %s", body)
	}
}

func TestPromptAssemblerIncludesInputFileMarkdownPreview(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "요약해줘",
		InputParts: []AgentPart{{
			Type: AgentPartTypeFile,
			File: &AgentFilePart{
				Path:             "/workspace/private/people/person-1/inbox/mattermost/post-1/report.pdf",
				Filename:         "report.pdf",
				ContentType:      "application/pdf",
				MarkdownPreview:  "# 보고서\n\n핵심 내용",
				ConversionStatus: "converted",
			},
		}},
	}, nil, "base", "")

	body := joinMessageContent(messages)
	for _, expected := range []string{"report.pdf", "Markdown preview:", "# 보고서", "핵심 내용"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected file markdown preview %q, got %s", expected, body)
		}
	}
}

func TestPromptAssemblerIncludesKnownFileContextSnippet(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "file_read",
		Output:        toolcontract.ToolOutput{Content: `{"path":"tmp/source.ts","content":"export const title = \"Known\"","startLine":1,"endLine":1,"totalLines":1,"totalLinesKnown":true,"originalSizeBytes":28,"returnedBytes":28,"isTruncated":false}`},
	}}

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "continue",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	for _, expected := range []string{"Known file context", "tmp/source.ts", "export const title"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected known file context %q, got %s", expected, body)
		}
	}
}

func TestPromptAssemblerOmitsRawBrowserSnapshotOutput(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "browser_snapshot",
		Output:        toolcontract.ToolOutput{Content: `{"url":"https://example.com","title":"Example","snapshotText":"VISIBLE START ` + strings.Repeat("raw-page-text ", 500) + ` SECRET_COOKIE_VALUE","interactiveRefs":["@e1","@e2"],"profilePath":"/Users/me/Profile","cdpURL":"ws://localhost:9222"}`},
	}}

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "summarize the page",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	if strings.Contains(body, "SECRET_COOKIE_VALUE") || strings.Contains(body, "/Users/me/Profile") || strings.Contains(body, "ws://localhost:9222") {
		t.Fatalf("expected unsafe raw browser output to be omitted, got %s", body)
	}
	if !strings.Contains(body, "Progress ledger") || !strings.Contains(body, "obs-001") || !strings.Contains(body, "@e1") {
		t.Fatalf("expected compact progress with observation and refs, got %s", body)
	}
}

func TestPromptAssemblerIncludesRawToolResultSummary(t *testing.T) {
	fetchResult := `{"results":[{"url":"https://example.com","content":"Example Corp provides sample automation and sample analytics services."}]}`
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "web_fetch",
		Output:        toolcontract.ToolOutput{Content: fetchResult},
		Summary:       fetchResult,
	}}

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "make a deck",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Tool result context") || !strings.Contains(body, "Example Corp provides sample automation") {
		t.Fatalf("expected raw fetch summary in tool result context, got %s", body)
	}
}

func TestPromptAssemblerIncludesTurnDateContext(t *testing.T) {
	turnStartedAt := time.Date(2026, time.May, 8, 18, 1, 10, 0, time.UTC)
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:        "내일부터 화요일까지 휴가 등록해줘",
		TurnStartedAt: turnStartedAt,
	}, nil, "base", "")
	body := joinMessageContent(messages)

	if !strings.Contains(body, "Runtime:") || strings.Contains(body, "Now:") {
		t.Fatalf("an unknown clock says nothing: naming it at all is what sends the model to the shell's wrong one, got %s", body)
	}
}

func TestPromptAssemblerIncludesWritableWorkspaceContext(t *testing.T) {
	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt:               "pptx 만들어줘",
		RequesterPersonID:    "person-1",
		TurnStartedAt:        time.Date(2026, time.May, 8, 18, 1, 10, 0, time.UTC),
		WorkspaceRootPath:    "/workspace",
		WorkspaceDefaultPath: "/workspace/private/people/person-1",
		ResponseLanguage:     "ko",
	}, nil, "base", "")
	body := joinMessageContent(messages)

	for _, expected := range []string{
		"Terminal commands run as the requester POSIX identity",
		"~ is your Linux home ($HOME)",
		"concrete POSIX path under your home also resolves",
		"ls -ld .",
		"stat -c '%A %U %G %n' .",
		"test -w .",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected workspace context %q, got %s", expected, body)
		}
	}
	if !strings.Contains(body, "/workspace/private/people/person-1") {
		t.Fatalf("an agent that is not told where it stands spends its turns running pwd to find out, and the directory is its own, reachable in one command: %s", body)
	}
}

func TestPromptAssemblerDoesNotExposeAttachmentDevicePath(t *testing.T) {
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "browser_screenshot",
		Output:        toolcontract.ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/screen.png","filename":"screen.png","contentType":"image/png"}`},
		Summary:       "Screenshot captured. Use the imageRefs for visual inspection.",
		ImageRefs: []ToolResultImageRef{{
			ObservationID:   "obs-001",
			AttachmentIndex: 0,
			MimeType:        "image/png",
			Filename:        "screen.png",
		}},
		Attachments: []toolcontract.FileAttachment{{
			DevicePath:    "/tmp/internkim-companion-files/screen.png",
			Filename:      "screen.png",
			ContentType:   "image/png",
			SizeBytes:     123,
			ContentBase64: "aW1hZ2U=",
		}},
	}}

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "send the screenshot",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	if strings.Contains(body, "/tmp/internkim-companion-files/screen.png") || strings.Contains(body, "devicePath") {
		t.Fatalf("expected device path to stay out of prompt, got %s", body)
	}
	if !strings.Contains(body, `"attachmentIndex":0`) || !strings.Contains(body, `"filename":"screen.png"`) {
		t.Fatalf("expected attachment evidence reference, got %s", body)
	}
	if len(messages) == 0 || !messagesContainImagePart(messages) {
		t.Fatalf("expected image part to be attached to LLM messages, got %+v", messages)
	}
	if !messagesContainUserImagePart(messages) {
		t.Fatalf("expected tool result image part to be sent as a user message, got %+v", messages)
	}
}

func TestPromptAssemblerCompressesLongObservationHistory(t *testing.T) {
	observations := []turnObservation{}
	for index := 0; index < 20; index++ {
		observations = append(observations, turnObservation{
			ObservationID: "obs-" + strings.Repeat("0", 2) + string(rune('a'+index)),
			Action:        "continue",
			Tool:          "browser_snapshot",
			Output:        toolcontract.ToolOutput{Content: `{"snapshotText":"` + strings.Repeat("very-long-page-output ", 200) + `","interactiveRefs":["@old"]}`},
		})
	}
	observations = append(observations, turnObservation{
		ObservationID: "obs-latest",
		Action:        "continue",
		Tool:          "browser_snapshot",
		Output:        toolcontract.ToolOutput{Content: `{"snapshotText":"latest form","interactiveRefs":["@latestSearch","@latestButton"]}`},
	})

	messages := (PromptAssembler{}).BuildTurnMessages(AgentTurnRequest{
		Prompt: "continue",
	}, observations, "base", "")
	body := joinMessageContent(messages)

	if len(body) > 16000 {
		t.Fatalf("expected compact prompt, got %d bytes", len(body))
	}
	if strings.Contains(body, strings.Repeat("very-long-page-output ", 50)) {
		t.Fatalf("expected long raw output to be compressed, got %s", body)
	}
	if !strings.Contains(body, "@latestSearch") {
		t.Fatalf("expected latest interactive refs to remain, got %s", body)
	}
}

func joinMessageContent(messages []model.Message) string {
	parts := []string{}
	for _, message := range messages {
		parts = append(parts, message.Content)
		for _, messagePart := range message.Parts {
			if messagePart.Type == "text" {
				parts = append(parts, messagePart.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func messagesContainImagePart(messages []model.Message) bool {
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Type == "image" && part.MimeType == "image/png" && part.DataBase64 == "aW1hZ2U=" {
				return true
			}
		}
	}
	return false
}

func messagesContainUserImagePart(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		for _, part := range message.Parts {
			if part.Type == "image" && part.MimeType == "image/png" && part.DataBase64 == "aW1hZ2U=" {
				return true
			}
		}
	}
	return false
}

func TestWorkspaceContextCarriesTheHostsOwnSandboxDescription(t *testing.T) {
	body := buildWorkspaceContextDescription(AgentTurnRequest{
		WorkspaceDefaultPath: "/srv/agent/people/person-1",
		WorkspaceGuidance: []string{
			"Circle-shared files live under /srv/agent/circles/<circleID>.",
			"",
			"/srv/agent/.runtime is service-owned.",
		},
	})

	for _, expected := range []string{
		"Circle-shared files live under /srv/agent/circles/<circleID>.",
		"/srv/agent/.runtime is service-owned.",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected the host's sandbox description %q, got %s", expected, body)
		}
	}
	if strings.Contains(body, "\n\n") {
		t.Fatalf("expected empty guidance lines to be dropped, got %s", body)
	}
}

func TestATurnWithNoImagesIsNotToldToInspectThem(t *testing.T) {
	message := toolResultImageContextMessage([]turnObservation{{ObservationID: "obs-001", Action: "continue", Tool: "terminal_run"}})

	if strings.TrimSpace(message.Content) != "" || len(message.Parts) != 0 {
		t.Fatalf("a turn that produced no images carries an instruction about image parts that are not there: %q", message.Content)
	}
}

func TestTheProgressLedgerDoesNotClaimResultsItNoLongerCarries(t *testing.T) {
	observation := turnObservation{ObservationID: "obs-001", Action: "continue", Tool: "terminal_run", Summary: "ran and printed a page of output"}

	native := buildProgressContext(AgentTurnRequest{Prompt: "do it"}, []turnObservation{observation}, true)
	flattened := buildProgressContext(AgentTurnRequest{Prompt: "do it"}, []turnObservation{observation}, false)

	if strings.Contains(native, "source of truth") {
		t.Fatalf("with the results on their own calls this ledger is an index, and claiming otherwise sends the model here for them: %s", native)
	}
	if strings.Contains(native, `"summary":""`) {
		t.Fatalf("a stripped summary should not leave the key behind: %s", native)
	}
	if !strings.Contains(flattened, "source of truth") {
		t.Fatalf("the path that does carry the results still says so: %s", flattened)
	}
}
