package intake

import (
	"slices"

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
	questions := map[string]model.DecisionQuestion{}
	if needsResponseLanguage(builder.request) {
		questions[agentcontract.IntakeQuestionResponseLanguage] = builder.responseLanguageQuestion(messageKey)
	}
	decidedWork := builder.request.DecidedWork
	if decidedWork == "" {
		questions[agentcontract.IntakeQuestionWork] = agentcontract.WorkQuestion(builder.about(messageKey), builder.agentName())
	}
	if decidedWork != "" && !decidedWork.IsDoable() {
		return questions
	}
	for name, question := range builder.workQuestions(messageKey) {
		questions[name] = question
	}
	return questions
}

func (builder questionBuilder) workQuestions(messageKey string) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{
		agentcontract.IntakeQuestionClarify:                 builder.clarifyQuestion(messageKey),
		agentcontract.IntakeQuestionExpectedToolCount:       builder.expectedToolCountQuestion(messageKey),
		agentcontract.IntakeQuestionIsExternalSendRequested: builder.isExternalSendRequestedQuestion(messageKey),
		agentcontract.IntakeQuestionTaskShape:               builder.taskShapeQuestion(messageKey),
		agentcontract.IntakeQuestionDeliverableKind:         builder.deliverableKindQuestion(messageKey),
	}
	if hasActiveGoal(builder.request) {
		questions[agentcontract.IntakeQuestionRelation] = builder.relationQuestion(messageKey)
	}
	if hasPriorTask(builder.request) {
		questions[agentcontract.IntakeQuestionPriorTaskReference] = builder.priorTaskReferenceQuestion(messageKey)
	}
	for _, formatName := range turnclassification.RequestedOutputFormatNames {
		questions[agentcontract.IntakeQuestionPrefixFormat+formatName] = builder.outputFormatQuestion(messageKey, formatName)
	}
	return questions
}

var relationRouteNames = []string{string(agentcontract.TurnRouteContinueTask), string(agentcontract.TurnRouteReviseTask), string(agentcontract.TurnRouteStartTask)}

func (builder questionBuilder) relationQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "How does the work it asks for stand to activeGoal in the state? The latest message is authoritative; earlier context only helps read it. The message is input to that goal unless it plainly starts something unrelated.",
		OptionDescriptions: optionDescriptions(relationRouteNames, map[string]string{
			string(agentcontract.TurnRouteContinueTask): "input to that goal: what it waits for, more of the same work, or an approval",
			string(agentcontract.TurnRouteReviseTask):   "it redirects that goal toward a changed target or scope",
			string(agentcontract.TurnRouteStartTask):    "it plainly starts something unrelated to that goal",
		}),
	}.Question()
}

func (builder questionBuilder) clarifyQuestion(messageKey string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions:     builder.about(messageKey) + "Must " + builder.agentName() + " ask the sender one question before any of the requested work can proceed?",
		TrueDescription:  "the requested goal, target, or outcome is still ambiguous after using the visible context, only the sender can resolve it, and no independently requested part with a clear target and effect can proceed without the answer. A step that is only a prerequisite, or an action the sender did not authorize, is not a part that can proceed",
		FalseDescription: "at least one independently requested part has a clear target and effect and can proceed now. Operational details, approval roles, and requirements a tool can inspect or resolve are not reasons to ask; the work finds those out. Never to ask for approval",
	}.Question()
}

func (builder questionBuilder) expectedToolCountQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "How many tools will doing what it asks call before the work is done?",
		OptionDescriptions: map[string]string{
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

var workTaskShapeNames = slices.DeleteFunc(slices.Clone(agentcontract.TaskShapeNames), func(name string) bool {
	return name == string(agentcontract.TaskShapeImmediateReply)
})

func (builder questionBuilder) taskShapeQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: builder.about(messageKey) + "What shape does the executable work take? If some work can proceed while another part awaits clarification, classify the work that can proceed.",
		OptionDescriptions: optionDescriptions(workTaskShapeNames, map[string]string{
			string(agentcontract.TaskShapeResearchTask):      "information acquisition from an external or private source, or synthesis across source material",
			string(agentcontract.TaskShapeMaintenanceTask):   "work that changes state: adding, updating, or deleting records, files, or settings",
			string(agentcontract.TaskShapeScheduledTask):     "work the message asks to run later, repeatedly, or on a schedule",
			string(agentcontract.TaskShapeApprovalGatedTask): "work held for a missing essential choice about the requested goal, target, or outcome that only the requester can resolve; tool-discoverable operational requirements belong to the work itself",
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
