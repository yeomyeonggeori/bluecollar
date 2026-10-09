package intake

import (
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type questionBuilder struct {
	request   turnclassification.IntakeDecisionRequest
	toolNames []string
}

func newQuestionBuilder(request turnclassification.IntakeDecisionRequest) questionBuilder {
	return questionBuilder{request: request, toolNames: request.CallableToolNames}
}

func (builder questionBuilder) questionsWithoutTools() map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for index := range builder.request.Messages {
		messageKey := decisionMessageKey(index)
		for name, question := range builder.routerQuestions(messageKey) {
			questions[messageKey+"."+name] = question
		}
	}
	return questions
}

func (builder questionBuilder) toolQuestions(messageKeys []string, toolNames []string) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for _, messageKey := range messageKeys {
		for _, toolName := range toolNames {
			questions[toolQuestionName(messageKey, toolName)] = builder.likelyToolQuestion(messageKey, toolName)
		}
	}
	return questions
}

func (builder questionBuilder) singleToolChoiceQuestion(messageKey string, tools []decisionTool) model.DecisionQuestion {
	optionDescriptions := map[string]string{
		agentcontract.IntakeChoiceOptionNone: "no tool in the catalog does what the work needs",
	}
	for _, tool := range tools {
		optionDescriptions[tool.Name] = tool.Description
	}
	return model.ChoiceQuestion{
		Instructions:       builder.about(messageKey) + "Which one tool will the work call?",
		OptionDescriptions: optionDescriptions,
	}.Question()
}

func singleToolChoiceQuestionName(messageKey string) string {
	return messageKey + "." + agentcontract.IntakeQuestionSingleToolChoice
}

func toolQuestionName(messageKey string, toolName string) string {
	return messageKey + "." + agentcontract.IntakeQuestionPrefixTool + toolName
}

const plainMessagePreambleEnding = " in the state. "

const firingMessagePreambleEnding = " in the state, which is the schedule in scheduledRun firing now: " + agentcontract.ScheduledRunReading + " "

func (builder questionBuilder) about(messageKey string) string {
	return "About message " + messageKey + builder.messagePreambleEnding()
}

func (builder questionBuilder) messagePreambleEnding() string {
	if builder.request.ScheduledRun.IsEmpty() {
		return plainMessagePreambleEnding
	}
	return firingMessagePreambleEnding
}

func (builder questionBuilder) agentName() string {
	return builder.request.AgentIdentity.DisplayName()
}

func optionDescriptions(optionNames []string, descriptionsByName map[string]string) map[string]string {
	descriptions := map[string]string{}
	for _, optionName := range optionNames {
		description := descriptionsByName[optionName]
		if description == "" {
			description = optionName
		}
		descriptions[optionName] = description
	}
	return descriptions
}

func (builder questionBuilder) routerQuestions(messageKey string) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{
		agentcontract.IntakeQuestionRoute:                   builder.routeQuestion(messageKey),
		agentcontract.IntakeQuestionExpectedToolCount:       builder.expectedToolCountQuestion(messageKey),
		agentcontract.IntakeQuestionHasIndependentWork:      builder.hasIndependentWorkQuestion(messageKey),
		agentcontract.IntakeQuestionIsExternalSendRequested: builder.isExternalSendRequestedQuestion(messageKey),
		agentcontract.IntakeQuestionTaskShape:               builder.taskShapeQuestion(messageKey),
		agentcontract.IntakeQuestionLevel:                   builder.levelQuestion(messageKey),
		agentcontract.IntakeQuestionDeliverableKind:         builder.deliverableKindQuestion(messageKey),
	}
	if needsResponseLanguage(builder.request) {
		questions[agentcontract.IntakeQuestionResponseLanguage] = builder.responseLanguageQuestion(messageKey)
	}
	if hasPriorTask(builder.request) {
		questions[agentcontract.IntakeQuestionPriorTaskReference] = builder.priorTaskReferenceQuestion(messageKey)
	}
	for _, formatName := range turnclassification.RequestedOutputFormatNames {
		questions[agentcontract.IntakeQuestionPrefixFormat+formatName] = builder.outputFormatQuestion(messageKey, formatName)
	}
	return questions
}

