package loop

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestSelectedEvidenceHintsComeFromSelectedSkills(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "office"}, {Name: "calendar"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "office", Status: "selected"}},
		RequiredEvidenceTools: []string{"write", "bash", "file_deliver"},
	}

	toolNames := selectedEvidenceHintTools(instructionBundle)

	if len(toolNames) != 3 || toolNames[0] != "write" || toolNames[1] != "bash" || toolNames[2] != "file_deliver" {
		t.Fatalf("expected selected skill evidence tools, got %+v", toolNames)
	}
}

func TestOutcomeContractDerivesScheduleEvidenceFromSkillHint(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "task_add", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
		{Name: "schedule_create", Namespace: "schedule", SideEffectClass: toolcontract.ToolSideEffectStateChange},
	})
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "send this message tomorrow at 3pm",
			ToolSet: toolSet,
		},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
			TaskShape:      TaskShapeScheduledTask,
		},
		InstructionBundle{RequiredEvidenceTools: []string{"schedule_create"}},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "schedule_create") {
		t.Fatalf("expected schedule_create required evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if stringSliceContains(contract.RequiredEvidenceTools, "task_add") {
		t.Fatalf("expected the skill-contract hint not to add fallback evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestAttachmentOutcomeTreatsWorkspaceFileWriteAsIntermediate(t *testing.T) {
	fileWrite := testToolDescriptor(toolcontract.WriteToolName)
	fileWrite.SideEffectClass = toolcontract.ToolSideEffectWorkspaceWrite
	fileWrite.Completion = toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation}
	fileWrite.OutputSchema = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	fileWrite.ResultContract.Schema = fileWrite.OutputSchema
	fileWrite.ResultContract.Effects = []toolcontract.ResourceEffectContract{{
		ObjectType:     "file",
		Effect:         "created",
		ResultField:    "path",
		EffectIdentity: "path",
	}}
	fileDeliver := testToolDescriptor(toolcontract.FileDeliverToolName)
	fileDeliver.SideEffectClass = toolcontract.ToolSideEffectExternalWrite
	fileDeliver.Completion = toolcontract.ToolCompletion{Mode: toolcontract.ToolCompletionObservation}
	fileDeliver.InputIntentSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	fileDeliver.OutputSchema = json.RawMessage(`{"type":"object","properties":{"deliveredPaths":{"type":"array","items":{"type":"string"}}},"required":["deliveredPaths"],"additionalProperties":false}`)
	fileDeliver.ResultContract.Schema = fileDeliver.OutputSchema
	fileDeliver.ResultContract.Effects = []toolcontract.ResourceEffectContract{{
		ObjectType:     "file",
		Effect:         "attached",
		ResultField:    "deliveredPaths",
		EffectIdentity: "path",
	}}
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{fileWrite, fileDeliver})
	if !toolProducesIntermediateAttachmentSource(toolSet, toolcontract.WriteToolName) {
		t.Fatal("expected write descriptor to represent an intermediate attachment source")
	}

	contract := outcomeContractForRequest(
		AgentRequest{ToolSet: toolSet},
		IntakeDecision{
			Classification:         IntakeClassificationBoundedTask,
			TaskShape:              TaskShapeMaintenanceTask,
			RequestedOutputFormats: []string{"docx"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		[]string{".docx"},
	)
	if !reflect.DeepEqual(contract.RequiredEvidenceTools, []string{toolcontract.FileDeliverToolName}) {
		t.Fatalf("expected delivery-only attachment evidence, got %v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractPreservesActiveGoalEvidence(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"task_delete"},
		}}},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "task_delete") || stringSliceContains(contract.RequiredEvidenceTools, "file_delete") {
		t.Fatalf("expected active goal evidence to remain authoritative, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractDoesNotFallbackToScheduleCreateForScheduledTaskShape(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "send this message tomorrow at 3pm",
			ToolSet: newTestToolSet([]string{"schedule_create"}),
		},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
			TaskShape:      TaskShapeScheduledTask,
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "schedule_create") {
		t.Fatalf("expected scheduled task shape not to create fallback evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractCreatesExpectedResultsForRequestedFile(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "pptx make the file"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, RequestedOutputFormats: []string{".pptx"}},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		[]string{".pptx"},
	)

	if !expectedResultsContain(contract.ExpectedResults, ExpectedResultTypeFile, "file in the requested format") {
		t.Fatalf("expected file output contract, got %+v", contract.ExpectedResults)
	}
	if len(contract.ExpectedResults[0].AcceptanceHints) == 0 || contract.ExpectedResults[0].AcceptanceHints[0] != ".pptx" {
		t.Fatalf("expected suffix hint to be preserved, got %+v", contract.ExpectedResults)
	}
}

func TestResolvedInputDischargesAskInputContract(t *testing.T) {
	request := AgentRequest{
		ExistingTaskRunID: "task-1",
		ActiveGoal: ActiveGoal{
			TaskRunID: "task-1",
			Status:    ActiveGoalStatusWaitingUserInput,
		},
	}
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{toolcontract.AskInputToolName, "task_update"},
		RequiredEvidenceAnyOf: [][]string{{toolcontract.AskInputToolName, "task_update"}},
		SelectedEvidenceHints: []string{toolcontract.AskInputToolName},
		ExpectedResults: []ExpectedResult{{
			ID:              "choice",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice",
			Required:        true,
			AcceptanceHints: []string{toolcontract.AskInputToolName},
		}, {
			ID:              "choice-update",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice applied",
			Required:        true,
			AcceptanceHints: []string{toolcontract.AskInputToolName, "task_update"},
		}},
	}

	resolvedContract := dischargeResolvedInputContract(request, TurnDecision{Route: TurnRouteContinueTask}, contract)

	if stringSliceContains(resolvedContract.RequiredEvidenceTools, toolcontract.AskInputToolName) ||
		stringSliceContains(resolvedContract.SelectedEvidenceHints, toolcontract.AskInputToolName) {
		t.Fatalf("expected resolved ask_input requirements to be discharged, got %+v", resolvedContract)
	}
	if len(resolvedContract.ExpectedResults) != 1 ||
		len(resolvedContract.ExpectedResults[0].AcceptanceHints) != 1 ||
		resolvedContract.ExpectedResults[0].AcceptanceHints[0] != "task_update" {
		t.Fatalf("expected mixed expected result to retain its remaining hint, got %+v", resolvedContract.ExpectedResults)
	}
	if len(resolvedContract.RequiredEvidenceAnyOf) != 1 || !stringSliceContains(resolvedContract.RequiredEvidenceAnyOf[0], "task_update") {
		t.Fatalf("expected unrelated evidence alternative to remain, got %+v", resolvedContract.RequiredEvidenceAnyOf)
	}
}

