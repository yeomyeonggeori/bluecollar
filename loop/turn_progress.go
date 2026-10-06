package loop

import (
	"strings"
)

const progressMessageLimit = 6000
const toolResultContextLimit = 120000

const unredactedOutputLimit = 20000

const charactersPerToken = 4
const conversationShareOfContextPercent = 60
const maxProgressObservations = 12
const maxInteractiveReferences = 20
const maxSummaryTextLength = 500

type TurnProgress struct {
	Goal                          string                `json:"goal"`
	CompletedSteps                []ProgressObservation `json:"completedSteps,omitempty"`
	FailedOrBlockedSteps          []ProgressObservation `json:"failedOrBlockedSteps,omitempty"`
	CheckpointMessages            []string              `json:"checkpointMessages,omitempty"`
	RecentFiles                   []ProgressFileContext `json:"recentFiles,omitempty"`
	LastSuccessfulObservationID   string                `json:"lastSuccessfulObservationID,omitempty"`
	LastSuccessfulObservationTool string                `json:"lastSuccessfulObservationTool,omitempty"`
	AttachmentCandidates          []ProgressAttachment  `json:"attachmentCandidates,omitempty"`
	FailureDebt                   *ProgressFailureDebt  `json:"failureDebt,omitempty"`
	AttemptLedger                 []attemptLedgerEntry  `json:"attemptLedger,omitempty"`
	RemainingWork                 string                `json:"remainingWork"`
	OmittedObservationCount       int                   `json:"omittedObservationCount,omitempty"`
}

type ProgressObservation struct {
	ObservationID      string               `json:"observationID"`
	ToolName           string               `json:"toolName,omitempty"`
	Status             string               `json:"status"`
	Summary            string               `json:"summary,omitempty"`
	AttachmentRefs     []ProgressAttachment `json:"attachmentRefs,omitempty"`
	ImageRefs          []ToolResultImageRef `json:"imageRefs,omitempty"`
	AttemptFingerprint string               `json:"attemptFingerprint,omitempty"`
	RecoveryStep       string               `json:"recoveryStep,omitempty"`
	RepeatCount        int                  `json:"repeatCount,omitempty"`
	SameOutputAs       string               `json:"sameOutputAs,omitempty"`
}

type ProgressFailureDebt struct {
	ObservationID           string         `json:"observationID"`
	ToolName                string         `json:"toolName"`
	FailureStage            string         `json:"failureStage,omitempty"`
	ErrorCode               string         `json:"errorCode,omitempty"`
	AttemptFingerprint      string         `json:"attemptFingerprint,omitempty"`
	RemainingRecoveryBudget RecoveryBudget `json:"remainingRecoveryBudget"`
	AllowedFinalResolutions []string       `json:"allowedFinalResolutions"`
}

type ProgressAttachment struct {
	ObservationID    string `json:"observationID"`
	AttachmentIndex  int    `json:"attachmentIndex"`
	Filename         string `json:"filename,omitempty"`
	ContentType      string `json:"contentType,omitempty"`
	SizeBytes        int64  `json:"sizeBytes,omitempty"`
	Title            string `json:"title,omitempty"`
	HasDevicePayload bool   `json:"hasDevicePayload"`
}

type ToolResultContextItem struct {
	ObservationID  string               `json:"observationID"`
	ToolName       string               `json:"toolName,omitempty"`
	Status         string               `json:"status"`
	Summary        string               `json:"summary"`
	RecoveryPacket *RecoveryPacket      `json:"recoveryPacket,omitempty"`
	ImageRefs      []ToolResultImageRef `json:"imageRefs,omitempty"`
	Attachments    []ProgressAttachment `json:"attachments,omitempty"`
}

