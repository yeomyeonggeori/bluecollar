package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestCompletionReplyPromptUsesOriginalInstructionForContinuation(t *testing.T) {
	prompt := buildCompletionReplyPrompt(AgentTurnRequest{
		Prompt: "승인",
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "고객지원 보고서를 JSON으로 만들어 이 DM에 첨부해줘.",
		},
	}, nil)

	if !strings.Contains(prompt, "고객지원 보고서를 JSON으로 만들어 이 DM에 첨부해줘.") {
		t.Fatalf("expected original instruction in completion prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "Original request:\n승인") {
		t.Fatalf("continuation prompt must not replace the original instruction: %q", prompt)
	}
}

func TestCompletionGateRejectsSatisfiedFinishWithUnresolvedFailureDebt(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionFacts(
		AgentTurnRequest{},
		[]turnObservation{
			newFailureObservation("obs-001", "continue", "file_read", "permission denied", toolcontract.FailurePermissionDenied, toolcontract.FailureCodes.AccessDenied, "file_read"),
		},
		turnActionDocument{
			Action:             "finish",
			Message:            "버튼 기능을 직접 구현할 수 있는 상태가 아닙니다.",
			FailureResolution:  failureResolutionNoToolFallback,
			GoalStatus:         "satisfied",
			GoalSatisfied:      &goalSatisfied,
			HasRemainingWork:   true,
			CompletionEvidence: []completionEvidenceReference{},
			QualityReview:      []qualityReviewItem{},
		},
	)
	if result.IsSatisfied {
		t.Fatal("expected completion gate to reject unresolved failure debt")
	}
	if !strings.Contains(result.Message, "hasRemainingWork") {
		t.Fatalf("expected remaining work guidance, got %q", result.Message)
	}
}

func TestCompletionGateAcceptsZeroRemainingWork(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionFacts(AgentTurnRequest{}, nil, turnActionDocument{
		Action:             "finish",
		Message:            "작업을 완료했습니다.",
		GoalStatus:         "satisfied",
		GoalSatisfied:      &goalSatisfied,
		HasRemainingWork:   false,
		CompletionEvidence: []completionEvidenceReference{},
		QualityReview:      []qualityReviewItem{},
	})
	if !result.IsSatisfied {
		t.Fatalf("expected zero remaining work to satisfy completion gate, got %q", result.Message)
	}
}

func TestCompletionGateRejectsEvidenceThatMissesDeclaredResultCondition(t *testing.T) {
	goalSatisfied := true
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:         "artifact_review",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		ResultContract: &toolcontract.ToolResultContract{
			Schema: json.RawMessage(`{"type":"object","properties":{"passed":{"type":"boolean"}},"required":["passed"],"additionalProperties":false}`),
			EvidenceCondition: &toolcontract.EvidenceCondition{
				ResultField: "passed",
				Equals:      json.RawMessage(`true`),
			},
		},
	}})
	observation := turnObservation{
		ObservationID: "obs-001",
		Tool:          "artifact_review",
		Output:        toolcontract.ToolOutput{Data: json.RawMessage(`{"passed":false}`)},
	}

	result := validateCompletionFacts(AgentTurnRequest{ToolSet: toolSet}, []turnObservation{observation}, turnActionDocument{
		Action:           "finish",
		Message:          "검토했습니다.",
		GoalStatus:       "satisfied",
		GoalSatisfied:    &goalSatisfied,
		HasRemainingWork: false,
		CompletionEvidence: []completionEvidenceReference{{
			ObservationID: "obs-001",
			ToolName:      "artifact_review",
		}},
	})

	if result.IsSatisfied || result.EvidenceKind != evidenceKindReference {
		t.Fatalf("expected failed review verdict to be rejected as completion evidence, got %+v", result)
	}
	if observation.Failed() {
		t.Fatal("expected review issues to remain available to the model as successful tool output")
	}
}

