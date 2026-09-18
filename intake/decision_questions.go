package intake

import (
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	questionNameTarget              = "target"
	questionNameShouldRespond       = "shouldRespond"
	questionNameReaction            = "reaction"
	questionNameReactionEmoji       = "reactionEmoji"
	questionNameDuty                = "duty"
	questionNameRelatesToActiveTask = "relatesToActiveTask"
	questionNameRoute               = "route"
	questionNameClassification      = "classification"
	questionNameTaskShape           = "taskShape"
	questionNameLevel               = "level"
	questionNameDeliverableKind     = "deliverableKind"
	questionNameResponseLanguage    = "responseLanguage"
	questionNamePriorTaskReference  = "priorTaskReference"
	questionNameApproval            = "approval"
	questionNameBusyRoute           = "busyRoute"

	questionPrefixFormat = "format."
	questionPrefixTool   = "tool."
	questionPrefixChoice = "choice."
)

const reactionOptionNone = "none"
const reactionOptionReact = "react"
const dutyOptionNone = "none"

var requestedOutputFormatNames = []string{"html", "pptx", "pdf", "txt", "docx", "xlsx", "csv", "json"}

var reactionEmojiDescriptions = map[string]string{
	"white_check_mark":       "acknowledged, seen, done",
	"eyes":                   "looking at it now",
	"+1":                     "agreement or approval",
	"ok_hand":                "understood, will do",
	"pray":                   "thanks, or please, aimed at the assistant",
	"heart":                  "warmth or appreciation",
	"tada":                   "celebration of a result",
	"clap":                   "praise for someone's work",
	"raised_hands":           "shared celebration or gratitude",
	"fire":                   "impressive results",
	"rocket":                 "a launch or shipped work",
	"sparkles":               "something new or polished",
	"100":                    "strong agreement with an impressive result",
	"muscle":                 "cheering effort on",
	"wave":                   "a greeting or a farewell",
	"thinking_face":          "an open question worth considering",
	"memo":                   "noted, written down",
	"hourglass_flowing_sand": "it will take a while",
	"mag":                    "looking into it",
	"bulb":                   "a good idea",
	"sob":                    "sympathy for bad news",
	"sweat_smile":            "an awkward or self-deprecating joke",
}

type questionBuilder struct {
	request    agentcontract.IntakeDecisionRequest
	toolNames  []string
	choiceKeys []string
}

func newQuestionBuilder(request agentcontract.IntakeDecisionRequest) questionBuilder {
	return questionBuilder{
		request:    request,
		toolNames:  request.CallableToolNames,
		choiceKeys: decisionChoiceKeys(request.PendingChoice),
	}
}

func (builder questionBuilder) questions() map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for index := range builder.request.Messages {
		messageKey := decisionMessageKey(index)
		for name, question := range builder.questionsForMessage(messageKey) {
			questions[messageKey+"."+name] = question
		}
	}
	return questions
}

func (builder questionBuilder) questionsForMessage(messageKey string) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for name, question := range builder.addressingQuestions(messageKey) {
		questions[name] = question
	}
	for name, question := range builder.routerQuestions(messageKey) {
		questions[name] = question
	}
	if builder.asksFollowUp() {
		questions[questionNameRelatesToActiveTask] = builder.relatesToActiveTaskQuestion(messageKey)
	}
	return questions
}

func (builder questionBuilder) asksFollowUp() bool {
	return strings.TrimSpace(builder.request.ActiveTask.TaskRunID) != "" || builder.request.IsTaskRecentlyFinished
}

func about(messageKey string) string {
	return "About message " + messageKey + " in the state. "
}

func (builder questionBuilder) agentName() string {
	return builder.request.AgentIdentity.DisplayName()
}

