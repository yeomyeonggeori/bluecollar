package intake

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func conformanceDecisionRequest() agentcontract.IntakeDecisionRequest {
	request := addressedDecisionRequest("보고서 정리해줘")
	request.PendingConfirmation = agentcontract.PendingConfirmationContext{TaskRunID: "task-run-1", Question: "삭제할까요?"}
	request.ActiveTask = agentcontract.ActiveTaskContext{TaskRunID: "task-run-2", Prompt: "보고서 정리"}
	return request
}

func questionOptionNames(t *testing.T, question model.DecisionQuestion) []string {
	t.Helper()
	criteria, isChoice := question.Criteria.(map[string]string)
	if !isChoice {
		t.Fatalf("expected a choice question, got %+v", question)
	}
	optionNames := []string{}
	for optionName := range criteria {
		optionNames = append(optionNames, optionName)
	}
	return optionNames
}

func TestEveryChoiceQuestionOffersExactlyTheValuesTheRuntimeAccepts(t *testing.T) {
	questions := questionsFor(conformanceDecisionRequest())
	acceptedSets := []struct {
		questionName    string
		acceptedOptions []string
		isAccepted      func(string) bool
	}{
		{
			questionName: agentcontract.IntakeQuestionRoute,
			acceptedOptions: []string{
				string(agentcontract.TurnRouteConsume), string(agentcontract.TurnRouteAnswerQuestion), string(agentcontract.TurnRouteAnswerMeta),
				string(agentcontract.TurnRouteClarify), string(agentcontract.TurnRouteStartTask), string(agentcontract.TurnRouteContinueTask),
				string(agentcontract.TurnRouteReviseTask), string(agentcontract.TurnRouteGiveUp),
			},
			isAccepted: func(option string) bool { return normalizeTurnRoute(agentcontract.TurnRoute(option)) != "" },
		},
		{
			questionName: agentcontract.IntakeQuestionClassification,
			acceptedOptions: []string{
				string(agentcontract.IntakeClassificationQuickReply), string(agentcontract.IntakeClassificationBoundedTask),
				string(agentcontract.IntakeClassificationNeedsConfirmation), string(agentcontract.IntakeClassificationUnsupported),
			},
			isAccepted: func(option string) bool {
				return agentcontract.NormalizeIntakeClassification(agentcontract.IntakeClassification(option)) != ""
			},
		},
		{
			questionName: agentcontract.IntakeQuestionTaskShape,
			acceptedOptions: []string{
				string(agentcontract.TaskShapeImmediateReply), string(agentcontract.TaskShapeResearchTask), string(agentcontract.TaskShapeMaintenanceTask),
				string(agentcontract.TaskShapeScheduledTask), string(agentcontract.TaskShapeBrowserHandoffTask), string(agentcontract.TaskShapeApprovalGatedTask),
			},
			isAccepted: func(option string) bool { return normalizeTaskShape(agentcontract.TaskShape(option)) != "" },
		},
		{
			questionName:    agentcontract.IntakeQuestionLevel,
			acceptedOptions: []string{string(agentcontract.TaskLevelLow), string(agentcontract.TaskLevelMedium), string(agentcontract.TaskLevelHigh)},
			isAccepted:      func(option string) bool { return agentcontract.NormalizeTaskLevel(option) != "" },
		},
		{
			questionName: agentcontract.IntakeQuestionDeliverableKind,
			acceptedOptions: []string{
				string(agentcontract.DeliverableKindWebsite), string(agentcontract.DeliverableKindPresentation),
				string(agentcontract.DeliverableKindDocument), string(agentcontract.DeliverableKindNone),
			},
			isAccepted: func(string) bool { return true },
		},
		{
			questionName: agentcontract.IntakeQuestionApproval,
			acceptedOptions: []string{
				string(agentcontract.ApprovalSignalApprove), string(agentcontract.ApprovalSignalApproveTask),
				string(agentcontract.ApprovalSignalReject), string(agentcontract.ApprovalSignalUnclear),
			},
			isAccepted: func(option string) bool {
				signal := agentcontract.ApprovalSignal(option)
				normalizedSignal := normalizeApprovalSignal(&signal, true)
				return normalizedSignal != nil && *normalizedSignal == signal
			},
		},
		{
			questionName: agentcontract.IntakeQuestionBusyRoute,
			acceptedOptions: []string{
				string(agentcontract.BusyRouteStatus), string(agentcontract.BusyRouteSteer), string(agentcontract.BusyRouteReplace),
				string(agentcontract.BusyRouteCancel), string(agentcontract.BusyRouteNewTask), string(agentcontract.BusyRouteUnrelated),
			},
			isAccepted: func(option string) bool { return isValidBusyRoute(agentcontract.BusyRoute(option)) },
		},
		{
			questionName:    agentcontract.IntakeQuestionPriorTaskReference,
			acceptedOptions: []string{string(agentcontract.PriorTaskReferenceOutcomeRecovery), string(agentcontract.PriorTaskReferenceNone)},
			isAccepted: func(option string) bool {
				return agentcontract.NormalizePriorTaskReference(agentcontract.PriorTaskReference(option)) == agentcontract.PriorTaskReference(option)
			},
		},
	}

	for _, acceptedSet := range acceptedSets {
		question, isAsked := questions["m1."+acceptedSet.questionName]
		if !isAsked {
			t.Fatalf("expected %s to be asked", acceptedSet.questionName)
		}
		offeredOptions := map[string]bool{}
		for _, optionName := range questionOptionNames(t, question) {
			offeredOptions[optionName] = true
			if !acceptedSet.isAccepted(optionName) {
				t.Fatalf("%s offers %q, which the runtime does not accept", acceptedSet.questionName, optionName)
			}
		}
		for _, acceptedOption := range acceptedSet.acceptedOptions {
			if !offeredOptions[acceptedOption] {
				t.Fatalf("%s accepts %q but does not offer it", acceptedSet.questionName, acceptedOption)
			}
		}
		if len(offeredOptions) != len(acceptedSet.acceptedOptions) {
			t.Fatalf("%s offers %d options for %d accepted values", acceptedSet.questionName, len(offeredOptions), len(acceptedSet.acceptedOptions))
		}
	}
}

func TestTheOutputFormatQuestionsAreTheFormatsTheRuntimeAccepts(t *testing.T) {
	questions := questionsFor(addressedDecisionRequest("보고서 정리해줘"))
	askedFormats := []string{}
	for questionName := range questions {
		if formatName, isFormat := strippedQuestionPrefix(questionName, "m1."+agentcontract.IntakeQuestionPrefixFormat); isFormat {
			askedFormats = append(askedFormats, formatName)
			if !agentcontract.IsRequestedOutputFormatName(formatName) {
				t.Fatalf("the decision asks about %q, which the runtime does not accept as an output format", formatName)
			}
		}
	}
	if len(askedFormats) != len(agentcontract.RequestedOutputFormatNames) {
		t.Fatalf("expected one question per accepted output format, got %v", askedFormats)
	}
}

func strippedQuestionPrefix(value string, prefix string) (string, bool) {
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return "", false
	}
	return value[len(prefix):], true
}
