package loop

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	completionJudgeSchemaName               = "bluecollar_completion_judge"
	completionJudgeMaxRequestedObservations = 8
	completionJudgeMaxMissingWork           = 5
	completionJudgeMissingWorkMaxLength     = 200
	completionJudgeReasonMaxLength          = 400
	completionJudgeInputMaxLength           = 2000
	completionJudgeResultMaxLength          = 300
	completionJudgeCitedResultMaxLength     = 6000
	completionJudgeLedgerByteBudget         = 24000
)

type completionJudgeVerdict struct {
	Satisfied          bool     `json:"satisfied"`
	MissingWork        []string `json:"missingWork"`
	Reason             string   `json:"reason"`
	NeedObservationIDs []string `json:"needObservationIDs"`
}

type completionLedgerEntry struct {
	Tool   string `json:"tool"`
	Input  string `json:"input"`
	Result string `json:"result"`
	Failed bool   `json:"failed,omitempty"`
}

func outcomeContractHasSideEffectEvidence(toolSet *toolcontract.ToolSet, contract OutcomeContract) bool {
	if requiredEvidenceIncludesSideEffect(toolSet, contract.RequiredEvidenceTools) {
		return true
	}
	for _, toolNames := range contract.RequiredEvidenceAnyOf {
		if requiredEvidenceIncludesSideEffect(toolSet, toolNames) {
			return true
		}
	}
	return false
}

// A hint is intake's guess, so it cannot bind the deterministic gate, but a task
// whose working set was chosen around a tool that changes something can end in a
// reply claiming the change. Whether this reply does is a reading of the reply
// against the ledger, which is the judge's question, not a rule.
func outcomeContractHintsSideEffectEvidence(toolSet *toolcontract.ToolSet, contract OutcomeContract) bool {
	return requiredEvidenceIncludesSideEffect(toolSet, contract.SelectedEvidenceHints)
}

// What there is to grade is named by the contract; delivering the reply is asked of every task
// and grades no work.
func contractAsksForSomethingToJudge(contract OutcomeContract) bool {
	for _, result := range contract.ExpectedResults {
		if result.Required && strings.TrimSpace(result.ID) != finalMessageExpectedResultID {
			return true
		}
	}
	return false
}

// The executor's account of a picture the message came with is the whole record
// of it: no operation reads it, so nothing else can contradict the account. A
// turn holding such a picture is judged even when the contract asks for nothing.
func requestCarriesInputImage(request AgentTurnRequest) bool {
	return len(inputImageContextMessage(request.InputParts).Parts) > 0
}

func (agentTurnRunner *AgentTurnRunner) validateCompletionGateWithJudge(ctx context.Context, taskRunID string, request AgentTurnRequest, requirements []toolUseRequirement, observations []turnObservation, attachments []toolcontract.FileAttachment, criteria []qualityCriterion, actionDocument turnActionDocument) completionGateResult {
	completionGateResult := validateCompletionGateForRequestWithExpectedResults(request, requirements, observations, attachments, criteria, actionDocument, agentTurnRunner.options.RecoveryBudget)
	if !completionGateResult.IsSatisfied || ctx.Err() != nil {
		return completionGateResult
	}
	if !contractAsksForSomethingToJudge(request.OutcomeContract) &&
		!outcomeContractHasSideEffectEvidence(request.ToolSet, request.OutcomeContract) &&
		!outcomeContractHintsSideEffectEvidence(request.ToolSet, request.OutcomeContract) &&
		!observationsIncludeSideEffect(request.ToolSet, observations) &&
		!requestCarriesInputImage(request) {
		return completionGateResult
	}
	if judgeResult := agentTurnRunner.evaluateCompletionJudge(ctx, taskRunID, request, observations, deliveredCompletionAttachments(observations, actionDocument), actionDocument); !judgeResult.IsSatisfied {
		return judgeResult
	}
	return completionGateResult
}