func TestUnresolvedInputKeepsAskInputContract(t *testing.T) {
	contract := OutcomeContract{
		RequiredEvidenceTools: []string{toolcontract.AskInputToolName},
		ExpectedResults: []ExpectedResult{{
			ID:              "choice",
			Type:            ExpectedResultTypeMessage,
			Description:     "user choice",
			Required:        true,
			AcceptanceHints: []string{toolcontract.AskInputToolName},
		}},
	}
	request := AgentRequest{
		ExistingTaskRunID: "task-1",
		ActiveGoal: ActiveGoal{
			TaskRunID: "task-1",
			Status:    ActiveGoalStatusWaitingUserInput,
		},
	}

	for _, turnDecision := range []TurnDecision{{Route: TurnRouteStartTask}, {Route: TurnRouteAnswerQuestion}} {
		unresolvedContract := dischargeResolvedInputContract(request, turnDecision, contract)
		if !stringSliceContains(unresolvedContract.RequiredEvidenceTools, toolcontract.AskInputToolName) || len(unresolvedContract.ExpectedResults) != 1 {
			t.Fatalf("expected route %s to preserve ask_input contract, got %+v", turnDecision.Route, unresolvedContract)
		}
	}
	request.ExistingTaskRunID = "task-2"
	mismatchedContract := dischargeResolvedInputContract(request, TurnDecision{Route: TurnRouteContinueTask}, contract)
	if !stringSliceContains(mismatchedContract.RequiredEvidenceTools, toolcontract.AskInputToolName) {
		t.Fatal("expected another task's continuation not to discharge ask_input")
	}
}

