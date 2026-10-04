package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const relocationNoticeRequest = "거래처에 보낼 사무실 이전 안내문을 pdf로 만들어 주세요.\n이전일: 2026년 11월 9일 (월)\n새 주소: 서울특별시 예시구 샘플로 12, 견본빌딩 7층"

func relocationNoticeObservations() []turnObservation {
	merge := newContentObservation("obs-011", "continue", toolcontract.BashToolName, `{"status":"ok","summary":"filled letter into 사무실 이전 안내문.pdf; no blank fields","outputPath":"사무실 이전 안내문.pdf"}`)
	merge.ToolInputKey = "bash\x00merge"
	delivery := newFailureObservation("obs-012", "continue", toolcontract.FileDeliverToolName, "actor.read_file failed for 사무실 이전 안내문.pdf as bc_person_person-1: /bin/bash: line 2: 사무실 이전 안내문.pdf: No such file or directory", toolcontract.FailureNotFound, toolcontract.FailureCodes.NotFound, "file_deliver")
	delivery.ToolInputKey = "file_deliver\x00사무실 이전 안내문.pdf"
	search := newContentObservation("obs-014", "continue", toolcontract.BashToolName, "./사무실 이전 안내문.pdf\n./사무실 이전 안내문.pdf.source.json")
	search.ToolInputKey = "bash\x00find"
	return []turnObservation{merge, delivery, search}
}

func finishClaimingSatisfied(message string) turnActionDocument {
	isSatisfied := true
	return turnActionDocument{Action: "finish", Message: message, GoalStatus: "satisfied", GoalSatisfied: &isSatisfied}
}

func TestAFinishAfterAFailedDeliveryWithNothingDeliveredSinceIsRefused(t *testing.T) {
	request := AgentTurnRequest{Prompt: relocationNoticeRequest}
	finish := finishClaimingSatisfied("파일이 워크스페이스에 존재함을 확인했으므로 전달을 재시도한다.")

	result := validateCompletionFacts(request, relocationNoticeObservations(), finish)

	if result.IsSatisfied {
		t.Fatal("expected a finish refused while the only delivery failed and nothing was delivered since")
	}
	if result.EvidenceKind != evidenceKindAttachment || !strings.Contains(result.Message, "obs-012") || !strings.Contains(result.Message, "No such file or directory") {
		t.Fatalf("expected the refusal to name the failed delivery and why it failed, got %q (%s)", result.Message, result.EvidenceKind)
	}
}

func TestADeliveryThatSucceedsAfterAFailedOneSettlesIt(t *testing.T) {
	request := AgentTurnRequest{Prompt: relocationNoticeRequest}
	devicePath := "/workspace/사무실 이전 안내문.pdf"
	redelivery := newContentObservation("obs-015", "continue", toolcontract.FileDeliverToolName, "files staged")
	redelivery.Attachments = []toolcontract.FileAttachment{{DevicePath: devicePath, Filename: "사무실 이전 안내문.pdf", ContentType: "application/pdf", SizeBytes: 40210}}
	observations := append(relocationNoticeObservations(), redelivery)

	result := validateCompletionFacts(request, observations, finishClaimingSatisfied("사무실 이전 안내문 PDF를 첨부했습니다."))

	if !result.IsSatisfied {
		t.Fatalf("expected a delivery after the failed one to settle it, got %q", result.Message)
	}
}

func TestAStopDoesNotCountAFailedDeliveryAsDone(t *testing.T) {
	services := newTurnRunnerTestServices(&refinishingLanguageModel{}, TurnOptions{MaxIterationCount: 8})
	request := AgentTurnRequest{Prompt: relocationNoticeRequest}
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)
	state := agentTaskState{CompletionIntentToolName: toolcontract.FileDeliverToolName, Observations: relocationNoticeObservations()}

	if services.runner.workIsDoneAtStop(context.Background(), taskRun.TaskRunID, request, state) {
		t.Fatal("expected a stop after a failed delivery with nothing delivered since not counted as done")
	}
}

const quoteRequest = "견적서를 pdf로 만들어 주세요.\n수신: 주식회사 샘플유통 박예시 과장님\n품목\n- 창고 관리 시스템 라이선스 (연간) 1식 4,800,000원"