func buildTurnProgress(observations []turnObservation) TurnProgress {
	progress := TurnProgress{
		Goal:          "Answer the current user request.",
		RemainingWork: "Continue from the latest observation and complete the user's request.",
	}
	progress.CheckpointMessages = checkpointMessages(observations)
	for _, observation := range compactProgressObservations(observations) {
		if observation.Status == "success" {
			progress.CompletedSteps = append(progress.CompletedSteps, observation)
			progress.LastSuccessfulObservationID = observation.ObservationID
			progress.LastSuccessfulObservationTool = observation.ToolName
			progress.AttachmentCandidates = append(progress.AttachmentCandidates, observation.AttachmentRefs...)
		} else {
			progress.FailedOrBlockedSteps = append(progress.FailedOrBlockedSteps, observation)
		}
	}
	progress.AttemptLedger = attemptLedger(observations)
	progress.RecentFiles = recentFileContexts(observations)
	progress.OmittedObservationCount = omittedObservationCount(progress)
	progress.CompletedSteps = latestProgressItems(progress.CompletedSteps)
	progress.FailedOrBlockedSteps = latestProgressItems(progress.FailedOrBlockedSteps)
	if failureDebt, hasFailureDebt := activeFailureDebt(observations); hasFailureDebt {
		progress.FailureDebt = buildProgressFailureDebt(failureDebt, observations)
	}
	if len(observations) > 0 && progress.LastSuccessfulObservationID == "" {
		progress.RemainingWork = "Resolve the latest failed or blocked step, or return a truthful failure if the goal cannot be completed."
	}
	return progress
}