// The judge reads the observation ledger, so a finish that added nothing to it is the same
// finish it already refused. Asking again could only change the answer by chance.
func standingJudgeRejection(observations []turnObservation) (completionGateResult, bool) {
	if len(observations) == 0 {
		return completionGateResult{}, false
	}
	latestObservation := observations[len(observations)-1]
	if latestObservation.Action != "evidence_missing" || latestObservation.PolicyCode != evidenceKindExpectedResult || latestObservation.Failure == nil {
		return completionGateResult{}, false
	}
	if !latestObservation.JudgeNamedMissingWork {
		return completionGateResult{}, false
	}
	return completionGateResult{
		Message:        latestObservation.Failure.UserSafeSummary,
		EvidenceKind:   evidenceKindExpectedResult,
		IsJudgeVerdict: true,
	}, true
}

func (agentTurnRunner *AgentTurnRunner) evaluateCompletionJudge(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, attachments []toolcontract.FileAttachment, actionDocument turnActionDocument) completionGateResult {
	if standingRejection, isStanding := standingJudgeRejection(observations); isStanding {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.standing_verdict", marshalEventBody(map[string]string{"reason": standingRejection.Message}))
		return standingRejection
	}
	if agentTurnRunner.languageModel == nil {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.degraded", marshalEventBody(map[string]string{"error": "completion judge language model was not configured"}))
		return completionGateResult{IsSatisfied: true}
	}
	verdict, isUsable := agentTurnRunner.completionJudgeVerdictFor(ctx, taskRunID, request, observations, attachments, actionDocument, nil)
	if !isUsable {
		return completionGateResult{IsSatisfied: true}
	}
	if requestedIDs := knownObservationIDs(observations, verdict.NeedObservationIDs); len(requestedIDs) > 0 {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.expanded", marshalEventBody(map[string]any{"observationIDs": requestedIDs}))
		if expandedVerdict, isExpandedUsable := agentTurnRunner.completionJudgeVerdictFor(ctx, taskRunID, request, observations, attachments, actionDocument, requestedIDs); isExpandedUsable {
			verdict = expandedVerdict
		}
	}
	if verdict.Satisfied {
		return completionGateResult{IsSatisfied: true}
	}
	return completionGateResult{
		Message:          completionJudgeUnsatisfiedMessage(verdict),
		EvidenceKind:     evidenceKindExpectedResult,
		IsJudgeVerdict:   true,
		NamesMissingWork: len(verdict.MissingWork) > 0,
	}
}

func (agentTurnRunner *AgentTurnRunner) completionJudgeVerdictFor(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, attachments []toolcontract.FileAttachment, actionDocument turnActionDocument, expandedObservationIDs []string) (completionJudgeVerdict, bool) {
	response, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, completionJudgeRequest(request, observations, attachments, actionDocument, expandedObservationIDs))
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.degraded", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return completionJudgeVerdict{}, false
	}
	verdict, errorValue := parseCompletionJudgeVerdict(response.Content)
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, "completion_judge.degraded", marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return completionJudgeVerdict{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, "completion_judge.verdict", marshalEventBody(verdict))
	return verdict, true
}

func knownObservationIDs(observations []turnObservation, requestedObservationIDs []string) []string {
	recordedIDs := map[string]bool{}
	for _, observation := range observations {
		recordedIDs[strings.TrimSpace(observation.ObservationID)] = true
	}
	knownIDs := []string{}
	for _, observationID := range requestedObservationIDs {
		if len(knownIDs) == completionJudgeMaxRequestedObservations {
			break
		}
		if trimmedID := strings.TrimSpace(observationID); recordedIDs[trimmedID] {
			knownIDs = append(knownIDs, trimmedID)
		}
	}
	return knownIDs
}

func parseCompletionJudgeVerdict(content string) (completionJudgeVerdict, error) {
	var verdict completionJudgeVerdict
	if errorValue := json.Unmarshal([]byte(content), &verdict); errorValue != nil {
		return completionJudgeVerdict{}, errorValue
	}
	return verdict, nil
}

