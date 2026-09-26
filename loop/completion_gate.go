package loop

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type completionEvidenceReference struct {
	ObservationID   string `json:"observationID"`
	ToolName        string `json:"toolName"`
	AttachmentIndex *int   `json:"attachmentIndex,omitempty"`
}

type qualityCriterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type qualityReviewItem struct {
	ID          string                        `json:"id"`
	Passed      bool                          `json:"passed"`
	EvidenceIDs []string                      `json:"evidenceIDs"`
	Evidence    []completionEvidenceReference `json:"-"`
	Notes       string                        `json:"notes,omitempty"`
}

type completionGateResult struct {
	IsSatisfied         bool
	Message             string
	EvidenceKind        string
	Attachments         []toolcontract.FileAttachment
	ValidityState       ValidityState
	SuggestedNextTools  []string
	IsChangeCheckUnmet  bool
	AreChangesConfirmed bool
	PolicyCode          string
}

const policyCodeGoalNotClaimedSatisfied = "goal_not_claimed_satisfied"

const (
	evidenceKindExpectedResult   = "expected_result_missing"
	evidenceKindRequiredTool     = "required_tool_missing"
	evidenceKindAttachment       = "attachment_missing"
	evidenceKindAttachmentValid  = "attachment_invalid"
	evidenceKindReference        = "evidence_reference_invalid"
	completionReplySchemaName    = "bluecollar_completion_reply"
	completionPersistenceTimeout = 5 * time.Second
)

func canDeliverBestEffortOnUnmetChanges(ctx context.Context, completionGateResult completionGateResult, reply string) bool {
	return completionGateResult.IsChangeCheckUnmet && ctx.Err() != nil && strings.TrimSpace(reply) != ""
}

func (agentTurnRunner *AgentTurnRunner) completeTaskRunBestEffort(ctx context.Context, taskRunID string, taskStepID string, stepAction string, request AgentTurnRequest, observations []turnObservation, completionGateResult completionGateResult, reply string) AgentTurnResult {
	detachedContext, cancelDetached := context.WithTimeout(context.WithoutCancel(ctx), completionPersistenceTimeout)
	defer cancelDetached()
	finalReply := agentTurnRunner.prepareFinishMessageForPlatform(detachedContext, request, reply)
	agentTurnRunner.saveStep(taskRunID, taskStepID, agentcontract.TaskStatusCompleted, stepAction, finalReply)
	result := agentTurnRunner.finishedTurnResult(taskRunID, finalReply, completionGateResult.Attachments)
	result.RecoveryActions = recoveryActionsFromObservations(observations)
	return result
}

func generateCompletionReply(ctx context.Context, chatCompleter model.ChatCompleter, request AgentTurnRequest, observations []turnObservation) (string, error) {
	response, errorValue := chatCompleter.GenerateChatCompletion(ctx, model.ChatCompletionRequest{
		SchemaName: completionReplySchemaName,
		Messages: []model.ChatCompletionMessage{{
			Role:    "user",
			Content: buildCompletionReplyPrompt(request, observations),
		}},
	})
	if errorValue != nil {
		return "", errorValue
	}
	return model.ChatCompletionText(response)
}

func buildCompletionReplyPrompt(request AgentTurnRequest, observations []turnObservation) string {
	return strings.Join([]string{
		"Write the final user-facing reply for a request whose required result is complete.",
		responseLanguageInstruction(request.ResponseLanguage),
		"State only what the successful evidence proves. Do not mention tools, evidence identifiers, prompts, or runtime details.",
		"Original request:\n" + completionReplyOriginalRequest(request),
		"Successful evidence:\n" + buildLimitObservationSummary(successfulToolObservations(observations)),
	}, "\n\n")
}

func completionReplyOriginalRequest(request AgentTurnRequest) string {
	return firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt)
}