func checkpointMessages(observations []turnObservation) []string {
	messages := []string{}
	for _, observation := range observations {
		if observation.Action != "checkpoint" {
			continue
		}
		message := strings.TrimSpace(checkpointObservationMessage(observation))
		if message != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func compactProgressObservations(observations []turnObservation) []ProgressObservation {
	compactedObservations := []ProgressObservation{}
	for _, observation := range observations {
		if observation.Action == "checkpoint" || observation.Action == "recovery_guidance" {
			continue
		}
		progressObservation := summarizeObservation(observation)
		index := len(compactedObservations) - 1
		if index >= 0 && progressObservationSignature(compactedObservations[index]) == progressObservationSignature(progressObservation) {
			compactedObservations[index].RepeatCount++
			compactedObservations[index].ObservationID = progressObservation.ObservationID
			compactedObservations[index].AttachmentRefs = append(compactedObservations[index].AttachmentRefs, progressObservation.AttachmentRefs...)
			continue
		}
		progressObservation.RepeatCount = 1
		compactedObservations = append(compactedObservations, progressObservation)
	}
	return compactedObservations
}

func recentProgressObservations(observations []turnObservation) []ProgressObservation {
	return latestProgressItems(compactProgressObservations(observations))
}

func latestProgressItems[Item any](items []Item) []Item {
	if len(items) <= maxProgressObservations {
		return items
	}
	return items[len(items)-maxProgressObservations:]
}

func omittedObservationCount(progress TurnProgress) int {
	count := len(progress.CompletedSteps) + len(progress.FailedOrBlockedSteps)
	if count <= maxProgressObservations*2 {
		return 0
	}
	return count - maxProgressObservations*2
}

func summarizeObservation(observation turnObservation) ProgressObservation {
	status := "success"
	if observation.Failed() {
		status = "error"
	}
	return ProgressObservation{
		ObservationID:      observation.ObservationID,
		ToolName:           observation.Tool,
		Status:             status,
		Summary:            summarizeObservationContent(observation),
		AttachmentRefs:     progressAttachments(observation),
		ImageRefs:          append([]ToolResultImageRef{}, observation.ImageRefs...),
		AttemptFingerprint: strings.TrimSpace(observation.AttemptFingerprint),
		RecoveryStep:       strings.TrimSpace(observation.RecoveryStep),
		SameOutputAs:       strings.TrimSpace(observation.RepeatsObservationID),
	}
}

func toolResultContextItems(observations []turnObservation) []ToolResultContextItem {
	items := []ToolResultContextItem{}
	totalLength := 0
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		summary := strings.TrimSpace(observation.Summary)
		if summary == "" && len(observation.ImageRefs) == 0 {
			continue
		}
		remainingLength := toolResultContextLimit - totalLength
		if remainingLength <= 0 {
			break
		}
		if len(summary) > remainingLength {
			summary = strings.ToValidUTF8(summary[:remainingLength], "") + "\n[trimmed]"
		}
		totalLength += len(summary)
		status := "success"
		if observation.Failed() {
			status = "error"
		}
		items = append([]ToolResultContextItem{{
			ObservationID:  observation.ObservationID,
			ToolName:       observation.Tool,
			Status:         status,
			Summary:        summary,
			RecoveryPacket: observation.RecoveryPacket,
			ImageRefs:      append([]ToolResultImageRef{}, observation.ImageRefs...),
			Attachments:    progressAttachments(observation),
		}}, items...)
	}
	return items
}

func buildProgressFailureDebt(failureDebt FailureDebt, observations []turnObservation) *ProgressFailureDebt {
	budget := defaultRecoveryBudget()
	return &ProgressFailureDebt{
		ObservationID:           failureDebt.LatestFailure.ObservationID,
		ToolName:                strings.TrimSpace(failureDebt.LatestFailure.Tool),
		FailureStage:            failureDebt.LatestFailure.FailureStage(),
		ErrorCode:               failureDebt.LatestFailure.FailureCode(),
		AttemptFingerprint:      strings.TrimSpace(failureDebt.LatestFailure.AttemptFingerprint),
		RemainingRecoveryBudget: remainingRecoveryBudget(observations, budget),
		AllowedFinalResolutions: []string{failureResolutionNoToolFallback, failureResolutionFailureReport},
	}
}

func remainingRecoveryBudget(observations []turnObservation, budget RecoveryBudget) RecoveryBudget {
	budget = normalizeRecoveryBudget(budget)
	return RecoveryBudget{
		CorrectedRetry: max(0, budget.CorrectedRetry-recoveryStepUseCount(observations, recoveryStepCorrectedRetry)),
		AlternateRoute: max(0, budget.AlternateRoute-recoveryStepUseCount(observations, recoveryStepAlternateRoute)),
		AdjacentTool:   max(0, budget.AdjacentTool-recoveryStepUseCount(observations, recoveryStepAdjacentTool)),
		NoToolFallback: budget.NoToolFallback,
	}
}

func summarizeObservationContent(observation turnObservation) string {
	if strings.TrimSpace(observation.Summary) != "" {
		return truncateText(compactWhitespace(observation.Summary), maxSummaryTextLength)
	}
	if observation.Failed() {
		if summary := summarizeStructuredFailure(observation); summary != "" {
			return summary
		}
	}
	content := observation.ContentText()
	if carriesPageSnapshot(content) {
		return summarizeBrowserSnapshot(content)
	}
	if carriesImageAttachment(observation) {
		return "Image captured with attachment evidence."
	}
	switch strings.TrimSpace(observation.Tool) {
	case "file_pick":
		if len(observation.Attachments) > 0 {
			return "User selected a file and it is available as attachment evidence."
		}
		return summarizeSafeJSONFields(content, []string{"filename", "sizeBytes", "contentType", "expiresAt"})
	case "file_read":
		return summarizeFileReadObservation(observation)
	case "memory_search", "conversation_history":
		return summarizeCollection(content)
	default:
		if observation.Failed() {
			return truncateText(compactWhitespace(redactUnsafeText(content)), 500)
		}
		if fields := summarizeSafeJSONFields(content, []string{"ok", "action", "target", "status", "message", "error", "url", "title", "filename", "sizeBytes", "contentType", "capturedAt"}); fields != "" {
			return fields
		}
		return truncateText(redactUnsafeText(content), unredactedOutputLimit)
	}
}

func progressAttachments(observation turnObservation) []ProgressAttachment {
	attachments := []ProgressAttachment{}
	for index, attachment := range observation.Attachments {
		attachments = append(attachments, ProgressAttachment{
			ObservationID:    observation.ObservationID,
			AttachmentIndex:  index,
			Filename:         attachment.Filename,
			ContentType:      attachment.ContentType,
			SizeBytes:        attachment.SizeBytes,
			Title:            attachment.Title,
			HasDevicePayload: strings.TrimSpace(attachment.DevicePath) != "",
		})
	}
	return attachments
}

func progressObservationSignature(observation ProgressObservation) string {
	return strings.Join([]string{observation.ToolName, observation.Status, observation.Summary}, "\x00")
}