func completionJudgeUnsatisfiedMessage(verdict completionJudgeVerdict) string {
	reason := strings.TrimSpace(verdict.Reason)
	if len(verdict.MissingWork) == 0 {
		return reason
	}
	missingWorkText := strings.Join(verdict.MissingWork, "; ")
	if reason == "" {
		return missingWorkText
	}
	return reason + " Missing: " + missingWorkText
}

func completionJudgeRequest(request AgentTurnRequest, observations []turnObservation, attachments []toolcontract.FileAttachment, actionDocument turnActionDocument, expandedObservationIDs []string) model.StructuredResponseRequest {
	return model.StructuredResponseRequest{
		Messages: completionJudgeMessages(request, observations, attachments, actionDocument, expandedObservationIDs),
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               completionJudgeSchemaName,
			Document:           completionJudgeSchema(),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: terminalStructuredGenerationOptions(model.GenerationOptions{}),
	}
}

func completionJudgeMessages(request AgentTurnRequest, observations []turnObservation, attachments []toolcontract.FileAttachment, actionDocument turnActionDocument, expandedObservationIDs []string) []model.Message {
	messages := withoutEmptyMessages([]model.Message{
		{Role: "system", Content: completionJudgeInstruction()},
		{Role: "system", Content: buildTemporalContextDescription(request.EnvironmentNow, request.Company.TimeZone)},
		{Role: "system", Content: "Original instruction:\n" + completionJudgeOriginalInstruction(request)},
	})
	if expectedResultsDescription := completionJudgeExpectedResultsDescription(request.OutcomeContract.ExpectedResults); expectedResultsDescription != "" {
		messages = append(messages, model.Message{Role: "system", Content: "Expected results:\n" + expectedResultsDescription})
	}
	if finishReply := strings.TrimSpace(finishActionMessage(actionDocument)); finishReply != "" {
		messages = append(messages, model.Message{Role: "system", Content: "Finish reply that accepting this completion delivers to the user:\n" + truncateForLedger(finishReply, completionJudgeInputMaxLength)})
	}
	if planContext := completionJudgePlanContext(observations); planContext != "" {
		messages = append(messages, model.Message{Role: "system", Content: planContext})
	}
	if rejectionContext := completionJudgePriorRejections(observations); rejectionContext != "" {
		messages = append(messages, model.Message{Role: "system", Content: rejectionContext})
	}
	messages = append(messages, model.Message{Role: "system", Content: completionJudgeAttachmentDescription(attachments)})
	// The executor says what it made of a picture, and until the judge is shown
	// the same picture it can only take that on trust. One turn said an image
	// held no text and that answer was delivered as the translation somebody
	// asked for.
	if toolResultImages := toolResultImageContextMessage(observations); len(toolResultImages.Parts) > 0 {
		messages = append(messages, toolResultImages)
	}
	if inputImages := inputImageContextMessage(request.InputParts); len(inputImages.Parts) > 0 {
		messages = append(messages, inputImages)
	}
	messages = append(messages, model.Message{Role: "user", Content: "Recorded operations this turn, reads and state changes alike. An entry with failed=true did not do what it attempted:\n" + completionJudgeLedgerDocument(request.ToolSet, observations, fullyShownObservationIDs(actionDocument, expandedObservationIDs))})
	return messages
}

// The ledger records every delivery the turn performed, but only the ones this
// completion cites are actually sent. Judging attachment requirements against
// the ledger therefore fails a turn for files the person never receives, and
// passes one whose cited file is not the deliverable. State the sent set.
func completionJudgeAttachmentDescription(attachments []toolcontract.FileAttachment) string {
	if len(attachments) == 0 {
		return "Files this completion attaches for the user: none."
	}
	filenames := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		filenames = append(filenames, firstNonEmptyString(attachment.Filename, attachment.DevicePath))
	}
	return strings.Join([]string{
		"Files this completion attaches for the user:",
		strings.Join(filenames, "\n"),
		"Judge attachment requirements against exactly this list.",
	}, "\n")
}

