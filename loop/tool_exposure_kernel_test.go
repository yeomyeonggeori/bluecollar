package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

import "testing"

func TestToolExposureUsesKernelAndTheWholeCatalogWithoutSelectedSkills(t *testing.T) {
	toolSet := testToolSet(append(toolcontract.KernelToolNames(),
		"site_serve",
		"site_serve",
		"message_send",
	))

	filteredToolSet, event := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{Prompt: "create and publish a site"},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	expectedToolNames := append(toolcontract.KernelToolNames(), "site_serve", "message_send")
	if got := filteredToolSet.ListToolNames(); !sameStringSet(got, expectedToolNames) {
		t.Fatalf("expected kernel tools plus the whole catalog, got %+v", got)
	}
	for _, catalogToolName := range []string{"site_serve", "message_send"} {
		if !filteredToolSet.IsAllowed(catalogToolName) {
			t.Fatalf("expected non-kernel tool %s to reach the model, got %+v", catalogToolName, filteredToolSet.ListToolNames())
		}
	}
	for _, kernelToolName := range []string{"file_read", "file_write", "file_edit", "file_preview", "image_read"} {
		if !filteredToolSet.IsAllowed(kernelToolName) {
			t.Fatalf("expected coding kernel tool %s to be exposed, got %+v", kernelToolName, filteredToolSet.ListToolNames())
		}
	}
	if event.SelectionSource != "fixed_kernel" || event.UsedFallbackGroups {
		t.Fatalf("expected fixed kernel exposure event, got %+v", event)
	}
}

func TestToolExposureHidesSkillSearchAfterSelectedInstructionsLoad(t *testing.T) {
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), "task_add"))
	instructionBundle := InstructionBundle{
		Skills:         []SkillInstruction{{Name: "internkim-flow", ToolReferences: []string{"task_add"}}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	if filteredToolSet.IsAllowed(toolcontract.SkillSearchToolName) {
		t.Fatalf("expected loaded skill instructions to hide skill_search, got %+v", filteredToolSet.ListToolNames())
	}
	if !filteredToolSet.IsAllowed("task_add") {
		t.Fatalf("expected selected skill tool to remain exposed, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestToolExposureKeepsSkillSearchWhenSelectedInstructionIsMissing(t *testing.T) {
	toolSet := testToolSet(toolcontract.KernelToolNames())
	instructionBundle := InstructionBundle{
		SkillDecisions: []SkillSelectionDecision{{Name: "missing", Status: "selected"}},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundle,
		AgentRequest{},
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)

	if !filteredToolSet.IsAllowed(toolcontract.SkillSearchToolName) {
		t.Fatalf("expected unresolved skill discovery to keep skill_search, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestToolExposureAddsAskInputOnlyForTypedInteraction(t *testing.T) {
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), toolcontract.AskInputToolName))
	outcomeContract := OutcomeContract{ExpectedResults: []ExpectedResult{{
		ID:              "interactive-choice",
		Type:            ExpectedResultTypeMessage,
		Description:     "The user can choose one of the presented options.",
		Required:        true,
		AcceptanceHints: []string{toolcontract.AskInputToolName},
	}}}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		AgentRequest{},
		ExecutionPlan{},
		false,
		outcomeContract,
		ToolExposureEvent{},
	)

	if !filteredToolSet.IsAllowed(toolcontract.AskInputToolName) {
		t.Fatalf("expected typed interactive outcome to expose ask_input, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestToolExposureRequiresExplicitSkillSearchForImmediateReply(t *testing.T) {
	toolSet := testToolSet(toolcontract.KernelToolNames())
	request := AgentRequest{TaskShape: TaskShapeImmediateReply}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		request,
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)
	if filteredToolSet.IsAllowed(toolcontract.SkillSearchToolName) {
		t.Fatalf("expected immediate reply to hide unrequested skill_search, got %+v", filteredToolSet.ListToolNames())
	}

	request.PinnedToolNames = []string{toolcontract.SkillSearchToolName}
	filteredToolSet, _ = toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		request,
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)
	if !filteredToolSet.IsAllowed(toolcontract.SkillSearchToolName) {
		t.Fatalf("expected typed initial tool to expose skill_search, got %+v", filteredToolSet.ListToolNames())
	}
}

func TestInstructionBundleFromTurnRequestPreservesContractWorkingSet(t *testing.T) {
	instructionBundle := instructionBundleFromTurnRequest(AgentTurnRequest{
		ContractToolWorkingSet: ContractToolWorkingSet{
			RequiredNextTools:     []string{"task_add"},
			RequiredEvidenceTools: []string{"task_add"},
		},
	})

	if !sameStringSet(instructionBundle.RequiredNextTools, []string{"task_add"}) {
		t.Fatalf("expected required next tools to survive reconstruction, got %+v", instructionBundle)
	}
	if !instructionBundle.HasContractSkillArbitration {
		t.Fatalf("expected arbitration authority to survive reconstruction, got %+v", instructionBundle)
	}
	if !sameStringSet(instructionBundle.RequiredEvidenceTools, []string{"task_add"}) {
		t.Fatalf("expected arbitrated evidence to survive reconstruction, got %+v", instructionBundle)
	}
}

func TestReconstructedEvidenceOnlyArbitrationPreservesEvidenceWorkingSet(t *testing.T) {
	flowToolNames := []string{"task_add", "task_list", "task_update", "task_delete"}
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), flowToolNames...))
	request := AgentTurnRequest{
		ToolSet: toolSet,
		AvailableSkills: []SkillInstruction{{
			Name:           "internkim-flow",
			ToolReferences: flowToolNames,
		}},
		SkillDecisions: []SkillSelectionDecision{{Name: "internkim-flow", Status: "selected"}},
		ContractToolWorkingSet: ContractToolWorkingSet{
			RequiredEvidenceTools: []string{"task_add"},
		},
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"task_add"}},
	}

	filteredToolSet, _ := toolSetForAgentTurnWithExposure(
		toolSet,
		instructionBundleFromTurnRequest(request),
		AgentRequest{},
		ExecutionPlan{},
		false,
		request.OutcomeContract,
		ToolExposureEvent{},
	)

	expectedToolNames := append(kernelToolNamesForInstructionBundle(instructionBundleFromTurnRequest(request)), flowToolNames...)
	if !sameStringSet(filteredToolSet.ListToolNames(), expectedToolNames) {
		t.Fatalf("expected reconstructed evidence working set with skill tools, got %+v", filteredToolSet.ListToolNames())
	}
}

func sameStringSet(leftValues []string, rightValues []string) bool {
	if len(leftValues) != len(rightValues) {
		return false
	}
	rightValueByValue := map[string]bool{}
	for _, rightValue := range rightValues {
		rightValueByValue[rightValue] = true
	}
	for _, leftValue := range leftValues {
		if !rightValueByValue[leftValue] {
			return false
		}
	}
	return true
}
