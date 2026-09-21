package loop

import (
	"context"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func buildFailureReport(request AgentTurnRequest, taskRunID string, phase string, stopReason string, observations []turnObservation, attachments []toolcontract.FileAttachment, executionState ExecutionState, decision recoveryDecision) FailureReport {
	report := FailureReport{
		Phase:               strings.TrimSpace(phase),
		StopReason:          compactWhitespace(strings.TrimSpace(stopReason)),
		RawError:            compactWhitespace(redactRawFailureNotice(strings.TrimSpace(stopReason))),
		FailedOperation:     latestFailedOperation(observations),
		SafeFailureSummary:  latestSafeFailureSummary(observations, stopReason),
		CompletedSummary:    buildLimitObservationSummary(observations),
		NextAction:          strings.TrimSpace(decision.NextAction),
		OriginalRequest:     strings.TrimSpace(request.Prompt),
		ResponseLanguage:    strings.TrimSpace(request.ResponseLanguage),
		ArtifactRequired:    requestRequiresFileAttachment(request),
		HasAttachments:      len(attachments) > 0,
		AttachmentFilenames: failureReportAttachmentFilenames(attachments),
		DiagnosticEventID:   diagnosticEventID(request, taskRunID, phase),
	}
	if report.Phase == "limit" && report.StopReason == "max_elapsed" {
		report.RawError = elapsedLimitRawErrorSummary
	}
	if report.NextAction == "" {
		report.NextAction = strings.TrimSpace(decision.UserReplyIntent)
	}
	if report.NextAction == "" {
		report.NextAction = strings.TrimSpace(executionState.NextPlan)
	}
	return report
}

func requestContextForTurn(request AgentTurnRequest) model.RequestContext {
	return model.RequestContext{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		ConversationID:          request.ConversationID,
		Platform:                request.Platform,
	}
}

func recoveryFinalizationContextWithParent(parentContext context.Context, request AgentTurnRequest) (context.Context, context.CancelFunc) {
	return context.WithCancel(model.ContextWithRequestContext(parentContext, requestContextForTurn(request)))
}

// The context that died is usually the wedged model path the notice has to be generated through,
// so the closing notice keeps the caller's values and drops its cancellation, bounded by the same
// ceiling the elapsed closing reply is given. Outliving that bound would hold the turn, and with
// it the conversation, open forever.
func closingNoticeContextWithParent(parentContext context.Context, request AgentTurnRequest) (context.Context, context.CancelFunc) {
	noticeContext := model.ContextWithRequestContext(context.WithoutCancel(parentContext), requestContextForTurn(request))
	return context.WithTimeout(noticeContext, maximumElapsedClosingDuration)
}

func latestFailedOperation(observations []turnObservation) string {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !observation.Failed() {
			continue
		}
		operation := strings.TrimSpace(observation.Tool)
		if operation == "" {
			operation = strings.TrimSpace(observation.Action)
		}
		return operation
	}
	return ""
}

// The summary labels the failure and shell labels every one of them with its exit status.
// A report that reaches the user saying the command exited 1 tells them nothing they can act on.
func failureLineForUser(observation turnObservation) string {
	summary := strings.TrimSpace(observation.FailureSummary())
	printed := strings.TrimSpace(observation.ContentText())
	if summary == "" {
		return firstNonEmptyString(printed, strings.TrimSpace(summarizeObservationContent(observation)))
	}
	if printed == "" || printed == summary {
		return summary
	}
	return summary + ": " + printed
}

func latestSafeFailureSummary(observations []turnObservation, fallback string) string {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !observation.Failed() {
			continue
		}
		if summary := failureLineForUser(observation); summary != "" {
			return truncateText(compactWhitespace(redactUnsafeText(summary)), 360)
		}
	}
	return truncateText(compactWhitespace(redactUnsafeText(fallback)), 360)
}