func TestOutcomeContractDoesNotTreatReplyInstructionAsExternalSend(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "message_send", Namespace: "message", SideEffectClass: toolcontract.ToolSideEffectExternalSend},
	})

	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "summarize this week's tasks and just give me the summary", ToolSet: toolSet},
		IntakeDecision{Classification: IntakeClassificationBoundedTask},
		instructionBundle,
		ExecutionPlan{},
		true,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected reply instruction not to require external send evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func expectedResultsContain(results []ExpectedResult, resultType string, descriptionFragment string) bool {
	for _, result := range results {
		if result.Type == resultType && strings.Contains(result.Description, descriptionFragment) {
			return true
		}
	}
	return false
}

func TestOutcomeContractIgnoresSelectedDirectMessageForNonSendGoal(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask}

	contract := outcomeContractForRequest(AgentRequest{Prompt: "https://example.com use it to write the business plan"}, intakeDecision, instructionBundle, ExecutionPlan{}, false, nil)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no DM hard gate for non-send goal, got %+v", contract.RequiredEvidenceTools)
	}
	if len(contract.SelectedEvidenceHints) != 1 || contract.SelectedEvidenceHints[0] != "message_send" {
		t.Fatalf("expected selected evidence hint to be retained, got %+v", contract.SelectedEvidenceHints)
	}
}

func TestOutcomeContractDoesNotPromoteDirectMessageHintForAttachmentFollowUp(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	request := AgentRequest{
		Prompt: "let's try again",
		VisibleContext: VisibleContext{
			Materials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Path:        "home/inbox/mattermost/direct/post/kim-intern-automation.html",
				ContentType: "text/html",
			}},
		},
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"message_send"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	if stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected attachment follow-up not to require DM send evidence, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractIgnoresMailKeywordForArtifactAttachmentGoal(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask}

	contract := outcomeContractForRequest(
		AgentRequest{Prompt: "attach a five-slide PPTX introducing the mail, calendar, and browser control features"},
		intakeDecision,
		instructionBundle,
		ExecutionPlan{},
		false,
		[]string{".pptx"},
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected artifact attachment request not to require DM send evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if !stringSliceContains(contract.RequiredEvidenceTools, "file_deliver") {
		t.Fatalf("expected artifact attachment request to require file_deliver, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractRequiresSendEvidenceForExternalSendPlan(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "direct-message"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
		RequiredEvidenceTools: []string{"message_send"},
	}
	intakeDecision := IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}

	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:            "message_send",
		Namespace:       "message",
		SideEffectClass: toolcontract.ToolSideEffectExternalSend,
	}})
	contract := outcomeContractForRequest(AgentRequest{Prompt: "send Dana a DM saying test", ToolSet: toolSet}, intakeDecision, instructionBundle, ExecutionPlan{ExternalSend: true, ThirdPartyExternalSend: true}, true, nil)

	if len(contract.RequiredEvidenceTools) != 1 || contract.RequiredEvidenceTools[0] != "message_send" {
		t.Fatalf("expected send hard gate for external send goal, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractIgnoresIntakeSendEvidenceForCurrentConversationReply(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{{
		Name:            "message_send",
		Namespace:       "message",
		SideEffectClass: toolcontract.ToolSideEffectExternalSend,
	}})
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "hi, answer with a short greeting.",
			ToolSet: toolSet,
		},
		IntakeDecision{
			Classification: IntakeClassificationBoundedTask,
			TaskShape:      TaskShapeMaintenanceTask,
		},
		InstructionBundle{},
		ExecutionPlan{},
		true,
		nil,
	)

	if stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected current-conversation reply not to require message_send, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractKeepsSendEvidenceForExternalSendContinuation(t *testing.T) {
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt: "do it",
			ActiveGoal: ActiveGoal{
				OriginalInstruction: "send Dana a DM saying test",
				OutcomeContract: OutcomeContract{
					RequiredEvidenceTools: []string{"message_send"},
				},
			},
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if !stringSliceContains(contract.RequiredEvidenceTools, "message_send") {
		t.Fatalf("expected external send continuation to keep message_send, got %+v", contract.RequiredEvidenceTools)
	}
}