const quoteReply = "견적서를 PDF로 만들어 견적서.pdf 파일로 첨부했습니다.\n\n- 수신처: 주식회사 샘플유통 박예시 과장님"

type unmetReplyRecorder struct {
	prompts []string
}

func (recorder *unmetReplyRecorder) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (recorder *unmetReplyRecorder) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, nil
}

func (recorder *unmetReplyRecorder) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	recorder.prompts = append(recorder.prompts, request.Messages[0].Content)
	return model.ChatCompletionResponse{FinishReason: "stop", Message: model.ChatCompletionMessage{Role: "assistant", Content: quoteReply}}, nil
}

func TestTheUnmetChangesReplyIsToldWhichFilesItCarries(t *testing.T) {
	recorder := &unmetReplyRecorder{}
	services := newTurnRunnerTestServices(recorder, TurnOptions{MaxIterationCount: 8})
	request := AgentTurnRequest{Prompt: quoteRequest, ResponseLanguage: "ko"}
	check := changeCheck{Unmet: []expectedChange{{Change: "file created", Asked: "견적서를 pdf로 만들어 주세요."}}}
	carried := []toolcontract.FileAttachment{{DevicePath: "/workspace/private/people/person-1/documents/견적서.pdf", Filename: "견적서.pdf", ContentType: "application/pdf", SizeBytes: 34549}}

	services.runner.replyForFinish(context.Background(), "task-run-1", request, &agentTaskState{}, completionGateResult{IsSatisfied: true, Attachments: carried, ChangeCheck: &check}, quoteReply, nil)

	if len(recorder.prompts) != 1 {
		t.Fatalf("expected one rewrite, got %d", len(recorder.prompts))
	}
	prompt := recorder.prompts[0]
	if !strings.Contains(prompt, "Files this reply carries: 견적서.pdf.") || !strings.Contains(prompt, "never say they were not made") {
		t.Fatalf("expected the rewrite told the reply carries 견적서.pdf and that it was made, got %q", prompt)
	}
}

func TestTheUnmetChangesReplyWithNoFileIsToldItCarriesNone(t *testing.T) {
	prompt := buildFinishReplyRewritePrompt(AgentTurnRequest{Prompt: quoteRequest}, quoteReply, finishReplyRewrite{unmet: []expectedChange{{Change: "file created", Asked: "견적서를 pdf로 만들어 주세요."}}})

	if !strings.Contains(prompt, "Files this reply carries: none.") {
		t.Fatalf("expected the rewrite told the reply carries no file, got %q", prompt)
	}
}

func TestAFinishWhoseCheckStaysUnmetRewritesTheReplyKnowingTheDeliveredFile(t *testing.T) {
	delivery := w1DeliveredWorkbook(t)
	attachment := delivery.Observations[len(delivery.Observations)-1].Attachments[0]
	recorder := &unmetReplyRecorder{}
	services := newTurnRunnerTestServices(recorder, TurnOptions{MaxIterationCount: 8})
	check := changeCheck{Unmet: []expectedChange{{Change: "file changed", Asked: strings.SplitN(delivery.Prompt, "\n", 2)[0]}}}
	gate := completionGateResult{IsSatisfied: true, Attachments: []toolcontract.FileAttachment{attachment}, ChangeCheck: &check}
	state := agentTaskState{}

	services.runner.replyForFinish(context.Background(), "task-run-1", AgentTurnRequest{Prompt: delivery.Prompt}, &state, gate, "done", nil)

	encoded, _ := json.Marshal(recorder.prompts)
	if len(recorder.prompts) != 1 || !strings.Contains(recorder.prompts[0], "Files this reply carries: "+attachment.Filename+".") {
		t.Fatalf("expected the finish's rewrite told the workbook it carries, got %s", encoded)
	}
}

const quarterlyReviewReply = "사내 3분기 업무 리뷰 발표자료를 첨부했습니다.\n1. 표지 — \"3분기 매출 목표의 90%를 달성하고, 4분기에 재도전합니다\" · 발표자 이샘플"