func TestAgentTurnRunnerAcceptsHtmlRequestWithHtmlAttachment(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"deck.html"}}`,
		finishMessageCiting("HTML 파일을 전달해 드립니다.", "obs-001"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
				SizeBytes:  12,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:          "person-1",
		ConversationID:             "conversation-1",
		Prompt:                     "html만 주면 돼",
		ToolSet:                    toolRegistry,
		PinnedToolNames:            toolRegistry.ListToolNames(),
		RequiredEvidenceTools:      []string{"file_deliver"},
		RequiredAttachmentSuffixes: []string{".html"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to finish: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID))
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestValidateCompletionEvidenceDoesNotDeliverImageReadAttachment(t *testing.T) {
	attachmentIndex := 0
	attachments, errorValue := validateCompletionEvidence(nil, []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "image_read",
		Attachments: []toolcontract.FileAttachment{{
			DevicePath:    "/workspace/inbox/mascot.png",
			Filename:      "mascot.png",
			ContentType:   "image/png",
			ContentBase64: "aW1hZ2U=",
		}},
	}}, []completionEvidenceReference{{
		ObservationID:   "obs-001",
		ToolName:        "image_read",
		AttachmentIndex: &attachmentIndex,
	}})
	if errorValue != nil {
		t.Fatalf("expected image_read evidence to validate: %v", errorValue)
	}
	if len(attachments) != 0 {
		t.Fatalf("expected image_read evidence to produce no delivery attachments, got %+v", attachments)
	}
}

func TestAgentTurnRunnerDoesNotRequireNonAttachmentToolInCompletionEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"deck.html"}}`,
		finishMessageCiting("HTML 파일을 첨부했습니다: deck.html", "obs-002"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{"write", "file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "write"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`{"path":"tmp/deck/presentation.md","sizeBytes":6}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "html 만들어줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"write", "file_deliver"},
	})
	if errorValue != nil {
		t.Fatalf("expected required evidence to recover: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.html" {
		t.Fatalf("expected html attachment, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerAcceptsReadableFileAttachObservation(t *testing.T) {
	workspaceRootPath := t.TempDir()
	artifactDirectoryPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "artifacts", "deck")
	if errorValue := os.MkdirAll(artifactDirectoryPath, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "presentation.md"), "Hermes Agent 장단점 분석")
	writeAgentTestFile(t, filepath.Join(artifactDirectoryPath, "deck.html"), "<html><body>Hermes Agent 장단점 분석</body></html>")
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"file_deliver","toolInput":{"path":"artifacts/deck/deck.html"}}`,
		finishMessageCiting("deck.html 파일을 첨부했습니다.", "obs-001"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 3})
	toolRegistry := newTestToolSet([]string{"file_deliver"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath: "artifacts/deck/deck.html",
				Filename:   "deck.html",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "html 만들어줘",
		WorkspaceRootPath:     workspaceRootPath,
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"file_deliver"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed turn without runner error: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
		t.Fatalf("expected completed task, got %s events=%+v", result.TaskRun.Status, events)
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("expected readable attachment to be delivered, got %+v", result.Attachments)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.artifact_attach_rejected", "deck intent manifest is missing") {
		t.Fatal("did not expect intent manifest rejection event")
	}
}

func TestAgentTurnRunnerRejectsUnsatisfiedFinishMessage(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"reply","final":true,"message":"done","goalStatus":"in_progress","goalSatisfied":false,"completionEvidenceIDs":[]}`,
		finishMessageDocument("now done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "say hello",
	})
	if errorValue != nil {
		t.Fatalf("expected turn to recover: %v", errorValue)
	}
	if result.FinishMessage != "now done" {
		t.Fatalf("expected recovered final reply, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) < 2 {
		t.Fatalf("expected structured retry request after finish rejection, got %d requests", len(languageModel.requests))
	}
	if !messagesContain(languageModel.requests[1].Messages, "a final reply requires goalSatisfied=true") {
		t.Fatalf("expected retry request to include the final reply rejection reason, got %+v", languageModel.requests[1].Messages)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "goalSatisfied=true") {
		t.Fatal("expected goalSatisfied completion gate event")
	}
}