func TestOutcomeContractDoesNotDeriveEvidenceFromPromptAndAvailableTools(t *testing.T) {
	tests := []struct {
		name      string
		prompt    string
		toolNames []string
	}{
		{
			name:      "flow task",
			prompt:    "register the task",
			toolNames: []string{"task_add"},
		},
		{
			name:      "external send",
			prompt:    "send Dana a DM saying test",
			toolNames: []string{"message_send"},
		},
	}

	for _, test := range tests {
		contract := outcomeContractForRequest(
			AgentRequest{
				Prompt:  test.prompt,
				ToolSet: testToolSet(test.toolNames),
			},
			IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
			InstructionBundle{},
			ExecutionPlan{},
			false,
			nil,
		)
		if len(contract.RequiredEvidenceTools) != 0 {
			t.Fatalf("expected %s prompt and available tools not to derive evidence, got %+v", test.name, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractDemotesIntakeInitialToolsToEvidenceHints(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "memory_remember", Namespace: "memory", SideEffectClass: toolcontract.ToolSideEffectStateChange},
	})
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "remember what was said in this conversation",
			ToolSet: toolSet,
		},
		IntakeDecision{
			Classification:   IntakeClassificationBoundedTask,
			TaskShape:        TaskShapeMaintenanceTask,
			InitialToolNames: []string{"memory_remember"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected intake initial tools not to become required evidence, got %+v", contract.RequiredEvidenceTools)
	}
	if len(contract.RequiredEvidenceAnyOf) != 0 {
		t.Fatalf("expected intake initial tools not to become required any-of evidence, got %+v", contract.RequiredEvidenceAnyOf)
	}
	if !stringSliceContains(contract.SelectedEvidenceHints, "memory_remember") {
		t.Fatalf("expected intake initial tools to be recorded as evidence hints, got %+v", contract.SelectedEvidenceHints)
	}
}

func TestAPlannedExternalSendKeepsTheToolItWasPlannedWith(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "message_send", Namespace: "message", SideEffectClass: toolcontract.ToolSideEffectExternalSend},
	})
	testCases := []struct {
		name                    string
		executionPlan           ExecutionPlan
		expectsRequiredEvidence bool
	}{
		{
			name:                    "a plan that declares an external send",
			executionPlan:           ExecutionPlan{ExternalSend: true},
			expectsRequiredEvidence: true,
		},
		{
			name:                    "a plan that declares no external send",
			executionPlan:           ExecutionPlan{},
			expectsRequiredEvidence: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			contract := outcomeContractForRequest(
				AgentRequest{
					Prompt:  "send 이샘플 a message about tomorrow",
					ToolSet: toolSet,
				},
				IntakeDecision{
					Classification:   IntakeClassificationBoundedTask,
					TaskShape:        TaskShapeMaintenanceTask,
					InitialToolNames: []string{"message_send"},
				},
				InstructionBundle{},
				testCase.executionPlan,
				true,
				nil,
			)

			isRequiredEvidence := stringSliceContains(contract.RequiredEvidenceTools, "message_send")
			if isRequiredEvidence != testCase.expectsRequiredEvidence {
				t.Fatalf("expected message_send required evidence %t, got %+v", testCase.expectsRequiredEvidence, contract.RequiredEvidenceTools)
			}
		})
	}
}

func TestALikelyToolTheWorkNeverCalledDoesNotHoldAFinishedTaskOpen(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "message_search", Namespace: "message", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "task_update", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
	})
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "어제 올린 공지 상태 정리해줘",
			ToolSet: toolSet,
		},
		IntakeDecision{
			Classification:   IntakeClassificationBoundedTask,
			TaskShape:        TaskShapeMaintenanceTask,
			InitialToolNames: []string{"message_search", "task_update"},
		},
		InstructionBundle{},
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected a merely likely tool to prove nothing about the outcome, got %+v", contract.RequiredEvidenceTools)
	}
	if !stringSliceContains(contract.SelectedEvidenceHints, "message_search") {
		t.Fatalf("expected the likely tool to stay a hint, got %+v", contract.SelectedEvidenceHints)
	}
}

