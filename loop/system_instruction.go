package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"sort"
	"strings"
)

func (agentTurnRunner *AgentTurnRunner) buildSystemInstruction(request AgentTurnRequest) string {
	return buildAgentSystemInstruction(request)
}

func buildAgentSystemInstruction(request AgentTurnRequest) string {
	instruction := "You are " + request.AgentIdentity.DisplayName() + ". Work as a careful task agent. A Task is the full lifecycle for one user request; a Step is one internal progress unit that either runs one tool or closes the Task. Use continue when more work requires a tool, and finish only when goalSatisfied is true and hasRemainingWork is false. finish is the permanent final reply for this task, not a progress update or promise that later tool work will happen. A continue call carries only toolName and toolInput; planning lives in the conversation, not in every call."
	instruction += "\n\nProgress and completion evidence: Track progress from the observations and the Progress ledger, not from a per-call plan: before each Step, check whether a successful observation already satisfies the user's request — if it does, call finish immediately with that evidence instead of repeating the action or narrating more progress. Every finish must cite completionEvidence by observationID and toolName for successful tool observations that prove the goal is complete. Do not cite failed observations. When the user asks to list, find, search, read, or look up data, the final reply must state the concrete result facts from the successful tool observation. A status-only reply such as saying you looked it up is not an answer."
	instruction += " Check your own work before you finish it. When you change a file, use whatever the workspace already provides to see whether the change is right — run its tests, run the program, read the file back — and read that output before deciding you are done. A change you have not seen work is not evidence that it works."
	if taskLevelRequiresPlan(request.TaskLevel) {
		instruction += "\n\nPlan: This task is classified as multi-step: before your first state-changing tool call, record your goal and step plan with plan_update, and keep step statuses current as you work; the plan is your own working list and revising it is normal. Do not invent required steps the request does not state, and updating the plan is not itself a deliverable."
		instruction += " Size the task on that same call: the level you set decides how much room the task gets, and you set it from the steps you just wrote rather than from how the request sounded. If the plan grows past what you first set, say so with a larger level on the next plan_update."
	}
	instruction += "\n\nDirect answers: Tool-free final replies are valid when the request only needs a direct answer. Opinions, casual recommendations, brainstorming, and answers available from common knowledge or visible conversation context are direct answers: finish immediately without skill_search. Ask for a preference only when the user's requested result genuinely depends on that missing preference."
	instruction += "\n\nTool calling: The action schema contains the exact tools callable in this step. Call domain operations directly by name with their typed parameters. Do not request hidden tools, wait for the palette to expand, or run an agent tool name as a shell command. The runtime injects requester identity, approval, and delivery — never pass requester identity in input."
	instruction += " When the next piece of work needs several tools that do not depend on each other — reading the files a request names, checking several paths, running independent commands — request them together in one response: they run in order and stop at the first failure. Ask for a call on its own only when its input depends on what an earlier call returns."
	instruction += " When an observation carries sameOutputAs, its output is byte-identical to that earlier observation: the call told you nothing new, so re-running it in another form will not either. Act on what that output already says, or do something different."
	instruction += " If a steer observation appears, treat it as the latest user correction for the current task and update the plan before continuing."
	instruction += " Never repeat an add or create operation for a record a successful observation in this task already created: one user request creates at most one record, and anything wrong or missing on it is fixed with the matching update operation, using the record's exact current title or ID as the hint."
	if requestReachesSkills(request) {
		instruction += "\n\nSkills: Treat retrieved skills as available capability references, not mandatory workflows. The current user message, ActiveGoal, and OutcomeContract decide the output type. Do not turn a document, plan, or text request into a different workflow just because a related skill or tool is listed."
		instruction += " Selected skills contribute their direct tools to the action schema while the compact kernel remains available within the provider tool budget."
		if capabilityPhrase := capabilityDomainPhrase(request.AvailableSkills); capabilityPhrase != "" {
			instruction += " Your available capabilities span " + capabilityPhrase + "; reach them through selected direct tools, skills, and bundled scripts."
		} else {
			instruction += " Use skill_search to discover available domain operations and their direct tools."
		}
		instruction += " Before you tell the user you lack the capability, permission, access, or data, first use skill_search to discover the relevant operation, then actually try its direct tool. Only claim you cannot do something after that discovery and a real attempt come up empty, and say what you tried."
	}
	instruction += "\n\nFailure recovery: If a tool call fails, it creates FailureDebt. Do not give up after one failed attempt. Do not finish until every failure is resolved, recovered, or reported: a finish that ignores an unresolved failure is wrong even when the rest of the work succeeded."
	if len(request.RequiredAttachmentSuffixes) > 0 {
		instruction += "\n\nRequired artifacts: This task requires attached artifacts with these filename suffixes before finish: " + strings.Join(request.RequiredAttachmentSuffixes, ", ") + "."
	}
	if hostInstruction := strings.TrimSpace(request.HostInstruction); hostInstruction != "" {
		instruction += "\n\n" + hostInstruction
	}
	return instruction
}

