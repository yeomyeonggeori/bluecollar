package loop

import (
	"fmt"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type PromptAssembler struct{}

type InjectedContextInput struct {
	BaseInstruction   string
	InstructionPrompt string
	ToolDescription   string
	TurnStartedAt     time.Time
	EnvironmentNow    time.Time
	RuntimeRequest    AgentTurnRequest
	MemoryContext     string
	Observations      []turnObservation
	ExecutionState    ExecutionState

	ToolResultsCarriedNatively bool
}

func BuildInjectedContextMessages(input InjectedContextInput) []model.Message {
	contextInput := LLMContextInput{
		ResponseLanguage:     input.RuntimeRequest.ResponseLanguage,
		RequesterPersonID:    input.RuntimeRequest.RequesterPersonID,
		RequesterName:        input.RuntimeRequest.RequesterName,
		RequesterCallingName: input.RuntimeRequest.RequesterCallingName,
		RequesterEmail:       input.RuntimeRequest.RequesterEmail,
		Company:              input.RuntimeRequest.Company,
		UserPrompt:           input.RuntimeRequest.Prompt,
		InputParts:           append([]AgentPart{}, input.RuntimeRequest.InputParts...),
		TurnStartedAt:        input.TurnStartedAt,
		EnvironmentNow:       input.EnvironmentNow,
		InstructionPrompt:    input.InstructionPrompt,
		ToolDescription:      input.ToolDescription,
		AdditionalToolNames:  droppedExposureToolNames(input.RuntimeRequest.ToolExposure),
		WorkspaceContext: WorkspaceContext{
			RootPath:          input.RuntimeRequest.WorkspaceRootPath,
			DefaultPath:       input.RuntimeRequest.WorkspaceDefaultPath,
			RequesterPersonID: input.RuntimeRequest.RequesterPersonID,
		},
		VisibleContext:             input.RuntimeRequest.VisibleContext,
		MemoryContext:              input.MemoryContext,
		ActiveGoal:                 input.RuntimeRequest.ActiveGoal,
		PriorTask:                  input.RuntimeRequest.PriorTask,
		ScheduledRun:               input.RuntimeRequest.ScheduledRun,
		StepBudgetContext:          input.RuntimeRequest.StepBudgetContext,
		ArtifactManifest:           input.RuntimeRequest.ArtifactManifest,
		Observations:               input.Observations,
		ExecutionState:             input.ExecutionState,
		ToolResultsCarriedNatively: input.ToolResultsCarriedNatively,
		FailureFacts:               buildFailureReportFacts(input.Observations, defaultRecoveryBudget()),
		Attachments:                attachmentsFromObservations(input.Observations),
		ToolSet:                    input.RuntimeRequest.ToolSet,
		RequiredEvidenceTools:      append([]string{}, input.RuntimeRequest.RequiredEvidenceTools...),
		OutcomeContract:            input.RuntimeRequest.OutcomeContract,
	}
	contextBuilder := LLMContextBuilder{}
	return compactMessages([]model.Message{
		systemMessage(input.BaseInstruction),
		systemMessage(contextBuilder.BuildUnchangingContext(contextInput)),
		systemMessage(contextBuilder.BuildChangingContext(contextInput)),
		toolResultImageContextMessage(input.Observations),
	})
}

func (promptAssembler PromptAssembler) BuildTurnMessages(request AgentTurnRequest, observations []turnObservation, baseInstruction string, toolDescription string, executionStates ...ExecutionState) []model.Message {
	return promptAssembler.buildTurnMessages(request, observations, baseInstruction, toolDescription, false, executionStates...)
}

func (promptAssembler PromptAssembler) buildTurnMessages(request AgentTurnRequest, observations []turnObservation, baseInstruction string, toolDescription string, toolResultsCarriedNatively bool, executionStates ...ExecutionState) []model.Message {
	executionState := ExecutionState{}
	if len(executionStates) > 0 {
		executionState = executionStates[0]
	}
	messages := BuildInjectedContextMessages(InjectedContextInput{
		BaseInstruction:   baseInstruction,
		InstructionPrompt: request.InstructionPrompt,
		ToolDescription:   toolDescription,
		TurnStartedAt:     request.TurnStartedAt,
		EnvironmentNow:    request.EnvironmentNow,
		RuntimeRequest:    request,
		MemoryContext:     buildMemoryContext(request.MemoryFacts),
		Observations:      observations,
		ExecutionState:    executionState,

		ToolResultsCarriedNatively: toolResultsCarriedNatively,
	})
	messages = append(messages, userMessageFromPromptAndParts(request.Prompt, request.InputParts))
	return messages
}

func userMessageFromPromptAndParts(prompt string, inputParts []AgentPart) model.Message {
	if len(inputParts) == 0 {
		return model.Message{Role: "user", Content: strings.TrimSpace(prompt)}
	}
	parts := []AgentPart{}
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, TextAgentPart(prompt))
	}
	parts = append(parts, inputParts...)
	llmParts := AgentPartsToLLMParts(parts)
	if len(llmParts) == 0 {
		return model.Message{Role: "user", Content: strings.TrimSpace(prompt)}
	}
	return model.Message{Role: "user", Parts: llmParts}
}