func TestOutcomeContractDerivesSideEffectEvidenceAnyOfGroupForMaintenanceTask(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "task_add", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
		{Name: "task_list", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "task_update", Namespace: "task", SideEffectClass: toolcontract.ToolSideEffectStateChange},
	})
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "internkim-flow"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		RequiredEvidenceTools: []string{"task_add", "task_list", "task_update"},
	}
	contract := outcomeContractForRequest(
		AgentRequest{
			Prompt:  "register one new task",
			ToolSet: toolSet,
		},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask},
		instructionBundle,
		ExecutionPlan{},
		false,
		nil,
	)

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no AND-required evidence from the working set derivation, got %+v", contract.RequiredEvidenceTools)
	}
	if len(contract.RequiredEvidenceAnyOf) != 1 {
		t.Fatalf("expected exactly one derived any-of group, got %+v", contract.RequiredEvidenceAnyOf)
	}
	if !stringSliceContains(contract.RequiredEvidenceAnyOf[0], "task_add") || !stringSliceContains(contract.RequiredEvidenceAnyOf[0], "task_update") {
		t.Fatalf("expected the side-effect working set tools in the derived group, got %+v", contract.RequiredEvidenceAnyOf[0])
	}
	if !stringSliceContains(contract.RequiredEvidenceAnyOf[0], "task_list") {
		t.Fatalf("expected the read tool to stay satisfiable so verification asks can finish on read evidence, got %+v", contract.RequiredEvidenceAnyOf[0])
	}
}

func TestOutcomeReferenceToolSetHidesSendToolsForDocumentGoal(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "web_fetch", Namespace: "web", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "write", Namespace: "file", SideEffectClass: toolcontract.ToolSideEffectWorkspaceWrite},
		{Name: "file_deliver", Namespace: "file", SideEffectClass: toolcontract.ToolSideEffectExternalWrite},
		{Name: "message_send", Namespace: "message", SideEffectClass: toolcontract.ToolSideEffectExternalSend},
		{Name: "mail_message_send", Namespace: "mail", SideEffectClass: toolcontract.ToolSideEffectExternalSend},
	})
	contract := OutcomeContract{
		SelectedEvidenceHints: []string{"message_send", "mail_message_send"},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "https://example.com use it to write the business plan"}, ExecutionPlan{}, false, contract)

	for _, toolName := range []string{"web_fetch", "write", "file_deliver"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
	for _, toolName := range []string{"message_send", "mail_message_send"} {
		if filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to be hidden for document goal, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestAgentTurnToolSetExposesPinnedNonKernelTools(t *testing.T) {
	toolSet := testToolSet([]string{"web_search", "web_fetch", "bash", "write"})
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "presentation", ToolReferences: []string{"bash", "write"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "presentation", Status: "selected"}},
	}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, AgentRequest{
		Prompt:          "https://example.com use it to make the deck",
		PinnedToolNames: []string{"web_search", "web_fetch", "bash", "write"},
	}, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"bash", "write", "web_search", "web_fetch"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected pinned tool %s to remain available, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeReferenceToolSetKeepsActiveGoalEvidenceToolsForContinuation(t *testing.T) {
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: "web_fetch", Namespace: "web", SideEffectClass: toolcontract.ToolSideEffectRead},
		{Name: "message_send", Namespace: "message", SideEffectClass: toolcontract.ToolSideEffectExternalSend},
	})
	request := AgentRequest{
		Prompt: "try again, it should work",
		ActiveGoal: ActiveGoal{OriginalInstruction: "send Dana a DM saying the draft is ready", OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"message_send"},
		}},
	}

	filteredToolSet := toolSetForOutcomeReference(toolSet, request, ExecutionPlan{}, false, OutcomeContract{})

	for _, toolName := range []string{"message_send"} {
		if !filteredToolSet.IsAllowed(toolName) {
			t.Fatalf("expected %s to remain available for an active send continuation, got %+v", toolName, filteredToolSet.ListToolNames())
		}
	}
}

func TestOutcomeContractRequiresActiveGoalRequiredEvidenceForContinuation(t *testing.T) {
	instructionBundle := InstructionBundle{
		Skills:                []SkillInstruction{{Name: "office"}},
		SkillDecisions:        []SkillSelectionDecision{{Name: "office", Status: "selected"}},
		RequiredEvidenceTools: []string{"bash", "file_deliver"},
	}
	request := AgentRequest{
		Prompt: "try again, it should work",
		ActiveGoal: ActiveGoal{OriginalInstruction: "make the quarterly report as a docx", OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"file_deliver"},
			SelectedEvidenceHints: []string{"bash", "file_deliver"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, instructionBundle, ExecutionPlan{}, false, nil)

	for _, toolName := range []string{"file_deliver"} {
		if !stringSliceContains(contract.RequiredEvidenceTools, toolName) {
			t.Fatalf("expected an active continuation to require %s evidence, got %+v", toolName, contract.RequiredEvidenceTools)
		}
	}
}

func TestOutcomeContractPreservesGoalDuringApprovalContinuation(t *testing.T) {
	request := AgentRequest{
		Prompt: "check",
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"task_delete"},
			SelectedEvidenceHints: []string{"task_delete"},
		}},
	}

	contract := outcomeContractForRequest(request, IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeMaintenanceTask}, InstructionBundle{}, ExecutionPlan{}, false, nil)

	if !stringSliceContains(contract.RequiredEvidenceTools, "task_delete") {
		t.Fatalf("expected task_delete evidence to remain, got %+v", contract)
	}
}

