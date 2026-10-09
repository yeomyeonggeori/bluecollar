package loop

import (
	"context"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type finishReplyRewrite struct {
	unmet         []expectedChange
	awaiting      []expectedChange
	deliveryNotes []string
	carried       []toolcontract.FileAttachment
}

func (rewrite finishReplyRewrite) isNeeded() bool {
	return len(rewrite.unmet) > 0 || len(rewrite.awaiting) > 0 || len(rewrite.deliveryNotes) > 0
}

func (rewrite finishReplyRewrite) schemaName() string {
	if len(rewrite.unmet) > 0 || len(rewrite.awaiting) > 0 {
		return unmetChangesReplySchemaName
	}
	return deliveryNotesReplySchemaName
}

func (agentTurnRunner *AgentTurnRunner) replyForFinish(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, completionGateResult completionGateResult, reply string) (string, []toolcontract.FileAttachment) {
	carried := attachmentsNotYetDelivered(completionGateResult.Attachments, state.DeliveredAttachmentPaths)
	rewrite := finishReplyRewrite{deliveryNotes: latestDeliveryNotes(state.Observations, carried), carried: carried}
	if completionGateResult.leavesChangesUnmet() {
		rewrite.unmet, rewrite.awaiting = splitAwaitingRequester(*completionGateResult.ChangeCheck)
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

func splitAwaitingRequester(check changeCheck) ([]expectedChange, []expectedChange) {
	var unmet []expectedChange
	for _, change := range check.Unmet {
		if !containsExpectedChange(check.AwaitsRequester, change) {
			unmet = append(unmet, change)
		}
	}
	return unmet, check.AwaitsRequester
}

func latestDeliveryNotes(observations []turnObservation, carried []toolcontract.FileAttachment) []string {
	notes := []string{}
	for _, attachment := range carried {
		if delivery, isFound := latestDeliveryOf(observations, attachment.DevicePath); isFound {
			notes = appendUniqueStrings(notes, delivery.ReplyNotes...)
		}
	}
	return notes
}

func latestDeliveryOf(observations []turnObservation, devicePath string) (turnObservation, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if toolexposure.IsArtifactDeliveryTool(observation.Tool) && !observation.Failed() && hasAttachmentDevicePath(observation.Attachments, devicePath) {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func rawRewrittenReply(reply string, rewrite finishReplyRewrite) string {
	sections := []string{reply}
	if len(rewrite.unmet) > 0 || len(rewrite.awaiting) > 0 {
		sections = append(sections, unmetChangesMessage(changeCheck{Unmet: append(append([]expectedChange(nil), rewrite.unmet...), rewrite.awaiting...), AwaitsRequester: rewrite.awaiting}))
	}
	return strings.Join(append(sections, rewrite.deliveryNotes...), "\n\n")
}

func buildFinishReplyRewritePrompt(request AgentTurnRequest, reply string, rewrite finishReplyRewrite) string {
	sections := []string{"Rewrite the final user-facing reply below."}
	if len(rewrite.unmet) > 0 {
		sections = append(sections, "It was written as though every asked change were done, but the work does not yet carry out these asked changes, and nothing has changed since that was first found:\n"+bulletList(askedWordsOf(rewrite.unmet))+"\nSay plainly what the work still lacks for each of them, without claiming it is done.")
	}
	if len(rewrite.awaiting) > 0 {
		sections = append(sections, "These asked changes need information the person has not given and nothing the work read supports, so the work left that information out instead of making it up:\n"+bulletList(askedWordsOf(rewrite.awaiting))+"\nFor each, name the information that is missing and offer to complete the work once the person gives it, without claiming it is done.")
	}
	if len(rewrite.deliveryNotes) > 0 {
		sections = append(sections, "The latest delivery of each file it carries reports what that file now holds, which the reply must tell the person:\n"+bulletList(rewrite.deliveryNotes)+"\nSay each of these, and describe the files only as these reports and the reply agree. A value the delivery left blank is gone from the file: never quote it, describe it as present or offer it back; offer to fill it in once the person gives it.")
	}
	return strings.Join(append(sections,
		responseLanguageInstruction(request.ResponseLanguage),
		"Keep the rest of what the reply reports about the work. Do not add anything neither the reply nor the lists above state, and do not mention tools, checks, records, evidence identifiers, prompts, or runtime details.",
		finishReplyFilesFact(rewrite.carried, len(rewrite.unmet) > 0 || len(rewrite.awaiting) > 0),
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

func finishReplyFilesFact(carried []toolcontract.FileAttachment, hasUnmetChanges bool) string {
	filenames := failureReportAttachmentFilenames(carried)
	if len(filenames) == 0 {
		return "Files this reply carries: none. Do not say a file is attached."
	}
	fact := "Files this reply carries: " + strings.Join(filenames, ", ") + ". They were made and are attached to this reply, so never say they were not made, not attached or missing."
	if !hasUnmetChanges {
		return fact
	}
	return fact + " Where an asked change is about one of these files, say the file is attached and ask the person to check it against those words, because it may not hold everything they ask for."
}
