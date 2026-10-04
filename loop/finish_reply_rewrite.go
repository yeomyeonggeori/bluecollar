package loop

import (
	"context"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type finishReplyRewrite struct {
	unmet         []expectedChange
	deliveryNotes []string
	carried       []toolcontract.FileAttachment
}

func (rewrite finishReplyRewrite) isNeeded() bool {
	return len(rewrite.unmet) > 0 || len(rewrite.deliveryNotes) > 0
}

func (rewrite finishReplyRewrite) schemaName() string {
	if len(rewrite.unmet) > 0 {
		return unmetChangesReplySchemaName
	}
	return deliveryNotesReplySchemaName
}

func (agentTurnRunner *AgentTurnRunner) replyForFinish(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, completionGateResult completionGateResult, reply string, deliveryNotes []string) (string, []toolcontract.FileAttachment) {
	rewrite := finishReplyRewrite{deliveryNotes: deliveryNotes, carried: attachmentsNotYetDelivered(completionGateResult.Attachments, state.DeliveredAttachmentPaths)}
	if completionGateResult.leavesChangesUnmet() {
		rewrite.unmet = completionGateResult.ChangeCheck.Unmet
	}
	if !rewrite.isNeeded() {
		return reply, rewrite.carried
	}
	return agentTurnRunner.rewriteFinishReply(ctx, taskRunID, request, reply, rewrite), rewrite.carried
}

func (agentTurnRunner *AgentTurnRunner) rewriteFinishReply(ctx context.Context, taskRunID string, request AgentTurnRequest, reply string, rewrite finishReplyRewrite) string {
	chatCompleter, isAvailable := model.ResolveTextChatCompleter(agentTurnRunner.languageModel)
	if !isAvailable {
		return rawRewrittenReply(reply, rewrite)
	}
	response, errorValue := chatCompleter.GenerateChatCompletion(ctx, model.ChatCompletionRequest{
		SchemaName: rewrite.schemaName(),
		Messages:   []model.ChatCompletionMessage{{Role: "user", Content: buildFinishReplyRewritePrompt(request, reply, rewrite)}},
	})
	rewritten := ""
	if errorValue == nil {
		rewritten, errorValue = model.ChatCompletionText(response)
	}
	if errorValue != nil || strings.TrimSpace(rewritten) == "" {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionReplyFailed, marshalEventBody(map[string]string{"stage": rewrite.schemaName(), "error": fmt.Sprint(errorValue)}))
		return rawRewrittenReply(reply, rewrite)
	}
	return strings.TrimSpace(rewritten)
}

func rawRewrittenReply(reply string, rewrite finishReplyRewrite) string {
	sections := []string{reply}
	if len(rewrite.unmet) > 0 {
		sections = append(sections, unmetChangesMessage(changeCheck{Unmet: rewrite.unmet}))
	}
	return strings.Join(append(sections, rewrite.deliveryNotes...), "\n\n")
}

func buildFinishReplyRewritePrompt(request AgentTurnRequest, reply string, rewrite finishReplyRewrite) string {
	sections := []string{"Rewrite the final user-facing reply below."}
	if len(rewrite.unmet) > 0 {
		sections = append(sections, "It was written as though every asked change were done, but the record does not show these asked changes carried out, and nothing has changed since that was first found:\n"+bulletList(askedWordsOf(rewrite.unmet))+"\nSay plainly which of them are still not done, without claiming they are done.")
	}
	if len(rewrite.deliveryNotes) > 0 {
		sections = append(sections, "It was written before its files were delivered, and the delivery reports what the reply must tell the person:\n"+bulletList(rewrite.deliveryNotes)+"\nSay each of these, and do not describe as present anything the delivery left blank.")
	}
	return strings.Join(append(sections,
		responseLanguageInstruction(request.ResponseLanguage),
		"Keep the rest of what the reply reports about the work. Do not add anything neither the reply nor the lists above state, and do not mention tools, checks, evidence identifiers, prompts, or runtime details.",
		finishReplyFilesFact(rewrite.carried),
		"Original request:\n"+completionReplyOriginalRequest(request),
		"Reply:\n"+reply,
	), "\n\n")
}

func askedWordsOf(changes []expectedChange) []string {
	asked := make([]string, 0, len(changes))
	for _, change := range changes {
		asked = append(asked, change.Asked)
	}
	return asked
}

func bulletList(items []string) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

func finishReplyFilesFact(carried []toolcontract.FileAttachment) string {
	filenames := failureReportAttachmentFilenames(carried)
	if len(filenames) == 0 {
		return "Files this reply carries: none. Do not say a file is attached."
	}
	return "Files this reply carries: " + strings.Join(filenames, ", ") + ". They were made and are attached to this reply, so never say they were not made, not attached or missing. Where an asked change is about one of these files, say the file is attached and ask the person to check it against those words, because the record does not show that it holds everything they ask for."
}