func TestAgentTurnToolSetExposesSendToolForActiveSendContinuation(t *testing.T) {
	toolSet := testToolSet([]string{"message_send", "write"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "direct-message",
			ToolReferences: []string{"message_send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "do it again",
		PinnedToolNames: []string{"message_send"},
		ActiveGoal: ActiveGoal{
			OriginalInstruction: "send Dana a DM saying test",
			OutcomeContract: OutcomeContract{
				RequiredEvidenceTools: []string{"message_send"},
				SelectedEvidenceHints: []string{"message_send"},
			},
		},
	}
	contract := OutcomeContract{RequiredEvidenceTools: []string{"message_send"}, SelectedEvidenceHints: []string{"message_send"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("message_send") {
		t.Fatalf("expected pinned send tool to remain available for continuation, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestAgentTurnToolSetHidesUnrequestedSendToolForAttachmentFollowUp(t *testing.T) {
	toolSet := testToolSet([]string{"message_send", "file_preview", "file_read"})
	instructionBundle := InstructionBundle{
		Skills: []SkillInstruction{{
			Name:           "direct-message",
			ToolReferences: []string{"message_send"},
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "direct-message", Status: "selected"}},
	}
	request := AgentRequest{
		Prompt:          "let's try again",
		PinnedToolNames: []string{"file_preview"},
		VisibleContext: VisibleContext{
			Materials: []VisibleContextMaterial{{
				MaterialID:  "mattermost:file-1",
				Path:        "home/inbox/mattermost/direct/post/kim-intern-automation.html",
				ContentType: "text/html",
			}},
		},
		ActiveGoal: ActiveGoal{OutcomeContract: OutcomeContract{
			SelectedEvidenceHints: []string{"message_send"},
		}},
	}
	contract := OutcomeContract{SelectedEvidenceHints: []string{"message_send"}}

	filteredToolSet := toolSetForAgentTurn(toolSet, instructionBundle, request, ExecutionPlan{}, false, contract)

	if !filteredToolSet.IsAllowed("message_send") {
		t.Fatalf("expected selected direct send tool to remain available, got %+v", filteredToolSet.ListToolNames())
	}
	if !filteredToolSet.IsAllowed("file_preview") {
		t.Fatalf("expected attachment preview to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestOutcomeReferenceToolSetKeepsSendToolsForExplicitSendGoal(t *testing.T) {
	toolSet := testToolSet([]string{"web_fetch", "message_send", "mail_message_send"})

	filteredToolSet := toolSetForOutcomeReference(toolSet, AgentRequest{Prompt: "send Dana a DM"}, ExecutionPlan{}, false, OutcomeContract{RequiredEvidenceTools: []string{"message_send"}})

	if !filteredToolSet.IsAllowed("message_send") {
		t.Fatalf("expected DM send to remain available, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestConfirmationHintsIgnoreUnrelatedSelectedSkillEvidence(t *testing.T) {
	hints := confirmationEvidenceHintsForRequest(
		AgentRequest{Prompt: "https://example.com use it to write the business plan"},
		IntakeDecision{Classification: IntakeClassificationBoundedTask, TaskShape: TaskShapeResearchTask},
		[]string{"task_add", "message_send"},
	)

	if len(hints) != 0 {
		t.Fatalf("expected unrelated skill evidence not to force confirmation planning, got %+v", hints)
	}
}

func TestAttachmentSuffixesComeFromStructuredOutputFormats(t *testing.T) {
	suffixes := attachmentSuffixesForRequestedOutputFormats([]string{"html", "pdf", "html"})

	if len(suffixes) != 2 || suffixes[0] != ".html" || suffixes[1] != ".pdf" {
		t.Fatalf("expected structured output format suffixes, got %+v", suffixes)
	}
}