// The attachments a finish sends are the ones its completion evidence cites,
// resolved the same way the gate resolves them, so the judge and the delivery
// never disagree about what the person receives.
func deliveredCompletionAttachments(observations []turnObservation, actionDocument turnActionDocument) []toolcontract.FileAttachment {
	return collectReferenceDeliveryAttachments(observations, actionDocument.CompletionEvidence)
}

func completionJudgeInstruction() string {
	return strings.Join([]string{
		"Judge whether the recorded operations actually accomplish the user's original instruction. An operation marked failed=true attempted something and did not do it, so it is not evidence the thing was done.",
		"Judge only from the recorded ledger facts below. The executor's own completion claims are not evidence.",
		"Accepting this completion delivers the finish reply to the user as the task's answer. Content the finish reply itself carries, such as links, results, and answers, is thereby delivered; never require a separate send or delivery operation for it. The reply's claims about operations it performed remain non-evidence and must match the ledger.",
		"When the instruction asks only for words — a greeting, an answer, an explanation, advice — no recorded operation can exist for it: the finish reply is the work itself. Judge whether the reply's content accomplishes the instruction, and require no operation evidence for it.",
		"Mark unsatisfied when the recorded operations do not plausibly accomplish the instruction: wrong target, wrong values, or a missing step.",
		"When the instruction states an explicit deadline, date, time, quantity, title, or recipient, that value must appear in at least one successful recorded operation input; if a stated value appears nowhere and no relevant entry is display-truncated, mark unsatisfied and name exactly that value in missingWork.",
		"When the instruction selects its target by a condition on an attribute — who has no account, which ones are over a count, what was sent this month — a successful recorded operation must show that attribute being read for the candidates. Acting on the unfiltered set is unfinished work: mark unsatisfied and name the condition that was never evaluated. A condition the instruction does not state is not a requirement, and an attribute already visible in a recorded result needs no separate lookup.",
		"When the instruction supplies worked examples — sample inputs with their expected outputs — every one of them must appear in a successful recorded operation, not just the first. Checking one example and generalising from it is unfinished work: mark unsatisfied and name the examples that were never run.",
		"A ledger entry carrying a display-truncated marker was cut for this display only, and earlier operations may be dropped from the top of the display the same way; everything cut or dropped was still recorded and executed. Such content is unknown in both directions: never cite it as missing work, and never treat it as confirming that something was checked, matched, sent, or absent. Judge only from the parts that are visible.",
		"When the verdict turns on content that is cut, dropped, or otherwise not visible, list those entries' observationIDs in needObservationIDs and decide from what is visible for now; they will be shown to you in full exactly once. Leave needObservationIDs empty when the visible parts already decide the verdict.",
		"Resolve relative dates such as today, tomorrow, 오늘, and 내일 only from the runtime temporal context below. Never guess the current date from ledger values.",
		"Judge state changes by the recorded operation results. Items that merely appear inside another result's diagnostic fields, such as candidate lists in a search result, are not additional requirements unless the instruction itself names them.",
		"Images a recorded operation read, and images the user's message came with, are shown to you as image parts. When the finish reply makes a claim about one — that it holds no text, that it shows a particular thing — judge that claim against the image itself, and mark unsatisfied when the image contradicts it. Do not require anything of an image the instruction does not ask for, and an image nobody made a claim about is not a failure.",
		"Do not invent requirements the instruction does not state. Wording, formatting, phrasing, and which list or table a record appears in are not failures. If the right operations ran and every explicitly stated value appears in some recorded input, mark satisfied.",
	}, "\n")
}