func TestAgentTurnRunnerRejectsCompletionEvidenceFromErrorObservation(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"unstable","toolInput":{}}`,
		finishMessageCiting("done", "obs-001"),
		failureReportDocument("tool failed", "unstable", "{}", toolcontract.FailureCodes.OperationFailed.String(), "unstable", "failed"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryBudget: exhaustedRecoveryBudgetForTest()})
	toolRegistry := newTestCapabilityToolSet([]string{"unstable"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "unstable"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "unstable", "failed"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "do it",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to fail safely: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusFailed {
		t.Fatalf("expected failed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "not a successful observation") {
		t.Fatal("expected failed evidence gate event")
	}
}

func TestAgentTurnRunnerNoToolFallbackWaivesFailedRequiredEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule_list","toolInput":{"range":"today"}}`,
		noToolFallbackFinishMessageDocument("Nothing in today's conversation mentioned a scheduled task."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolRegistry := newTestCapabilityToolSet([]string{"schedule_list"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "schedule_list"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return structuredFailureToolResult("schedule storage unavailable", "schedule storage unavailable", "schedule_lookup_failed", "schedule_lookup", false, false), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "what is scheduled for today?",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"schedule_list"},
	})
	if errorValue != nil {
		t.Fatalf("expected no-tool fallback to complete: %v", errorValue)
	}
	if result.FinishMessage != "Nothing in today's conversation mentioned a scheduled task." {
		t.Fatalf("expected direct fallback answer, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.schedule_list.result", toolcontract.FailureCodes.OperationFailed.String()) {
		t.Fatal("expected internal tool failure event to remain recorded")
	}
}