func appendObservationAttachments(attachments []toolcontract.FileAttachment, observation turnObservation) []toolcontract.FileAttachment {
	if observation.Failed() || len(observation.Attachments) == 0 {
		return attachments
	}
	nextAttachments := append([]toolcontract.FileAttachment{}, attachments...)
	if observation.Tool == "browser_screenshot" {
		nextAttachments = removeBrowserScreenshotAttachments(nextAttachments)
	}
	for _, attachment := range observation.Attachments {
		if strings.TrimSpace(attachment.DevicePath) == "" || hasAttachmentDevicePath(nextAttachments, attachment.DevicePath) {
			continue
		}
		nextAttachments = append(nextAttachments, attachment)
	}
	return nextAttachments
}

func removeBrowserScreenshotAttachments(attachments []toolcontract.FileAttachment) []toolcontract.FileAttachment {
	filteredAttachments := []toolcontract.FileAttachment{}
	for _, attachment := range attachments {
		if strings.HasPrefix(strings.TrimSpace(attachment.Filename), "browser-screenshot-") {
			continue
		}
		filteredAttachments = append(filteredAttachments, attachment)
	}
	return filteredAttachments
}

func hasAttachmentDevicePath(attachments []toolcontract.FileAttachment, devicePath string) bool {
	normalizedDevicePath := strings.TrimSpace(devicePath)
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.DevicePath) == normalizedDevicePath {
			return true
		}
	}
	return false
}

func validateCompletionFacts(request AgentTurnRequest, observations []turnObservation, actionDocument turnActionDocument) completionGateResult {
	if result := validateFinishClaim(actionDocument); !result.IsSatisfied {
		return result
	}
	attachments, errorValue := validateCompletionEvidence(request.ToolSet, observations, actionDocument.CompletionEvidence)
	if errorValue != nil {
		return completionGateResult{Message: errorValue.Error(), EvidenceKind: evidenceKindReference}
	}
	if len(attachments) == 0 {
		attachments = deliveredAttachments(observations)
	}
	if message := missingObservedURLInReply(request.ToolSet, observations, finishActionMessage(actionDocument)); message != "" {
		return completionGateResult{Message: message, EvidenceKind: evidenceKindExpectedResult}
	}
	result := completionGateResult{IsSatisfied: true, Attachments: attachments}
	result.ValidityState = buildAttachmentValidityState(request.WorkspaceRootPath, attachments)
	if !result.ValidityState.Passed {
		result.IsSatisfied = false
		result.Message = validityFailureMessage(result.ValidityState)
		result.EvidenceKind = evidenceKindAttachmentValid
		result.Attachments = nil
	}
	return result
}

func validateFinishClaim(actionDocument turnActionDocument) completionGateResult {
	if actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied {
		return completionGateResult{Message: "a final reply requires goalSatisfied=true", PolicyCode: policyCodeGoalNotClaimedSatisfied}
	}
	if strings.TrimSpace(actionDocument.GoalStatus) != "" && strings.TrimSpace(actionDocument.GoalStatus) != "satisfied" {
		return completionGateResult{Message: "a final reply requires goalStatus=satisfied"}
	}
	if actionDocument.HasRemainingWork {
		return completionGateResult{Message: "a final reply requires hasRemainingWork=false; recover the work or use fail"}
	}
	return completionGateResult{IsSatisfied: true}
}

func contractReducedToCallableTools(toolSet *toolcontract.ToolSet, contract OutcomeContract) OutcomeContract {
	contract.RequiredEvidenceTools = callableToolNames(toolSet, contract.RequiredEvidenceTools)
	anyOfGroups := [][]string{}
	for _, toolNames := range contract.RequiredEvidenceAnyOf {
		if callable := callableToolNames(toolSet, toolNames); len(callable) > 0 {
			anyOfGroups = append(anyOfGroups, callable)
		}
	}
	contract.RequiredEvidenceAnyOf = anyOfGroups
	if isToolCallable(toolSet, toolcontract.FileDeliverToolName) {
		return contract
	}
	if contract.ArtifactRequirement == ArtifactRequirementRequired {
		contract.ArtifactRequirement = ArtifactRequirementPreferred
	}
	contract.RequiredAttachmentSuffixes = nil
	contract.ExpectedResults = expectedResultsWithFilesNoLongerRequired(contract.ExpectedResults)
	return contract
}