func completionJudgePriorRejections(observations []turnObservation) string {
	rejectionReasons := []string{}
	for _, observation := range observations {
		if observation.Action != "evidence_missing" || observation.PolicyCode != evidenceKindExpectedResult || observation.Failure == nil {
			continue
		}
		if reason := strings.TrimSpace(observation.Failure.UserSafeSummary); reason != "" {
			rejectionReasons = appendUniqueStrings(rejectionReasons, truncateForLedger(reason, completionJudgeInputMaxLength))
		}
	}
	if len(rejectionReasons) == 0 {
		return ""
	}
	return strings.Join([]string{
		"Earlier finishes of this task were rejected for these reasons:",
		strings.Join(rejectionReasons, "\n"),
		"Each of these is an open gap. Mark satisfied only when visible ledger entries recorded after the rejection close every one of them; the same state that earned a rejection earns the same rejection again. A gap that later evidence genuinely closes is closed — these reasons are not permanent vetoes.",
	}, "\n")
}

func completionJudgePlanContext(observations []turnObservation) string {
	plan, hasPlan := latestPlanUpdate(observations)
	if !hasPlan || len(plan.Steps) == 0 {
		return ""
	}
	document, errorValue := json.Marshal(plan)
	if errorValue != nil {
		return ""
	}
	return "The model's own step plan is below as a checklist hint; the source of truth is the user's original request — verify every part of the request is satisfied.\n" + string(document)
}

func completionJudgeOriginalInstruction(request AgentTurnRequest) string {
	return firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt)
}