func buildInstructionContext(instructionPrompt string) string {
	if strings.TrimSpace(instructionPrompt) == "" {
		return ""
	}
	return "Workspace instructions and available skill references:\n" + strings.TrimSpace(instructionPrompt)
}

func systemMessage(content string) model.Message {
	return model.Message{Role: "system", Content: strings.TrimSpace(content)}
}

func toolResultContextText(observations []turnObservation) string {
	items := toolResultContextItems(observations)
	if len(items) == 0 {
		return ""
	}
	body := marshalEventBody(items)
	return "Tool result context. This is the model-visible representation of tool outputs; use it for the next action instead of guessing from progress labels:\n" + body
}

func observationsShowingTheirImages(observations []turnObservation) []turnObservation {
	if len(observations) <= maxProgressObservations {
		return observations
	}
	return observations[len(observations)-maxProgressObservations:]
}

func toolResultImageContextMessage(observations []turnObservation) model.Message {
	message := model.Message{Role: "user"}
	for _, observation := range observationsShowingTheirImages(observations) {
		for index, attachment := range observation.Attachments {
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") || strings.TrimSpace(attachment.ContentBase64) == "" {
				continue
			}
			message.Parts = append(message.Parts, model.MessagePart{
				Type:       "image",
				MimeType:   strings.TrimSpace(attachment.ContentType),
				DataBase64: strings.TrimSpace(attachment.ContentBase64),
				Text:       observation.ObservationID + ":" + fmt.Sprintf("%d", index),
			})
		}
	}
	if len(message.Parts) == 0 {
		return model.Message{}
	}
	message.Content = "Tool result images for the next answer. Inspect these image parts directly; do not infer visual details from filenames or progress text."
	return message
}

func compactMessages(messages []model.Message) []model.Message {
	result := []model.Message{}
	for _, message := range messages {
		trimmedContent := strings.TrimSpace(message.Content)
		if trimmedContent == "" && len(message.Parts) == 0 {
			continue
		}
		result = append(result, model.Message{Role: message.Role, Content: trimmedContent, Parts: append([]model.MessagePart{}, message.Parts...)})
	}
	return result
}

func buildWorkspaceContextDescription(request AgentTurnRequest) string {
	if strings.TrimSpace(request.WorkspaceDefaultPath) == "" {
		return ""
	}
	lines := []string{
		"Your shell starts in " + strings.TrimSpace(request.WorkspaceDefaultPath) + " and stays there between commands. You do not need to discover where you are.",
		"Terminal commands run as the requester POSIX identity; ~ is your Linux home ($HOME) and your private workspace, and the same ~ path works in a tool path field and in a shell command.",
		"A concrete POSIX path under your home also resolves, so open one you see in ls output instead of giving up.",
		"If unsure, inspect access from a working directory such as ~: id; pwd; ls -ld .; stat -c '%A %U %G %n' .; test -w .",
	}
	lines = append(lines, nonEmptyStrings(request.WorkspaceGuidance)...)
	return strings.Join(lines, "\n")
}

func defaultTurnLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue != nil {
		return time.Local
	}
	return location
}

func buildObservationContext(observations []turnObservation, toolResultsCarriedNatively bool) string {
	if len(observations) == 0 {
		return ""
	}
	ledger := recentProgressObservations(observations)
	if toolResultsCarriedNatively {
		ledger = withoutSummaries(ledger)
	}
	body := marshalEventBody(ledger)
	if len(body) > progressMessageLimit {
		body = body[:progressMessageLimit] + "\n[trimmed]"
	}
	return "Relevant observation ledger so far. Use observationID/toolName/attachmentIndex when citing completionEvidence:\n" + body
}

func progressLedgerHeading(toolResultsCarriedNatively bool) string {
	if toolResultsCarriedNatively {
		return "Progress ledger. This indexes what has already happened by observationID; each result is on the call that produced it:\n"
	}
	return "Progress ledger. This is the compact source of truth for what has already happened; raw tool output is intentionally omitted:\n"
}

func withoutSummaries(observations []ProgressObservation) []ProgressObservation {
	stripped := make([]ProgressObservation, 0, len(observations))
	for _, observation := range observations {
		observation.Summary = ""
		stripped = append(stripped, observation)
	}
	return stripped
}

func buildProgressContext(request AgentTurnRequest, observations []turnObservation, toolResultsCarriedNatively bool) string {
	progress := buildTurnProgress(request, observations)
	if len(observations) == 0 {
		progress.RemainingWork = "No tool work has been attempted yet."
	}
	if toolResultsCarriedNatively {
		progress.CompletedSteps = withoutSummaries(progress.CompletedSteps)
		progress.FailedOrBlockedSteps = withoutSummaries(progress.FailedOrBlockedSteps)
	}
	body := marshalEventBody(progress)
	if len(body) > progressMessageLimit {
		body = body[:progressMessageLimit] + "\n[trimmed]"
	}
	return progressLedgerHeading(toolResultsCarriedNatively) + body
}
