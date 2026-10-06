package loop

import (
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type IntakeFailureReportInput struct {
	OriginalRequest           string
	ResponseLanguage          string
	DiagnosticEventID         string
	PlannedInterpretation     string
	UnverifiedUserFacingReply string
	Classification            agentcontract.IntakeClassification
	TaskShape                 agentcontract.TaskShape
	MaxIterationCount         int
	MaxToolCallCount          int
	MaxElapsedSecond          int
	ElapsedSecond             float64
	CarriedOutToolNames       []string
	PriorTaskID               string
	PriorTaskStatus           string
	PriorTaskResult           string
	PriorTaskFailureReason    string
}

func BuildIntakeFailureReport(input IntakeFailureReportInput) agentcontract.FailureReport {
	return agentcontract.FailureReport{
		Phase:              "limit",
		StopReason:         "max_elapsed",
		SafeFailureSummary: "Execution time limit reached during request intake; the execution loop did not begin.",
		RawError:           ElapsedLimitRawErrorSummary,
		OriginalRequest:    strings.TrimSpace(input.OriginalRequest),
		ResponseLanguage:   input.ResponseLanguage,
		DiagnosticEventID:  strings.TrimSpace(input.DiagnosticEventID),
		IntakeFacts: &agentcontract.IntakeFailureFacts{
			PlannedInterpretation:     strings.TrimSpace(input.PlannedInterpretation),
			UnverifiedUserFacingReply: strings.TrimSpace(input.UnverifiedUserFacingReply),
			Classification:            input.Classification,
			TaskShape:                 input.TaskShape,
			MaxIterationCount:         input.MaxIterationCount,
			MaxToolCallCount:          input.MaxToolCallCount,
			MaxElapsedSecond:          input.MaxElapsedSecond,
			ElapsedSecond:             input.ElapsedSecond,
			CarriedOutToolNames:       append([]string{}, input.CarriedOutToolNames...),
			PriorTaskID:               strings.TrimSpace(input.PriorTaskID),
			PriorTaskStatus:           strings.TrimSpace(input.PriorTaskStatus),
			PriorTaskResult:           strings.TrimSpace(input.PriorTaskResult),
			PriorTaskFailureReason:    strings.TrimSpace(input.PriorTaskFailureReason),
		},
	}
}

const ElapsedLimitRawErrorSummary = "Execution time limit reached."

func BuildElapsedLimitRawErrorFailureNotice(request agentcontract.AgentTurnRequest) agentcontract.FailureNotice {
	report := agentcontract.FailureReport{
		Phase:            "limit",
		StopReason:       "max_elapsed",
		RawError:         ElapsedLimitRawErrorSummary,
		ResponseLanguage: request.ResponseLanguage,
		OriginalRequest:  request.Prompt,
	}
	return agentcontract.BuildRawErrorFailureNotice(report)
}

func BuildFinishMessageCompressionPrompt(reply string, responseLanguage string, maximumCharacters int) string {
	return strings.Join([]string{
		"You are compressing a successful user-facing reply for a chat message.",
		agentcontract.ResponseLanguageInstruction(responseLanguage),
		"Keep the concrete result, attachment filenames, and next useful action if present.",
		"Do not add claims that were not in the original reply.",
		"Write a concise reply under the character limit.",
		"Maximum characters: " + strconv.Itoa(maximumCharacters),
		"Original reply:\n" + strings.TrimSpace(reply),
	}, "\n\n")
}
