package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

import "testing"

func TestToolExposureUsesKernelWithoutSelectedSkills(t *testing.T) {
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

	if got := filteredToolSet.ListToolNames(); !sameStringSet(got, toolcontract.KernelToolNames()) {
		t.Fatalf("expected fixed kernel tools, got %+v", got)
	}
	for _, hiddenToolName := range []string{"site_serve", "site_serve", "message_send"} {
		if filteredToolSet.IsAllowed(hiddenToolName) {
			t.Fatalf("expected non-kernel tool %s to be hidden, got %+v", hiddenToolName, filteredToolSet.ListToolNames())
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

func TestToolExposureRequiresExplicitPinForImmediateReply(t *testing.T) {
	toolSet := testToolSet(append(toolcontract.KernelToolNames(), "task_add"))
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
	if filteredToolSet.IsAllowed("task_add") {
		t.Fatalf("expected immediate reply to hide unpinned tools, got %+v", filteredToolSet.ListToolNames())
	}

	request.PinnedToolNames = []string{"task_add"}
	filteredToolSet, _ = toolSetForAgentTurnWithExposure(
		toolSet,
		InstructionBundle{},
		request,
		ExecutionPlan{},
		false,
		OutcomeContract{},
		ToolExposureEvent{},
	)
	if !filteredToolSet.IsAllowed("task_add") {
		t.Fatalf("expected the pinned tool to be exposed, got %+v", filteredToolSet.ListToolNames())
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

	expectedToolNames := append(toolcontract.KernelToolNames(), flowToolNames...)
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