func TestAgentTurnRunnerRemovesQualityCriteriaActionAfterCriteriaAreSet(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":["done once: criteria are declared"],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"alpha","toolInput":{}}`,
		`{"action":"reply","final":true,"message":"done","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002"],"qualityReview":[{"id":"done-once-criteria-are-declared","passed":true,"evidenceIDs":["obs-002"]}]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"alpha"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "alpha"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess("alpha result"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "make an artifact",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		OutcomeContract:   OutcomeContract{ArtifactRequirement: ArtifactRequirementPreferred},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.FinishMessage != "done" {
		t.Fatalf("expected final reply, got %q", result.FinishMessage)
	}
	if len(languageModel.requests) < 2 {
		t.Fatalf("expected at least two model requests, got %d", len(languageModel.requests))
	}
	if !strings.Contains(languageModel.requests[0].StructuredOutputSchema.Document, "set_quality_criteria") {
		t.Fatalf("expected initial schema to allow quality criteria, got %s", languageModel.requests[0].StructuredOutputSchema.Document)
	}
	if strings.Contains(languageModel.requests[1].StructuredOutputSchema.Document, "set_quality_criteria") {
		t.Fatalf("expected next schema to remove quality criteria, got %s", languageModel.requests[1].StructuredOutputSchema.Document)
	}
}

func TestAgentTurnRunnerDoesNotBlockFinishedExpectedResultForMissingQualityReview(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"set_quality_criteria","qualityCriteria":["visual review: review the artifact"],"goalStatus":"in_progress","goalSatisfied":false}`,
		`{"action":"continue","toolName":"site_serve","toolInput":{"siteID":"site-1"},"nextStepPlan":{"objective":"finish with the public URL","expectedTools":[],"expectedNextResults":["public URL"],"doneCriteria":["public URL is available"],"risk":"none","workingSetReason":"publish satisfies the link expected result"}}`,
		`{"action":"reply","final":true,"message":"배포했습니다: https://portfolio.example","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002"]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5})
	toolRegistry := newTestCapabilityToolSet([]string{"site_serve"})
	registerTestTool(toolRegistry, canonicalLinkToolDefinition("site_serve"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return canonicalLinkToolResult("https://portfolio.example"), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "사이트를 배포해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		OutcomeContract: OutcomeContract{ExpectedResults: []ExpectedResult{{
			ID:          "site-public-link",
			Type:        ExpectedResultTypeLink,
			Description: "사용자가 열 수 있는 public URL의 웹사이트",
			Required:    true,
		}}},
	})
	if errorValue != nil {
		t.Fatalf("expected finish to pass without qualityReview hard gate: %v", errorValue)
	}
	if result.FinishMessage != "배포했습니다: https://portfolio.example" {
		t.Fatalf("expected final publish message, got %q", result.FinishMessage)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.completion_required", "qualityReview") {
		t.Fatal("expected missing qualityReview to remain a review hint, not a completion blocker")
	}
}

func TestCompletionGateUsesAttachmentsFromCompletionEvidence(t *testing.T) {
	goalSatisfied := true
	observation := newContentObservation("obs-001", "continue", toolcontract.FileDeliverToolName, "file attached")
	observation.Attachments = []toolcontract.FileAttachment{{
		DevicePath:  "/workspace/private/people/person-1/report.json",
		Filename:    "report.json",
		ContentType: "application/json",
	}}
	result := validateCompletionFacts(AgentTurnRequest{
		ToolSet: newTestToolSet([]string{toolcontract.FileDeliverToolName}),
	}, []turnObservation{observation}, turnActionDocument{
		Action:        "finish",
		Message:       "JSON 파일을 첨부했습니다.",
		GoalStatus:    "satisfied",
		GoalSatisfied: &goalSatisfied,
		CompletionEvidence: []completionEvidenceReference{{
			ObservationID: "obs-001",
			ToolName:      toolcontract.FileDeliverToolName,
		}},
	})

	if !result.IsSatisfied || len(result.Attachments) != 1 {
		t.Fatalf("expected completion evidence attachment to satisfy verification, got %+v", result)
	}
}

func TestAgentTurnRunnerExpectedResultsRequireTheirTypedToolEvidence(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"site_serve","toolInput":{"siteID":"site-1","message":"Publish"},"nextStepPlan":{"objective":"finish with public URL","expectedTools":[],"expectedNextResults":["public URL exists"],"doneCriteria":["public URL exists"],"risk":"none","workingSetReason":"publish should satisfy the expected result"}}`,
			`{"action":"reply","final":true,"message":"배포했습니다: https://portfolio.example","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestCapabilityToolSet([]string{"site_serve"})
	registerTestTool(toolRegistry, canonicalLinkToolDefinition("site_serve"), func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return canonicalLinkToolResult("https://portfolio.example"), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.NotFound, "test_tool", "tool is not registered"), nil
	})
	toolRegistry = toolRegistry.WithAdditionalAllowedToolNames([]string{toolcontract.FileDeliverToolName})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "개인 홈페이지 배포해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"site_serve"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"site_serve"},
			ExpectedResults: []ExpectedResult{{
				ID:          "site-public-link",
				Type:        ExpectedResultTypeLink,
				Description: "사용자가 열 수 있는 public URL의 개인 홈페이지",
				Required:    true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected run to complete: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if strings.Contains(result.FinishMessage, "첨부") {
		t.Fatalf("expected link result, got %q", result.FinishMessage)
	}
}

func TestAgentTurnRunnerResolvesFinalReplyAttachmentBehindTheReply(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		contents: []string{
			`{"action":"continue","toolName":"file.promote","toolInput":{"path":"tmp/deck/build/deck.pptx","destinationDirectoryPath":"artifacts/deck","overwrite":true},"nextStepPlan":{"objective":"attach promoted file","expectedTools":["file_deliver"],"expectedNextResults":["attached pptx"],"doneCriteria":["file attached"],"risk":"none","workingSetReason":"file deliverable requires attachment"}}`,
			`{"action":"reply","final":true,"message":"PPTX를 첨부했습니다.","attachments":[{"path":"artifacts/deck/deck.pptx","filename":"deck.pptx"}],"goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6})
	toolRegistry := newTestCapabilityToolSet([]string{"file.promote"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file.promote"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`{"path":"artifacts/deck/deck.pptx"}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_deliver", Visibility: toolcontract.ToolVisibilityInternal}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "file attached"},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath:  "/tmp/deck.pptx",
				Filename:    "deck.pptx",
				ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "PPTX 파일로 첨부해줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
		OutcomeContract: OutcomeContract{
			ArtifactRequirement:        ArtifactRequirementRequired,
			RequiredAttachmentSuffixes: []string{".pptx"},
			ExpectedResults: []ExpectedResult{{
				ID:          "attached-file",
				Type:        ExpectedResultTypeFile,
				Description: "수정 가능한 PPTX 파일 한 개",
				Required:    true,
			}},
		},
	})
	if errorValue != nil {
		t.Fatalf("expected run to complete after attachment: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "tool.file_deliver.requested", "deck.pptx") {
		t.Fatal("expected the final reply attachment to be resolved behind the reply")
	}
	if len(result.Attachments) != 1 || result.Attachments[0].Filename != "deck.pptx" {
		t.Fatalf("expected the delivered attachment to reach the result, got %+v", result.Attachments)
	}
}