func requestReachesSkills(request AgentTurnRequest) bool {
	return len(request.AvailableSkills) > 0 || request.ToolSet.IsRegistered(toolcontract.SkillSearchToolName)
}

func requestCanAskTheUser(request AgentTurnRequest) bool {
	return request.ToolSet.IsRegistered(toolcontract.AskInputToolName) || request.ToolSet.IsRegistered(toolcontract.AskChoiceToolName)
}

func requestSpeaksToPeople(request AgentTurnRequest) bool {
	return strings.TrimSpace(request.Platform) != "" || strings.TrimSpace(request.ConversationID) != ""
}

func capabilityDomainPhrase(skills []SkillInstruction) string {
	seenLabels := map[string]bool{}
	labels := []string{}
	for _, skill := range skills {
		for _, toolName := range skill.ToolReferences {
			domain := strings.ToLower(strings.TrimSpace(toolName))
			if separatorIndex := strings.Index(domain, "_"); separatorIndex > 0 {
				domain = domain[:separatorIndex]
			}
			if domain == "" {
				continue
			}
			if seenLabels[domain] {
				continue
			}
			seenLabels[domain] = true
			labels = append(labels, domain)
		}
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}

func (agentTurnRunner *AgentTurnRunner) buildToolDescription(toolRegistry *toolcontract.ToolSet) string {
	return buildAgentToolDescription(toolRegistry)
}

func buildAgentToolDescription(toolRegistry *toolcontract.ToolSet) string {
	if toolRegistry == nil {
		return ""
	}
	return toolRegistry.Descriptions()
}

func (agentTurnRunner *AgentTurnRunner) appendInstructionEvent(taskRunID string, request AgentTurnRequest) {
	body := map[string]any{
		"profileName":                 normalizedAgentProfileName(request.ProfileName),
		"toolNames":                   toolNamesForEvent(request.ToolSet),
		"registeredToolCount":         registeredToolCountForEvent(request.ToolSet),
		"describedToolNames":          describedToolNamesForEvent(request.ToolSet),
		"exposedToolNames":            toolNamesForEvent(request.ToolSet),
		"hiddenDescribedToolNames":    hiddenDescribedToolNamesForEvent(request.ToolSet),
		"selectedSkillToolReferences": selectedSkillToolReferencesForEvent(request),
		"pinnedSkillToolReferences":   pinnedSkillToolReferencesForEvent(request),
		"sourceCount":                 len(request.InstructionSources),
		"sources":                     request.InstructionSources,
		"skillNames":                  instructionSkillNames(request.InstructionSources),
		"skillDecisions":              request.SkillDecisions,
		"retrievalMode":               request.SkillRetrievalMode,
		"indexStatus":                 request.SkillIndexStatus,
		"candidateCount":              request.SkillCandidateCount,
		"skillQueries":                request.SkillQueries,
		"activeGoal":                  request.ActiveGoal,
		"outcomeContract":             request.OutcomeContract,
		"toolExposure":                request.ToolExposure,
	}
	if strings.TrimSpace(request.InstructionPrompt) == "" {
		body["status"] = "empty"
	} else {
		body["status"] = "loaded"
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.instructions_loaded", marshalEventBody(body))
}

func toolNamesForEvent(toolSet *toolcontract.ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListToolNames()
}

func registeredToolCountForEvent(toolSet *toolcontract.ToolSet) int {
	if toolSet == nil {
		return 0
	}
	return len(toolSet.ListRegisteredToolNames())
}

func describedToolNamesForEvent(toolSet *toolcontract.ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListDescribedToolNames()
}

func hiddenDescribedToolNamesForEvent(toolSet *toolcontract.ToolSet) []string {
	if toolSet == nil {
		return nil
	}
	return toolSet.ListHiddenDescribedToolNames()
}

func selectedSkillToolReferencesForEvent(request AgentTurnRequest) map[string][]string {
	selectedSkillNames := selectedSkillNames(request.SkillDecisions)
	return toolReferencesBySkillNameForEvent(request.AvailableSkills, selectedSkillNames)
}

func pinnedSkillToolReferencesForEvent(request AgentTurnRequest) map[string][]string {
	return toolReferencesBySkillNameForEvent(request.AvailableSkills, stringSet(request.PinnedSkillNames))
}

func toolReferencesBySkillNameForEvent(skillInstructions []SkillInstruction, skillNameByName map[string]bool) map[string][]string {
	result := map[string][]string{}
	for _, skillInstruction := range skillInstructions {
		if !skillNameByName[skillInstruction.Name] {
			continue
		}
		result[skillInstruction.Name] = SkillToolNames(skillInstruction)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func instructionSkillNames(sources []InstructionSource) []string {
	skillNames := []string{}
	seen := map[string]bool{}
	for _, source := range sources {
		if strings.TrimSpace(source.SkillName) == "" || seen[source.SkillName] {
			continue
		}
		seen[source.SkillName] = true
		skillNames = append(skillNames, source.SkillName)
	}
	return skillNames
}