func (builder questionBuilder) addressingQuestions(messageKey string) map[string]model.DecisionQuestion {
	agentName := builder.agentName()
	return map[string]model.DecisionQuestion{
		questionNameTarget: model.ChoiceQuestion{
			Instructions: about(messageKey) + "Who is it directed at? " + agentName + " is the workplace assistant in this conversation.",
			OptionDescriptions: map[string]string{
				string(agentcontract.AddressingTargetBot):     "directed at " + agentName + ", by mention, by reply, or by an unmistakable request to it",
				string(agentcontract.AddressingTargetHuman):   "directed at one specific person other than " + agentName,
				string(agentcontract.AddressingTargetAnyone):  "directed at the room in general, a share or an announcement anyone may answer",
				string(agentcontract.AddressingTargetNone):    "directed at nobody, a self-note, a reaction, or filler",
				string(agentcontract.AddressingTargetUnclear): "genuinely impossible to tell who it is aimed at",
			},
		}.Question(),
		questionNameShouldRespond: model.NoulQuestion{
			Instructions:    about(messageKey) + "Should " + agentName + " write a text reply to it?",
			TrueDescription: "it is a direct request, question, or instruction to " + agentName + "; it answers a question " + agentName + " asked; it makes " + agentName + " the intended responder; or it is social or playful and aimed at " + agentName + ", where a short in-kind reply keeps the conversation going",
			FalseDescription: "anything else. Ignore is the normal outcome for channel traffic: work chatter between other people, their status updates and coordination, thanks between two other people, " +
				"and a share, an FYI, or a closing thanks aimed at " + agentName + " that wants no words back",
		}.Question(),
		questionNameReaction: model.ChoiceQuestion{
			Instructions: about(messageKey) + "Would a courteous coworker leave an emoji reaction on it?",
			OptionDescriptions: map[string]string{
				reactionOptionNone: "no reaction; reacting would be noise. Routine work chatter between other people, status exchanges between colleagues, personal thanks between two people, and any message that neither addresses nor includes " + agentName + " get nothing. Topic or wording alone is never a reason to react",
				reactionOptionReact: "a single emoji acknowledges it well: a share or FYI posted for the whole team or for " + agentName + ", news worth celebrating, a joke posted for the room, " +
					"or a closing thanks or acknowledgement aimed at " + agentName,
			},
		}.Question(),
		questionNameReactionEmoji: model.ChoiceQuestion{
			Instructions:       about(messageKey) + "If a reaction were added to it, which emoji fits best?",
			OptionDescriptions: reactionEmojiOptionDescriptions(),
		}.Question(),
		questionNameDuty: model.ChoiceQuestion{
			Instructions:       about(messageKey) + "Does it specify a concrete item a standing duty should record right now, even though it was not addressed to " + agentName + "? The duties are listed in standingDuties in the state. Answer none for vague mentions, opinions, questions, hypotheticals, and chit-chat, and for anything addressed to " + agentName + " as a request.",
			OptionDescriptions: standingDutyOptionDescriptions(),
		}.Question(),
	}
}

func reactionEmojiOptionDescriptions() map[string]string {
	descriptions := map[string]string{}
	for _, name := range agentcontract.ReactionEmojiNames {
		description := reactionEmojiDescriptions[name]
		if description == "" {
			description = name
		}
		descriptions[name] = description
	}
	return descriptions
}

func standingDutyOptionDescriptions() map[string]string {
	descriptions := map[string]string{dutyOptionNone: "it records nothing: chatter, a question, a share, or a request aimed at the assistant itself"}
	for _, duty := range agentcontract.StandingDuties() {
		descriptions[duty.Name] = duty.Description
	}
	return descriptions
}

func (builder questionBuilder) relatesToActiveTaskQuestion(messageKey string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions:     about(messageKey) + "Does it continue, correct, cancel, or ask about the task in the state (activeTask or recentlyFinishedTask)?",
		TrueDescription:  "it is about that task: a correction, an addition, a cancellation, or a question about its progress",
		FalseDescription: "it is a self-contained new request that has nothing to do with that task",
	}.Question()
}

func (builder questionBuilder) routerQuestions(messageKey string) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{
		questionNameRoute:              builder.routeQuestion(messageKey),
		questionNameClassification:     builder.classificationQuestion(messageKey),
		questionNameTaskShape:          builder.taskShapeQuestion(messageKey),
		questionNameLevel:              builder.levelQuestion(messageKey),
		questionNameDeliverableKind:    builder.deliverableKindQuestion(messageKey),
		questionNameResponseLanguage:   builder.responseLanguageQuestion(messageKey),
		questionNamePriorTaskReference: builder.priorTaskReferenceQuestion(messageKey),
	}
	if strings.TrimSpace(builder.request.PendingConfirmation.TaskRunID) != "" {
		questions[questionNameApproval] = builder.approvalQuestion(messageKey)
	}
	if strings.TrimSpace(builder.request.ActiveTask.TaskRunID) != "" {
		questions[questionNameBusyRoute] = builder.busyRouteQuestion(messageKey)
	}
	for _, formatName := range requestedOutputFormatNames {
		questions[questionPrefixFormat+formatName] = builder.outputFormatQuestion(messageKey, formatName)
	}
	for _, toolName := range builder.toolNames {
		questions[questionPrefixTool+toolName] = builder.initialToolQuestion(messageKey, toolName)
	}
	for index := range builder.choiceKeys {
		questions[questionPrefixChoice+strconv.Itoa(index+1)] = builder.choiceSelectionQuestion(messageKey, index)
	}
	return questions
}