func TestAgentTurnRunnerFinalizesOneShotEvidenceToolAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"calendar_add","toolInput":{"title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}}`,
		finishMessageCiting("휴가 일정을 등록했습니다.", "obs-001"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"calendar_add"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "calendar_add"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"id":"event-1","title":"휴가","startISO":"2026-05-10T00:00:00+09:00","endISO":"2026-05-13T00:00:00+09:00","timeZone":"Asia/Seoul","isAllDay":true}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "내일부터 화요일까지 휴가 등록해줘",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"calendar_add"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed calendar turn: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one calendar write, got %d", toolCallCount)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected a final model reply after evidence success, got %d requests", len(languageModel.requests))
	}
	if result.FinishMessage != "휴가 일정을 등록했습니다." {
		t.Fatalf("expected model-authored finish reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.action", "finish") {
		t.Fatal("expected model finish action after calendar evidence")
	}
}

func TestAgentTurnRunnerFinalizesScheduleCreateAfterSuccess(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"schedule_create","toolInput":{"taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"repeatPolicy":"finite","timeZone":"Asia/Seoul"}}`,
		finishMessageCiting("반복 일정을 만들었습니다.", "obs-001"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4, MaxToolCallCount: 4})
	toolCallCount := 0
	toolRegistry := newTestCapabilityToolSet([]string{"schedule_create"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "schedule_create"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		toolCallCount++
		return testToolSuccess(`{"taskScheduleID":"schedule-1","taskInstruction":"현재 대화에 \"죄송합니다\"라고 보낸다.","kind":"interval","intervalSecond":60,"maxRunCount":10,"nextRunAt":"2026-05-09T05:07:00Z"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "1분에 한 번씩 나한테 죄송합니다 10번 해봐",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"schedule_create"},
	})
	if errorValue != nil {
		t.Fatalf("expected completed schedule turn: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected one schedule create, got %d", toolCallCount)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected a final model reply after schedule success, got %d requests", len(languageModel.requests))
	}
	if result.FinishMessage != "반복 일정을 만들었습니다." {
		t.Fatalf("expected model-authored finish reply, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.action", "finish") {
		t.Fatal("expected model finish action after schedule evidence")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"bash","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"bash","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"bash", "write"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "bash"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "bash", `{"exitCode":1,"stdout":"","stderr":"Error: presentation.md not found. Create presentation.md or set SRC=yourfile.md\n","timedOut":false}`), nil
		}
		return testToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "write"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return testToolSuccess(`{"path":"` + input.Path + `","sizeBytes":5}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build deck",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "presentation.md") {
		t.Fatal("did not expect terminal precondition block event")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalRerunForMissingDesignFile(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"bash","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"write","toolInput":{"path":"tmp/deck/DESIGN.md","content":"colors: blue"}}`,
		`{"action":"continue","toolName":"bash","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		finishMessageDocument("done"),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 6, MaxToolCallCount: 6})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"bash", "write"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "bash"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		terminalCallCount++
		if terminalCallCount == 1 {
			return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "bash", `{"exitCode":1,"stdout":"","stderr":"DESIGN.md is missing colors:\n","timedOut":false}`), nil
		}
		return testToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "write"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return testToolSuccess(`{"path":"` + input.Path + `","sizeBytes":12}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "build deck",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected terminal rerun to remain available, got %d terminal calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "DESIGN.md") {
		t.Fatal("did not expect DESIGN.md precondition block event")
	}
}

func TestAgentTurnRunnerDoesNotBlockTerminalBeforeRequiredFileWrite(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"bash","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"continue","toolName":"write","toolInput":{"path":"tmp/deck/presentation.md","content":"# Deck"}}`,
		`{"action":"continue","toolName":"bash","toolInput":{"command":"NAME=deck ./build.sh"}}`,
		`{"action":"reply","final":true,"message":"done","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-002"]}`,
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 5, MaxToolCallCount: 5})
	terminalCallCount := 0
	toolRegistry := newTestToolSet([]string{"bash", "write"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "bash"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		terminalCallCount++
		return testToolSuccess(`{"exitCode":0,"stdout":"built","stderr":"","timedOut":false}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "write"}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "tool", errorValue.Error()), nil
		}
		return testToolSuccess(`{"path":"` + input.Path + `","sizeBytes":5}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "build deck",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"write"},
	})
	if errorValue != nil {
		t.Fatalf("expected turn to succeed: %v", errorValue)
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected completed task, got %s", result.TaskRun.Status)
	}
	if terminalCallCount != 2 {
		t.Fatalf("expected rebuild after write to run instead of duplicate rejection, got %d calls", terminalCallCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.tool_precondition_blocked", "first required workspace file") {
		t.Fatal("did not expect required write precondition block event")
	}
}

func TestCompletionGateObservationStatesZeroToolRealityForFirstTurnFinish(t *testing.T) {
	result := completionGateResult{Message: "completionEvidence references an unknown observation", EvidenceKind: "evidence_reference_invalid"}
	observation := completionGateObservation(1, result, nil, nil)
	if !strings.Contains(observation.ContentText(), "ZERO successful tool observations") {
		t.Fatalf("expected recorded-reality statement, got: %s", observation.ContentText())
	}
	successfulObservation := turnObservation{ObservationID: "obs-001", Action: "tool", Tool: "task_add"}
	observationAfterTool := completionGateObservation(2, result, nil, []turnObservation{successfulObservation})
	if strings.Contains(observationAfterTool.ContentText(), "ZERO successful tool observations") {
		t.Fatalf("did not expect recorded-reality statement after a successful tool observation")
	}
}

func TestFinishHiddenAfterEvidenceMissingRejectionWithoutToolEvidence(t *testing.T) {
	rejection := completionGateObservation(1, completionGateResult{Message: "no evidence", EvidenceKind: "evidence_reference_invalid"}, nil, nil)
	if !finishWasRejectedWithoutAnyToolEvidence([]turnObservation{rejection}) {
		t.Fatalf("expected finish hidden after gate rejection with zero tool evidence")
	}
	successfulTool := turnObservation{ObservationID: "obs-002", Action: "continue", Tool: "task_add"}
	if finishWasRejectedWithoutAnyToolEvidence([]turnObservation{successfulTool, rejection}) {
		t.Fatalf("expected finish exposed once a successful tool observation exists")
	}
	if finishWasRejectedWithoutAnyToolEvidence([]turnObservation{rejection, successfulTool}) {
		t.Fatalf("expected finish exposed when the latest observation is not a gate rejection")
	}
	if finishWasRejectedWithoutAnyToolEvidence(nil) {
		t.Fatalf("expected finish exposed with no observations")
	}
}

func TestASecondRefusalTheAgentDidNothingAboutWithdrawsFinish(t *testing.T) {
	successfulSend := turnObservation{ObservationID: "obs-001", Action: "continue", Tool: "bash"}
	refusal := func(index int) turnObservation {
		return completionGateObservation(index, completionGateResult{Message: "the condition was never evaluated", EvidenceKind: evidenceKindExpectedResult}, nil, []turnObservation{successfulSend})
	}

	if finishKeepsBeingRefusedWithNothingDoneBetween([]turnObservation{successfulSend, refusal(2)}) {
		t.Fatal("one refusal is the gate telling the agent what to go do, and taking finish away there answers it before the agent has")
	}
	if !finishKeepsBeingRefusedWithNothingDoneBetween([]turnObservation{successfulSend, refusal(2), refusal(3)}) {
		t.Fatal("a refusal the agent answered with the same finish has to stop being answerable that way")
	}
	if finishKeepsBeingRefusedWithNothingDoneBetween([]turnObservation{refusal(1), refusal(2), successfulSend}) {
		t.Fatal("an agent that went and did something has finish back")
	}
}

func TestFinishHiddenAfterAttachmentRejectionDespiteToolEvidence(t *testing.T) {
	successfulRead := turnObservation{ObservationID: "obs-001", Action: "continue", Tool: "file_read"}
	rejection := completionGateObservation(2, completionGateResult{Message: "attach the artifact", EvidenceKind: "attachment_missing"}, nil, []turnObservation{successfulRead})
	if !finishWasRejectedWithoutAnyToolEvidence([]turnObservation{successfulRead, rejection}) {
		t.Fatalf("expected finish hidden after attachment rejection even with prior tool evidence")
	}
}

func TestARequiredFileResultIsNotRequiredWhenNothingCanDeliverIt(t *testing.T) {
	toolSet := newTestToolSet([]string{toolcontract.BashToolName})
	contract := OutcomeContract{ExpectedResults: []ExpectedResult{{Type: ExpectedResultTypeFile, Required: true}}}

	reduced := contractReducedToCallableTools(toolSet, contract)

	if expectedResultRequiresFileAttachment(reduced) {
		t.Fatal("a required file on a task that holds only a terminal is a demand no turn can meet, and the run spends every remaining turn on it")
	}
}

func TestEveryCopyOfTheContractIsReducedToWhatTheTaskCanCall(t *testing.T) {
	toolSet := newTestToolSet([]string{toolcontract.BashToolName})
	undeliverable := OutcomeContract{
		RequiredEvidenceTools:      []string{toolcontract.FileDeliverToolName},
		RequiredAttachmentSuffixes: []string{".txt"},
		ExpectedResults:            []ExpectedResult{{Type: ExpectedResultTypeFile, Required: true}},
	}
	request := AgentTurnRequest{
		ToolSet:               toolSet,
		OutcomeContract:       undeliverable,
		ActiveGoal:            ActiveGoal{OutcomeContract: undeliverable},
		RequiredEvidenceTools: []string{toolcontract.FileDeliverToolName},
	}

	request.OutcomeContract = contractReducedToCallableTools(request.ToolSet, request.OutcomeContract)
	request.ActiveGoal.OutcomeContract = contractReducedToCallableTools(request.ToolSet, request.ActiveGoal.OutcomeContract)
	request.RequiredEvidenceTools = callableToolNames(request.ToolSet, request.RequiredEvidenceTools)

	if expectedResultRequiresFileAttachment(request.ActiveGoal.OutcomeContract) {
		t.Fatal("the goal carries its own copy of the contract, and reducing only the request's copy left the demand alive where the gates actually read it")
	}
	if len(request.RequiredEvidenceTools) != 0 {
		t.Fatalf("the request carries a third copy as a flat list, got %v", request.RequiredEvidenceTools)
	}
}

func TestARejectedCitationNamesTheOnesThatWouldHaveDone(t *testing.T) {
	observations := []turnObservation{
		{ObservationID: "obs-001", Action: "continue", Tool: "bash", Summary: "listed the workspace"},
		{ObservationID: "obs-002", Action: "continue", Tool: "bash", Summary: "wrote avg_temp.txt"},
	}

	errorValue := validateCompletionEvidenceReferences(nil, observations, []completionEvidenceReference{{ObservationID: "obs-009"}})

	if errorValue == nil {
		t.Fatal("citing an observation that does not exist has to fail")
	}
	message := errorValue.Error()
	for _, expected := range []string{"obs-009", "obs-001", "obs-002", "ledger"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("an agent cannot correct a citation without the bad one, the good ones, and where to read what they did; %q missing from %q", expected, message)
		}
	}
	if strings.Contains(message, "listed the workspace") {
		t.Fatalf("repeating each summary here put a plan document into the message eight times over and cost 8x the prompt: %q", message)
	}
}

func TestCitingAnObservationTheTaskNeverMadeIsNotEvidence(t *testing.T) {
	toolSet := newTestToolSet([]string{toolcontract.BashToolName})
	observations := []turnObservation{
		newContentObservation("obs-001", "continue", toolcontract.BashToolName, "ok"),
	}
	citesNothingReal := []completionEvidenceReference{{ObservationID: "obs-999"}}

	if _, errorValue := validateCompletionEvidence(toolSet, observations, citesNothingReal); errorValue == nil {
		t.Error("a finish citing an observation the task never made is not evidence")
	}
}

func TestARefusalNamesWhatAlreadyChangedSomething(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:            "money_send",
		InputSchema:     json.RawMessage(`{"type":"object","additionalProperties":false}`),
		SideEffectClass: toolcontract.ToolSideEffectExternalSend,
	}})
	sent := turnObservation{ObservationID: "obs-004", Action: "continue", Tool: "money_send"}
	result := completionGateResult{Message: "the condition was never evaluated", EvidenceKind: evidenceKindExpectedResult}

	refusal := completionGateObservation(5, result, toolSet, []turnObservation{sent})

	content := refusal.ContentText()
	if !strings.Contains(content, "obs-004 money_send") {
		t.Fatalf("an agent told only that its evidence is missing does the work again, and the second send is not undone by the refusal: %s", content)
	}
	if !strings.Contains(content, "Repeating one of them makes the change twice") {
		t.Fatalf("naming the call is not enough without saying what repeating it costs: %s", content)
	}
}