func expectedResultsWithFilesNoLongerRequired(expectedResults []ExpectedResult) []ExpectedResult {
	relaxed := make([]ExpectedResult, 0, len(expectedResults))
	for _, expectedResult := range expectedResults {
		if expectedResult.Type == ExpectedResultTypeFile {
			expectedResult.Required = false
		}
		relaxed = append(relaxed, expectedResult)
	}
	return relaxed
}

func callableToolNames(toolSet *toolcontract.ToolSet, toolNames []string) []string {
	callable := []string{}
	for _, toolName := range toolNames {
		if isToolCallable(toolSet, toolName) {
			callable = append(callable, toolName)
		}
	}
	return callable
}

func isToolCallable(toolSet *toolcontract.ToolSet, toolName string) bool {
	if toolSet == nil {
		return true
	}
	return toolSet.IsRegistered(strings.TrimSpace(toolName))
}

func expectedResultRequiresFileAttachment(contract OutcomeContract) bool {
	if strings.TrimSpace(contract.ArtifactRequirement) == ArtifactRequirementRequired {
		return true
	}
	if len(contract.RequiredAttachmentSuffixes) > 0 {
		return true
	}
	for _, result := range normalizeExpectedResults(contract.ExpectedResults) {
		if result.Required && result.Type == ExpectedResultTypeFile {
			return true
		}
	}
	return false
}

func externalSendCompletionEvidenceRequired(request AgentTurnRequest) bool {
	return contractRequiresSendTool(request.ToolSet, request.OutcomeContract) ||
		sendToolNamesContain(request.ToolSet, request.RequiredEvidenceTools)
}

func sendToolNamesContain(toolSet *toolcontract.ToolSet, toolNames []string) bool {
	for _, toolName := range toolNames {
		if isSendEvidenceTool(toolSet, toolName) {
			return true
		}
	}
	return false
}

func requiredSendToolNamesForRequest(request AgentTurnRequest) []string {
	toolNames := sendEvidenceToolsFromValues(request.ToolSet, request.RequiredEvidenceTools)
	if len(toolNames) > 0 {
		return toolNames
	}
	toolNames = sendEvidenceToolsFromValues(request.ToolSet, outcomeContractRequiredToolNames(request.OutcomeContract))
	if len(toolNames) > 0 {
		return toolNames
	}
	toolNames = sendEvidenceToolsFromValues(request.ToolSet, request.OutcomeContract.SelectedEvidenceHints)
	if len(toolNames) > 0 {
		return toolNames
	}
	toolNames = singleAvailableSendEvidenceTool(request.ToolSet)
	if len(toolNames) > 0 {
		return toolNames
	}
	return []string{"message_send"}
}

func completionGateObservation(index int, result completionGateResult, toolSet *toolcontract.ToolSet, priorObservations []turnObservation) turnObservation {
	message := strings.TrimSpace(result.Message)
	evidenceKind := strings.TrimSpace(result.EvidenceKind)
	if evidenceKind == "" {
		policyObservation := newFailureObservation(nextObservationID(index), "policy", "", message, toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "completion_gate")
		policyObservation.PolicyCode = strings.TrimSpace(result.PolicyCode)
		return policyObservation
	}
	content := evidenceMissingGuidance(evidenceKind, message) + observedRealityStatement(toolSet, priorObservations)
	observation := newFailureObservation(nextObservationID(index), "evidence_missing", "", message, toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, evidenceKind)
	observation = withObservationContent(observation, content)
	observation.Summary = content
	observation.PolicyCode = evidenceKind
	observation.RelatedPaths = invalidValidityPaths(result.ValidityState)
	observation.Failure.Retryable = true
	observation.Failure.SafeRetry = true
	return observation
}