// Delivering a reply is required of every task and accomplishes none of them; listed beside the
// results the instruction asks for, it reads as one of them.
func completionJudgeExpectedResultsDescription(expectedResults []ExpectedResult) string {
	normalizedResults := []ExpectedResult{}
	for _, result := range normalizeExpectedResults(expectedResults) {
		if strings.TrimSpace(result.ID) == finalMessageExpectedResultID {
			continue
		}
		normalizedResults = append(normalizedResults, result)
	}
	if len(normalizedResults) == 0 {
		return ""
	}
	document, errorValue := json.Marshal(normalizedResults)
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func completionJudgeLedgerDocument(toolSet *toolcontract.ToolSet, observations []turnObservation, citedObservationIDs map[string]bool) string {
	document, errorValue := json.Marshal(completionJudgeLedger(toolSet, observations, citedObservationIDs))
	if errorValue != nil {
		return "[]"
	}
	return string(document)
}

func observationsIncludeSideEffect(toolSet *toolcontract.ToolSet, observations []turnObservation) bool {
	for _, observation := range observations {
		if isSideEffectObservation(toolSet, observation) {
			return true
		}
	}
	return false
}

func fullyShownObservationIDs(actionDocument turnActionDocument, expandedObservationIDs []string) map[string]bool {
	shownIDs := citedObservationIDs(actionDocument)
	for _, observationID := range expandedObservationIDs {
		if trimmedID := strings.TrimSpace(observationID); trimmedID != "" {
			shownIDs[trimmedID] = true
		}
	}
	return shownIDs
}

func citedObservationIDs(actionDocument turnActionDocument) map[string]bool {
	citedIDs := map[string]bool{}
	for _, reference := range actionDocument.CompletionEvidence {
		if observationID := strings.TrimSpace(reference.ObservationID); observationID != "" {
			citedIDs[observationID] = true
		}
	}
	return citedIDs
}

// A finish points at the results that prove it, and those are what the judge exists to read.
// Cutting them to the short cap asks it about bytes the runtime removed.
func completionJudgeResultLimit(observation turnObservation, citedObservationIDs map[string]bool) int {
	if citedObservationIDs[strings.TrimSpace(observation.ObservationID)] {
		return completionJudgeCitedResultMaxLength
	}
	return completionJudgeResultMaxLength
}

func completionJudgeLedger(toolSet *toolcontract.ToolSet, observations []turnObservation, citedObservationIDs map[string]bool) []completionLedgerEntry {
	ledger := []completionLedgerEntry{}
	for _, observation := range observations {
		if observation.Action != "continue" || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		ledger = append(ledger, completionLedgerEntry{
			Tool:   strings.TrimSpace(observation.Tool),
			Input:  truncateForLedger(string(observation.ToolInput), completionJudgeInputMaxLength),
			Result: truncateForLedger(observation.ContentText(), completionJudgeResultLimit(observation, citedObservationIDs)),
			Failed: observation.Failed(),
		})
	}
	return newestLedgerEntriesWithinBudget(ledger, completionJudgeLedgerByteBudget)
}

func toolTally(entries []completionLedgerEntry) string {
	counts := map[string]int{}
	order := []string{}
	for _, entry := range entries {
		if counts[entry.Tool] == 0 {
			order = append(order, entry.Tool)
		}
		counts[entry.Tool]++
	}
	parts := []string{}
	for _, toolName := range order {
		parts = append(parts, toolName+" x"+strconv.Itoa(counts[toolName]))
	}
	return strings.Join(parts, ", ")
}

func newestLedgerEntriesWithinBudget(ledger []completionLedgerEntry, byteBudget int) []completionLedgerEntry {
	usedBytes := 0
	keptFromIndex := len(ledger)
	for entryIndex := len(ledger) - 1; entryIndex >= 0; entryIndex-- {
		entry := ledger[entryIndex]
		entryBytes := len(entry.Tool) + len(entry.Input) + len(entry.Result)
		if usedBytes+entryBytes > byteBudget && keptFromIndex < len(ledger) {
			break
		}
		usedBytes += entryBytes
		keptFromIndex = entryIndex
	}
	if keptFromIndex == 0 {
		return ledger
	}
	kept := ledger[keptFromIndex:]
	marker := completionLedgerEntry{
		Tool:   "earlier_operations",
		Result: "…" + strconv.Itoa(keptFromIndex) + " earlier operations (" + toolTally(ledger[:keptFromIndex]) + ") were dropped from this display. What they did is unknown in both directions: do not treat them as having satisfied anything, and do not treat something as never done merely because it is not visible here.",
	}
	return append([]completionLedgerEntry{marker}, kept...)
}

func isSideEffectObservation(toolSet *toolcontract.ToolSet, observation turnObservation) bool {
	toolName := strings.TrimSpace(observation.Tool)
	if toolName == "" || observation.Failed() {
		return false
	}
	return toolcontract.IsArtifactDeliveryTool(toolName) || requiredEvidenceToolNeedsSuccessfulSideEffect(toolSet, toolName)
}

func truncateForLedger(value string, maxLength int) string {
	trimmedValue := strings.TrimSpace(value)
	if len(trimmedValue) <= maxLength {
		return trimmedValue
	}
	headLength := maxLength / 2
	head := strings.ToValidUTF8(trimmedValue[:headLength], "")
	tail := strings.ToValidUTF8(trimmedValue[len(trimmedValue)-(maxLength-headLength):], "")
	return head + " …[display truncated; full " + strconv.Itoa(len(trimmedValue)) + " bytes were recorded and executed]… " + tail
}

func completionJudgeSchema() string {
	document := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"satisfied", "missingWork", "reason", "needObservationIDs"},
		"properties": map[string]any{
			"satisfied": map[string]any{"type": "boolean"},
			"missingWork": map[string]any{
				"type":     "array",
				"maxItems": completionJudgeMaxMissingWork,
				"items":    map[string]any{"type": "string", "maxLength": completionJudgeMissingWorkMaxLength},
			},
			"reason": map[string]any{"type": "string", "maxLength": completionJudgeReasonMaxLength},
			"needObservationIDs": map[string]any{
				"type":     "array",
				"maxItems": completionJudgeMaxRequestedObservations,
				"items":    map[string]any{"type": "string", "maxLength": 64},
			},
		},
	}
	encodedDocument, _ := json.Marshal(document)
	return string(encodedDocument)
}