func (builder questionBuilder) routeQuestion(messageKey string) model.DecisionQuestion {
	agentName := builder.agentName()
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "What should " + agentName + " do about it? The latest message is authoritative; earlier context only helps read it.",
		OptionDescriptions: map[string]string{
			string(agentcontract.TurnRouteConsume):        "nothing to say: an addressed message that needs no text reply, acknowledged with an emoji. Never consume a message that asks " + agentName + " to do, check, read, verify, or report anything",
			string(agentcontract.TurnRouteAnswerQuestion): "answer in words right now, from common knowledge, judgment, or what is visible, possibly after one small read-only tool call",
			string(agentcontract.TurnRouteAnswerMeta):     "answer a question about " + agentName + " itself: what it can do, how it works, what it is",
			string(agentcontract.TurnRouteClarify):        "ask one clarifying question first, because an essential choice only the sender can make is missing. Not for a bare mention when the visible context gives a clear topic, and never to ask for approval",
			string(agentcontract.TurnRouteStartTask):      "start work that takes tools and time",
			string(agentcontract.TurnRouteContinueTask):   "add to, or approve, work already running",
			string(agentcontract.TurnRouteReviseTask):     "redirect work already running toward a changed target or scope",
			string(agentcontract.TurnRouteGiveUp):         "say it cannot be done: physically impossible, nonsensical, or plainly improper on its face. Never for a permission concern, which the operating system decides at execution",
		},
	}.Question()
}

func (builder questionBuilder) classificationQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "What kind of turn is it?",
		OptionDescriptions: map[string]string{
			string(agentcontract.IntakeClassificationQuickReply):        "answerable in words now, with at most one small read-only or computation tool: greetings, jokes, office banter, capability questions, arithmetic, opinions, casual recommendations, brainstorming, and anything available from common knowledge or the visible conversation",
			string(agentcontract.IntakeClassificationBoundedTask):       "executable tool work with a clear outcome",
			string(agentcontract.IntakeClassificationNeedsConfirmation): "essential input only the sender can supply is missing. Approval for risky, destructive, paid, or externally visible work is handled after routing and is never this",
			string(agentcontract.IntakeClassificationUnsupported):       "pointless to even attempt",
		},
	}.Question()
}

func (builder questionBuilder) taskShapeQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "What shape does the work take?",
		OptionDescriptions: map[string]string{
			string(agentcontract.TaskShapeImmediateReply):     "a tool-free answer; only for a quick reply or an unsupported request",
			string(agentcontract.TaskShapeResearchTask):       "information acquisition from an external or private source, or synthesis across source material",
			string(agentcontract.TaskShapeMaintenanceTask):    "work that changes state: adding, updating, or deleting records, files, or settings",
			string(agentcontract.TaskShapeScheduledTask):      "work the message asks to run later, repeatedly, or on a schedule",
			string(agentcontract.TaskShapeBrowserHandoffTask): "work that needs a person at a browser, such as a sign-in or a captcha",
			string(agentcontract.TaskShapeApprovalGatedTask):  "work held for a missing essential input",
		},
	}.Question()
}

func (builder questionBuilder) levelQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "How difficult is the work it asks for? This one tier sizes both the model and the work budget.",
		OptionDescriptions: map[string]string{
			string(agentcontract.TaskLevelLow):    "ordinary bounded work with a clear short outcome that normally produces one final reply, even when it needs a few tools",
			string(agentcontract.TaskLevelMedium): "multi-step work, research, or artifact generation, where progress updates are useful",
			string(agentcontract.TaskLevelHigh):   "long, wide, deployment-shaped, or verification-heavy work",
		},
	}.Question()
}

func (builder questionBuilder) deliverableKindQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "What is the primary deliverable the work ultimately is? This is about the final form, not which tool runs first.",
		OptionDescriptions: map[string]string{
			string(agentcontract.DeliverableKindWebsite):      "a live site, web page, landing page, dashboard, or demo served at a URL, at every stage including an unpublished draft",
			string(agentcontract.DeliverableKindPresentation): "a slide deck",
			string(agentcontract.DeliverableKindDocument):     "a text document that exists as a file",
			string(agentcontract.DeliverableKindNone):         "anything else, including everything whose final form is a message in the conversation",
		},
	}.Question()
}

func (builder questionBuilder) responseLanguageQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "Which language should the reply be written in? Answer with the language the message itself is written in unless runtimeResponseLanguage in the state names another.",
		OptionDescriptions: map[string]string{
			"ko":                   "Korean",
			"en":                   "English",
			"same_as_conversation": "only when runtimeResponseLanguage already defines it",
		},
	}.Question()
}