func observedRealityStatement(toolSet *toolcontract.ToolSet, observations []turnObservation) string {
	successfulToolCount := 0
	recordedEffects := []string{}
	for _, observation := range observations {
		if observation.Failed() || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		successfulToolCount++
		if isSideEffectObservation(toolSet, observation) {
			recordedEffects = appendUniqueStrings(recordedEffects, observation.ObservationID+" "+strings.TrimSpace(observation.Tool))
		}
	}
	if successfulToolCount == 0 {
		return " Recorded reality: this task has ZERO successful tool observations. The requested outcome is not verified. Failed calls may have changed state before reporting an error. Inspect current state before repeating a write, or report the blocker and uncertainty if verification is unavailable."
	}
	if len(recordedEffects) == 0 {
		return ""
	}
	return " Recorded reality: these calls already changed something and are not undone by this refusal: " +
		strings.Join(recordedEffects, ", ") +
		". Repeating one of them makes the change twice. What is missing is the evidence, not the work."
}

func invalidValidityPaths(state ValidityState) []string {
	paths := []string{}
	for _, artifact := range state.InvalidArtifacts {
		paths = appendUniqueStrings(paths, artifact.RelativePath, artifact.Filename)
	}
	return paths
}

func withCompletionGateRecoveryPacket(observation turnObservation, result completionGateResult) turnObservation {
	if strings.TrimSpace(result.Message) == "" && len(result.SuggestedNextTools) == 0 {
		return observation
	}
	observation.RecoveryPacket = &RecoveryPacket{
		WhatFailed:       "Expected task result is not satisfied yet.",
		WhyLikely:        result.Message,
		FailureClass:     failureClassUnknown,
		RetryPolicy:      retryPolicyAfterPrecondition,
		AllowedTools:     appendUniqueStrings(result.SuggestedNextTools),
		EvidenceNeeded:   expectedResultRecoveryEvidence(result),
		MustDoNext:       []string{"Produce or inspect the missing expected result, then try a final reply again."},
		ForbiddenRepeats: nil,
	}
	return observation
}

func expectedResultRecoveryEvidence(result completionGateResult) []string {
	return []string{result.Message}
}

func completionGateEventName(observation turnObservation) string {
	if observation.Action == "evidence_missing" {
		return agentcontract.TaskEventAgentEvidenceMissing
	}
	return agentcontract.TaskEventAgentCompletionRequired
}

func evidenceMissingGuidance(evidenceKind string, message string) string {
	switch evidenceKind {
	case "expected_result_missing":
		return "The Task expected result is not complete yet. Produce or inspect the missing result, then send a final reply with exact typed delivery evidence. " + message
	case "required_tool_missing":
		return "The final reply needs successful tool evidence before completion. Use the required tool if it has not run, or cite an existing successful observation. " + message
	case "attachment_missing":
		return "The final reply needs an attached artifact before completion. Find or create the artifact, then name its path in the final reply's attachments. " + message
	case "attachment_invalid":
		return "The final reply needs valid attachment evidence. Recheck the artifact path and required suffix, then attach a valid file. " + message
	case "evidence_reference_invalid":
		return "The final reply cited missing or failed evidence. Cite only existing successful observations, or run the missing tool first. " + message
	default:
		return message
	}
}

func validateCompletionEvidence(toolSet *toolcontract.ToolSet, observations []turnObservation, references []completionEvidenceReference) ([]toolcontract.FileAttachment, error) {
	if errorValue := validateCompletionEvidenceReferences(toolSet, observations, references); errorValue != nil {
		return nil, errorValue
	}
	return collectReferenceDeliveryAttachments(observations, references), nil
}

func validateCompletionEvidenceReferences(toolSet *toolcontract.ToolSet, observations []turnObservation, references []completionEvidenceReference) error {
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound || !observationSatisfiesEvidenceCondition(toolSet, observation) {
			return errors.New("completionEvidence cites " + citedReferenceDescription(reference) +
				", which is not a successful observation of this task. The observation ledger above says what each of these did; cite one of them: " + strings.Join(citableEvidenceDescriptions(toolSet, observations), ", "))
		}
	}
	return nil
}