func TestARefusalWithNothingChangedNamesNothing(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:            toolcontract.BashToolName,
		InputSchema:     json.RawMessage(`{"type":"object","additionalProperties":false}`),
		SideEffectClass: toolcontract.ToolSideEffectRead,
	}})
	read := turnObservation{ObservationID: "obs-002", Action: "continue", Tool: toolcontract.BashToolName}
	result := completionGateResult{Message: "the condition was never evaluated", EvidenceKind: evidenceKindExpectedResult}

	refusal := completionGateObservation(5, result, toolSet, []turnObservation{read})

	if strings.Contains(refusal.ContentText(), "already changed something") {
		t.Fatalf("a shell read changed nothing and warning about repeating it argues against the work the gate is asking for: %s", refusal.ContentText())
	}
}

func TestDecliningToClaimSuccessPutsTheExitOnTheMenu(t *testing.T) {
	goalNotSatisfied := false
	result := validateCompletionFacts(AgentTurnRequest{}, nil, turnActionDocument{Action: "finish", GoalSatisfied: &goalNotSatisfied})
	refusal := completionGateObservation(2, result, nil, nil)

	if !shouldExposeFailAction(agentTaskState{Observations: []turnObservation{refusal}}) {
		t.Fatal("an agent that has decided it cannot do the task, and said so in the typed field, is left with claiming it did as the only way to reply")
	}
}

func TestAFinishClaimingSuccessLeavesTheExitOffTheMenu(t *testing.T) {
	goalSatisfied := true
	result := validateCompletionFacts(AgentTurnRequest{}, nil, turnActionDocument{Action: "finish", GoalSatisfied: &goalSatisfied})
	refusal := completionGateObservation(2, result, nil, nil)

	if shouldExposeFailAction(agentTaskState{Observations: []turnObservation{refusal}}) {
		t.Fatal("a finish refused for missing evidence is a reason to do the work, not to give up on it")
	}
}