var quarterlyReviewDeliveryNotes = []string{
	"q3-review-2026.pptx: left blank because nothing the person gave supports them, for the reply to offer to complete: 슬라이드 1 제목 (it said \"3분기 매출 목표의 90%를 달성하고, 4분기에 재도전합니다\")",
	"q3-review-2026.pptx: slides that still show a defect after the visual review, for the reply to say what remains: slide 2 (imbalanced_layout)",
}

type deliveringFinishModel struct {
	actionCount    int
	rewritePrompts []string
}

func (languageModel *deliveringFinishModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *deliveringFinishModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, nil
}

func (languageModel *deliveringFinishModel) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	if request.SchemaName == deliveryNotesReplySchemaName {
		languageModel.rewritePrompts = append(languageModel.rewritePrompts, request.Messages[0].Content)
		return model.ChatCompletionResponse{FinishReason: "stop", Message: model.ChatCompletionMessage{Role: "assistant", Content: "발표자료를 첨부했습니다. 표지 제목은 비워 두었습니다."}}, nil
	}
	languageModel.actionCount++
	finish, _ := json.Marshal(map[string]any{"final": true, "message": quarterlyReviewReply, "attachments": []map[string]string{{"path": "q3-review-2026/build/q3-review-2026.pptx"}}, "goalStatus": "satisfied", "goalSatisfied": true})
	toolCall := nativeAgentActionToolCall("reply", string(finish))
	return model.ChatCompletionResponse{FinishReason: "tool_calls", Message: model.ChatCompletionMessage{Role: "assistant", ToolCalls: []model.ChatCompletionToolCall{toolCall}}}, nil
}

func TestAFinishWhoseOwnDeliveryReportsNotesRewritesTheReplyWithThem(t *testing.T) {
	languageModel := &deliveringFinishModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 4})
	toolRegistry := newTestToolSet([]string{toolcontract.BashToolName})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: toolcontract.FileDeliverToolName, Visibility: toolcontract.ToolVisibilityInternal}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output:      toolcontract.ToolOutput{Content: "files staged"},
			Attachments: []toolcontract.FileAttachment{{DevicePath: "/workspace/q3-review-2026.pptx", Filename: "q3-review-2026.pptx", ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation", SizeBytes: 2590842}},
			ReplyNotes:  quarterlyReviewDeliveryNotes,
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{RequesterPersonID: "person-1", ConversationID: "conversation-1", Prompt: "사내 3분기 업무 리뷰 발표자료를 pptx로 만들어 주세요.", ToolSet: toolRegistry})

	if errorValue != nil || result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected the finish to complete, got %v %+v", errorValue, result.TaskRun)
	}
	if len(languageModel.rewritePrompts) != 1 || !strings.Contains(languageModel.rewritePrompts[0], quarterlyReviewDeliveryNotes[0]) || !strings.Contains(languageModel.rewritePrompts[0], quarterlyReviewDeliveryNotes[1]) || !strings.Contains(languageModel.rewritePrompts[0], quarterlyReviewReply) {
		t.Fatalf("expected one rewrite told the reply and both notes its own delivery reported, got %q", languageModel.rewritePrompts)
	}
	if strings.Contains(languageModel.rewritePrompts[0], "ask the person to check it") {
		t.Fatalf("expected no request to check the file when every asked change is met, got %q", languageModel.rewritePrompts[0])
	}
	if result.FinishMessage != "발표자료를 첨부했습니다. 표지 제목은 비워 두었습니다." || len(result.Attachments) != 1 {
		t.Fatalf("expected the rewritten reply with its attachment, got %q %+v", result.FinishMessage, result.Attachments)
	}
}

func TestAFinishWithoutDeliveryNotesSendsItsReplyAsWritten(t *testing.T) {
	reply, carried := (&AgentTurnRunner{}).replyForFinish(context.Background(), "task-run-1", AgentTurnRequest{Prompt: quoteRequest}, &agentTaskState{}, completionGateResult{IsSatisfied: true}, quoteReply, nil)

	if reply != quoteReply || len(carried) != 0 {
		t.Fatalf("expected the reply unchanged, got %q", reply)
	}
}