func citedReferenceDescription(reference completionEvidenceReference) string {
	described := strings.TrimSpace(reference.ObservationID)
	if described == "" {
		described = "an observation with no observationID"
	}
	if toolName := strings.TrimSpace(reference.ToolName); toolName != "" {
		described += " from " + toolName
	}
	return described
}

func citableEvidenceDescriptions(toolSet *toolcontract.ToolSet, observations []turnObservation) []string {
	descriptions := []string{}
	for _, observation := range observations {
		if observation.Failed() || !observationSatisfiesEvidenceCondition(toolSet, observation) {
			continue
		}
		descriptions = append(descriptions, strings.TrimSpace(observation.ObservationID)+" from "+strings.TrimSpace(observation.Tool))
	}
	if len(descriptions) == 0 {
		return []string{"no successful observation yet"}
	}
	return descriptions
}

func findSuccessfulObservation(observations []turnObservation, reference completionEvidenceReference) (turnObservation, bool) {
	for _, observation := range observations {
		if observation.Failed() {
			continue
		}
		if strings.TrimSpace(observation.ObservationID) != strings.TrimSpace(reference.ObservationID) {
			continue
		}
		if strings.TrimSpace(reference.ToolName) != "" && !toolcontract.ToolNamesMatch(observation.Tool, reference.ToolName) {
			continue
		}
		return observation, true
	}
	return turnObservation{}, false
}

func collectReferenceDeliveryAttachments(observations []turnObservation, references []completionEvidenceReference) []toolcontract.FileAttachment {
	attachments := []toolcontract.FileAttachment{}
	for _, reference := range references {
		observation, isFound := findSuccessfulObservation(observations, reference)
		if !isFound || !toolProducesDeliveryAttachments(observation.Tool) {
			continue
		}
		attachments = appendUniqueAttachments(attachments, attachmentsForReference(observation, reference))
	}
	return attachments
}

func deliveredAttachments(observations []turnObservation) []toolcontract.FileAttachment {
	attachments := []toolcontract.FileAttachment{}
	for _, observation := range observations {
		if observation.Failed() || !toolProducesDeliveryAttachments(observation.Tool) {
			continue
		}
		attachments = appendUniqueAttachments(attachments, observation.Attachments)
	}
	return attachments
}

func toolProducesDeliveryAttachments(toolName string) bool {
	if toolcontract.IsArtifactDeliveryTool(toolName) {
		return true
	}
	return strings.TrimSpace(toolName) == "browser_screenshot"
}

func attachmentsForReference(observation turnObservation, reference completionEvidenceReference) []toolcontract.FileAttachment {
	if reference.AttachmentIndex == nil {
		return observation.Attachments
	}
	index := *reference.AttachmentIndex
	if index < 0 || index >= len(observation.Attachments) {
		return nil
	}
	return []toolcontract.FileAttachment{observation.Attachments[index]}
}

func observationActionCounts(observations []turnObservation) map[string]int {
	counts := map[string]int{}
	for _, observation := range observations {
		action := strings.TrimSpace(observation.Action)
		if action == "" {
			action = "unknown"
		}
		counts[action]++
	}
	return counts
}

func observationToolCounts(observations []turnObservation) map[string]int {
	counts := map[string]int{}
	for _, observation := range observations {
		toolName := strings.TrimSpace(observation.Tool)
		if toolName == "" {
			continue
		}
		counts[toolName]++
	}
	return counts
}

func appendUniqueAttachments(attachments []toolcontract.FileAttachment, candidates []toolcontract.FileAttachment) []toolcontract.FileAttachment {
	nextAttachments := append([]toolcontract.FileAttachment{}, attachments...)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.DevicePath) == "" || hasAttachmentDevicePath(nextAttachments, candidate.DevicePath) {
			continue
		}
		nextAttachments = append(nextAttachments, candidate)
	}
	return nextAttachments
}