func (builder questionBuilder) routeQuestion(messageKey string) model.DecisionQuestion {
	agentName := builder.agentName()
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "What should " + agentName + " do about it? The latest message is authoritative; earlier context only helps read it. When activeGoal is in the state the message is input to that goal unless it plainly starts something unrelated.",
		OptionDescriptions: optionDescriptions(agentcontract.TurnRouteNames, map[string]string{
			string(agentcontract.TurnRouteAnswerQuestion): "answer in words right now, from common knowledge, judgment, or what is visible",
			string(agentcontract.TurnRouteAnswerMeta):     "answer a question about " + agentName + " itself: what it can do, how it works, what it is",
			string(agentcontract.TurnRouteClarify):        "ask one clarifying question first only when the requested goal, target, or outcome is still ambiguous after using visible context and only the sender can resolve it. Do not block on operational details, approval roles, or requirements a tool can inspect or resolve; start work and let the execution loop discover those. Never to ask for approval.",
			string(agentcontract.TurnRouteStartTask):      "start work that takes tools and time",
			string(agentcontract.TurnRouteContinueTask):   "add to, or approve, work already running",
			string(agentcontract.TurnRouteReviseTask):     "redirect work already running toward a changed target or scope",
			string(agentcontract.TurnRouteGiveUp):         "say it cannot be done: physically impossible, nonsensical, or plainly improper on its face. Never for a permission concern, which the operating system decides at execution",
		}),
	}.Question()
}

func (builder questionBuilder) hasIndependentWorkQuestion(messageKey string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions:     builder.about(messageKey) + "Can any independently requested part proceed now, even if another part needs clarification?",
		TrueDescription:  "at least one separable part is explicitly requested, has a clear target and effect, and does not depend on the unresolved answer",
		FalseDescription: "no actionable work was requested, all requested work depends on the unresolved answer, or the apparent first step is only a prerequisite, operational detail, or action the requester did not authorize",
	}.Question()
}

func (builder questionBuilder) expectedToolCountQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "How many tools will doing what it asks call before the work is done?",
		OptionDescriptions: map[string]string{
			string(agentcontract.ExpectedToolCountNone):    "none: words from common knowledge, judgment, or the visible conversation are enough. A message that merely mentions work is not a reason to call a tool",
			string(agentcontract.ExpectedToolCountOne):     "one: a single lookup, a single record, or a single change answers it",
			string(agentcontract.ExpectedToolCountSeveral): "several: the work reads or records one thing and then sends, records or changes another, so more than one tool is called",
		},
	}.Question()
}

func (builder questionBuilder) isExternalSendRequestedQuestion(messageKey string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions:     builder.about(messageKey) + "Does the requester explicitly ask to send or deliver content or files to a person or conversation outside the current conversation, now or on a schedule? Use only the requester's own instruction, not quoted or forwarded content. This records intent, not whether the effect is approved or permitted.",
		TrueDescription:  "the requester asks for content or files to be sent to a person or an external conversation, including a scheduled send",
		FalseDescription: "the requester asks for a reply or attachment in this current conversation, mentions an optional notification without asking to send one, or only has a send tool available",
	}.Question()
}