func (builder questionBuilder) priorTaskReferenceQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "How does it relate to priorTask in the state, which is a candidate previous task rather than a running one?",
		OptionDescriptions: map[string]string{
			string(agentcontract.PriorTaskReferenceOutcomeRecovery): "it asks to deliver, retry, continue, or revise that prior task's outcome",
			string(agentcontract.PriorTaskReferenceNone):            "unrelated or self-contained, including a follow-up that asks to read, open, check, or summarize an artifact the prior task already delivered",
		},
	}.Question()
}

func (builder questionBuilder) approvalQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "A confirmation is pending (pendingConfirmation in the state). How does the message answer it? Redirecting the work is not approving it. When pendingConfirmation.exchangesSince is above zero, a bare yes, no, or option number no longer names the pending action, and only a message naming this action or this question answers it.",
		OptionDescriptions: map[string]string{
			string(agentcontract.ApprovalSignalApprove):     "it clearly authorizes this exact pending action, this once",
			string(agentcontract.ApprovalSignalApproveTask): "it authorizes this action and the rest of this task's work of the same kind, without asking again",
			string(agentcontract.ApprovalSignalReject):      "it declines the pending action or says to stop",
			string(agentcontract.ApprovalSignalUnclear):     "it does not answer the pending confirmation, or it changes the target, scope, conditions, or asks for a different action",
		},
	}.Question()
}

func (builder questionBuilder) busyRouteQuestion(messageKey string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about(messageKey) + "A task is already running (activeTask in the state). What should happen to it? Natural-language stop requests are ordinary messages, so read them by intent.",
		OptionDescriptions: map[string]string{
			string(agentcontract.BusyRouteStatus):    "it asks whether work is happening, or asks for progress",
			string(agentcontract.BusyRouteSteer):     "it corrects or redirects the running task without cancelling it",
			string(agentcontract.BusyRouteReplace):   "it clearly cancels or replaces the running task with a new instruction",
			string(agentcontract.BusyRouteCancel):    "it asks to stop, cancel, abort, or not continue the running task",
			string(agentcontract.BusyRouteNewTask):   "it is independent and should not affect the running task",
			string(agentcontract.BusyRouteUnrelated): "it should neither start nor alter work",
		},
	}.Question()
}

func (builder questionBuilder) outputFormatQuestion(messageKey string, formatName string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions: about(messageKey) + "Does it explicitly ask for a deliverable file in " + formatName + " format?",
		TrueDescription: "the message names that file format for something it asks to create, edit, convert, generate, or deliver. Words like presentation, slides, deck, 피피티, and 발표자료 name the kind of artifact, not a pptx file. " +
			"A request to create or update a website or web page is a live site, not an html file",
		FalseDescription: "anything else, including reading, summarizing, searching, or analyzing an input attachment, and anything whose final form is a message in the conversation",
	}.Question()
}

func (builder questionBuilder) initialToolQuestion(messageKey string, toolName string) model.DecisionQuestion {
	return model.NoulQuestion{
		Instructions:     about(messageKey) + "Is " + toolName + " among the first tools the work will call? Its description is in availableTools in the state.",
		TrueDescription:  "a confident pick: the tool's effect matches the outcome the message asks for. When the visible conversation shows an artifact already created for this sender, an edit to it uses that artifact's read and edit tools rather than its create tool",
		FalseDescription: "anything else, including every tool you are unsure about and every tool when no tool is needed",
	}.Question()
}

func (builder questionBuilder) choiceSelectionQuestion(messageKey string, optionIndex int) model.DecisionQuestion {
	optionKey := builder.choiceKeys[optionIndex]
	return model.NoulQuestion{
		Instructions:     about(messageKey) + "A choice is pending (pendingChoice in the state). Does the message select option " + strconv.Itoa(optionIndex+1) + ", whose key is " + optionKey + "?",
		TrueDescription:  "it names that option by its number, its label, or a paraphrase of it, in any language and any script",
		FalseDescription: "it names a different option, gives a custom answer, or is not an answer to the pending question at all",
	}.Question()
}

func decisionChoiceKeys(pendingChoice agentcontract.PendingChoiceContext) []string {
	keys := []string{}
	seenKeys := map[string]bool{}
	for _, option := range pendingChoice.Options {
		key := strings.TrimSpace(option.Key)
		if key == "" || seenKeys[key] {
			continue
		}
		seenKeys[key] = true
		keys = append(keys, key)
	}
	return keys
}
