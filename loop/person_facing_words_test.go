package loop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestEveryWriterOfWhatAPersonReadsIsToldToNameThingsTheWayThePersonKnowsThem(t *testing.T) {
	request := AgentTurnRequest{Prompt: "완료 업무로 추가해줘", ToolSet: newTestToolSet([]string{toolcontract.BashToolName})}
	writers := map[string]string{
		"the turn that chooses reply or fail":          systemInstructionFor(TurnOptions{}, request).Text(),
		"the reply for a completed request":            buildCompletionReplyPrompt(request, nil, nil),
		"the rewrite of a reply that claimed too much": buildFinishReplyRewritePrompt(request, "추가했어요.", finishReplyRewrite{unmet: []string{"완료 업무로 추가해줘"}}),
		"the compression of a long reply":              BuildFinishMessageCompressionPrompt("추가했어요.", "ko", 1200),
		"the confirmation message":                     confirmationMessagePrompt(t),
	}
	for writer, prompt := range writers {
		if !strings.Contains(prompt, agentcontract.PeopleReadWhatTheyRecognize) {
			t.Errorf("%s is not told to name things the way the person knows them", writer)
		}
	}
}

func confirmationMessagePrompt(t *testing.T) string {
	t.Helper()
	languageModel := &sequenceLanguageModel{contents: []string{`{"reply":"공유할 그룹을 알려주세요."}`}}
	kernel := NewAgentKernel(nil, nil)
	kernel.UseLanguageModelProvider(languageModel)
	_, errorValue := kernel.GenerateClarificationMessage(context.Background(), AgentRequest{Prompt: "보고서 공유해줘", ResponseLanguage: "ko"}, ExecutionPlan{MissingInformation: []string{"공유 대상 그룹"}}, ConfirmationPolicyDecision{RequiresClarification: true})
	if errorValue != nil || len(languageModel.requests) != 1 {
		t.Fatalf("expected one confirmation message request, got %d: %v", len(languageModel.requests), errorValue)
	}
	return joinMessageContent(languageModel.requests[0].Messages)
}

type failingRewriteLanguageModel struct{}

func (failingRewriteLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (failingRewriteLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, nil
}

func (failingRewriteLanguageModel) GenerateChatCompletion(context.Context, model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return model.ChatCompletionResponse{}, errors.New("provider returned 503")
}

func TestAFailedRewriteStillTellsThePersonWhatIsNotDoneAndRecordsWhy(t *testing.T) {
	services := newTurnRunnerTestServices(failingRewriteLanguageModel{}, TurnOptions{MaxIterationCount: 8})
	request := AgentTurnRequest{Prompt: "완료 업무로 추가해줘", ResponseLanguage: "ko"}
	check := changeCheck{ExpectedChanges: []string{"완료 업무로 추가"}, Unmet: []string{"완료 업무로 추가"}, UnwrittenFields: map[string]string{changeQuestionKey(0): "status"}}
	state := agentTaskState{Observations: []turnObservation{{Tool: "file_deliver", Attachments: []toolcontract.FileAttachment{{DevicePath: "/workspace/report.docx", Filename: "report.docx"}}, ReplyNotes: []string{"report.docx: left blank, for the reply to offer to complete: owner"}}}}
	gate := completionGateResult{IsSatisfied: true, ChangeCheck: &check, Attachments: []toolcontract.FileAttachment{{DevicePath: "/workspace/report.docx", Filename: "report.docx"}}}

	reply, _ := services.runner.replyForFinish(context.Background(), "task-run-1", request, &state, gate, "추가했어요.")

	if !strings.HasPrefix(reply, "추가했어요.") || !strings.Contains(reply, "완료 업무로 추가") {
		t.Fatalf("expected the reply as written followed by what is still not done, got %q", reply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-run-1"), agentcontract.TaskEventAgentCompletionReplyFailed, "provider returned 503") {
		t.Fatalf("expected the failed rewrite recorded, got %+v", services.taskEventService.ListTaskEvent("task-run-1"))
	}
}

func TestARewriteWithNoModelToWriteItStillTellsThePersonWhatIsNotDoneAndRecordsWhy(t *testing.T) {
	services := newTurnRunnerTestServices(nil, TurnOptions{MaxIterationCount: 8})
	check := changeCheck{Unmet: []string{"완료 업무로 추가"}}

	reply, _ := services.runner.replyForFinish(context.Background(), "task-run-1", AgentTurnRequest{Prompt: "완료 업무로 추가해줘"}, &agentTaskState{}, completionGateResult{IsSatisfied: true, ChangeCheck: &check}, "추가했어요.")

	if !strings.HasPrefix(reply, "추가했어요.") || !strings.Contains(reply, "완료 업무로 추가") {
		t.Fatalf("expected the reply as written followed by what is still not done, got %q", reply)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent("task-run-1"), agentcontract.TaskEventAgentCompletionReplyFailed, unmetChangesReplySchemaName) {
		t.Fatalf("expected the skipped rewrite recorded, got %+v", services.taskEventService.ListTaskEvent("task-run-1"))
	}
}