func (builder questionBuilder) taskShapeQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "What shape does the executable work take? If some work can proceed while another part awaits clarification, classify the work that can proceed.",
		OptionDescriptions: optionDescriptions(agentcontract.TaskShapeNames, map[string]string{
			string(agentcontract.TaskShapeImmediateReply):    "a tool-free answer; only for a quick reply or an unsupported request",
			string(agentcontract.TaskShapeResearchTask):      "information acquisition from an external or private source, or synthesis across source material",
			string(agentcontract.TaskShapeMaintenanceTask):   "work that changes state: adding, updating, or deleting records, files, or settings",
			string(agentcontract.TaskShapeScheduledTask):     "work the message asks to run later, repeatedly, or on a schedule",
			string(agentcontract.TaskShapeApprovalGatedTask): "work held for a missing essential choice about the requested goal, target, or outcome that only the requester can resolve; tool-discoverable operational requirements belong to the work itself",
		}),
	}.Question()
}

func (builder questionBuilder) levelQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "How difficult is the work it asks for? This one tier sizes both the model and the work budget.",
		OptionDescriptions: optionDescriptions(agentcontract.IntakeTaskLevelNames, map[string]string{
			string(agentcontract.TaskLevelLow):    "ordinary bounded work with a clear short outcome that normally produces one final reply, even when it needs a few tools",
			string(agentcontract.TaskLevelMedium): "multi-step work, research, or artifact generation, where progress updates are useful",
			string(agentcontract.TaskLevelHigh):   "long, wide, deployment-shaped, or verification-heavy work",
		}),
	}.Question()
}

func (builder questionBuilder) deliverableKindQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "What is the primary deliverable the work ultimately is? This is about the final form, not which tool runs first.",
		OptionDescriptions: optionDescriptions(agentcontract.DeliverableKindNames, map[string]string{
			string(agentcontract.DeliverableKindPresentation): "a slide deck",
			string(agentcontract.DeliverableKindDocument):     "a text document that exists as a file",
			string(agentcontract.DeliverableKindNone):         "anything else, including everything whose final form is a message in the conversation",
		}),
	}.Question()
}

func (builder questionBuilder) priorTaskReferenceQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "How does it relate to priorTask in the state, which is a candidate previous task rather than a running one?",
		OptionDescriptions: optionDescriptions(agentcontract.PriorTaskReferenceNames, map[string]string{
			string(agentcontract.PriorTaskReferenceOutcomeRecovery): "it asks to deliver, retry, continue, or revise that prior task's outcome",
			string(agentcontract.PriorTaskReferenceNone):            "unrelated or self-contained, including a follow-up that asks to read, open, check, or summarize an artifact the prior task already delivered",
		}),
	}.Question()
}

func (builder questionBuilder) outputFormatQuestion(messageKey string, formatName string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions:     builder.about(messageKey) + "Does it explicitly ask for a deliverable file in " + formatName + " format?",
		TrueDescription:  "the message names that file format for something it asks to create, edit, convert, generate, or deliver. Words like presentation, slides, deck, 피피티, and 발표자료 name the kind of artifact, not a pptx file",
		FalseDescription: "anything else, including reading, summarizing, searching, or analyzing an input attachment, and anything whose final form is a message in the conversation",
	}.Question()
}

func (builder questionBuilder) likelyToolQuestion(messageKey string, toolName string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions:     builder.about(messageKey) + "Will the work call " + toolName + "? " + toolLikelihoodGuidanceReference,
		TrueDescription:  "the work plainly needs what that tool does",
		FalseDescription: "its name merely shares a word with the message, or it might conceivably help",
	}.Question()
}

func needsResponseLanguage(request turnclassification.IntakeDecisionRequest) bool {
	return toolcontract.ResolveResponseLanguage(request.ResponseLanguage) == ""
}

func (builder questionBuilder) responseLanguageQuestion(messageKey string) model.DecisionQuestion {
	descriptions := map[string]string{toolcontract.ResponseLanguageOther: "a language not listed here, or no language the message is mainly written in"}
	for _, language := range toolcontract.ResponseLanguages {
		descriptions[language.Code] = language.Name
	}
	return model.ChoiceQuestion{
		Instructions:       builder.about(messageKey) + "Which language is it mainly written in? The reply will be written in that language.",
		OptionDescriptions: descriptions,
	}.Question()
}
