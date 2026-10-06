package loop

import (
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestNormalizePersistedActiveGoalMigratesLegacyToolNames(t *testing.T) {
	activeGoal := ActiveGoal{
		RequiredNextTools: []string{"terminal.session", "file.attach"},
		SelectedToolNames: []string{"terminal.session", "file.attach"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"file.attach", "artifact.deliver"},
			RequiredEvidenceAnyOf: [][]string{{"ask_choice", "terminal.session"}},
			SelectedEvidenceHints: []string{"artifact.deliver"},
			ExpectedResults: []ExpectedResult{{
				Description:     "choice",
				Required:        true,
				AcceptanceHints: []string{"ask_choice"},
			}},
			RequiredEffects: []OutcomeEffect{{
				ObjectType:         "file",
				Effect:             "delivered",
				SuggestedNextTools: []string{"artifact.deliver"},
			}},
		},
	}

	normalizedGoal := normalizePersistedActiveGoal(activeGoal)

	assertSameStrings(t, normalizedGoal.RequiredNextTools, []string{toolcontract.BashToolName, toolcontract.FileDeliverToolName})
	assertSameStrings(t, normalizedGoal.SelectedToolNames, []string{toolcontract.BashToolName, toolcontract.FileDeliverToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEvidenceTools, []string{toolcontract.FileDeliverToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEvidenceAnyOf[0], []string{toolcontract.AskInputToolName, toolcontract.BashToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.SelectedEvidenceHints, []string{toolcontract.FileDeliverToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.ExpectedResults[0].AcceptanceHints, []string{toolcontract.AskInputToolName})
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEffects[0].SuggestedNextTools, []string{toolcontract.FileDeliverToolName})
}

func TestNormalizePersistedActiveGoalCanonicalizesRenamedKernelTools(t *testing.T) {
	legacyToolNames := []string{"shell", "file_write", "file_edit", "find_tools"}
	activeGoal := ActiveGoal{
		SelectedToolNames: append([]string{}, legacyToolNames...),
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: append([]string{}, legacyToolNames...),
		},
	}

	normalizedGoal := normalizePersistedActiveGoal(activeGoal)

	canonicalToolNames := []string{
		toolcontract.BashToolName,
		toolcontract.WriteToolName,
		toolcontract.EditToolName,
		toolcontract.EquipToolName,
	}
	assertSameStrings(t, normalizedGoal.SelectedToolNames, canonicalToolNames)
	assertSameStrings(t, normalizedGoal.OutcomeContract.RequiredEvidenceTools, canonicalToolNames)
}

func TestNormalizePersistedActiveGoalDoesNotMutateSource(t *testing.T) {
	activeGoal := ActiveGoal{
		RequiredNextTools: []string{"artifact.deliver"},
		SelectedToolNames: []string{"file.attach"},
		OutcomeContract: OutcomeContract{
			RequiredEvidenceTools: []string{"artifact.deliver"},
		},
	}

	normalizePersistedActiveGoal(activeGoal)

	assertSameStrings(t, activeGoal.RequiredNextTools, []string{"artifact.deliver"})
	assertSameStrings(t, activeGoal.SelectedToolNames, []string{"file.attach"})
	assertSameStrings(t, activeGoal.OutcomeContract.RequiredEvidenceTools, []string{"artifact.deliver"})
}

func TestNormalizeOutcomeContractRequiresDeliveryForRequiredFileResult(t *testing.T) {
	contract := normalizeOutcomeContract(OutcomeContract{
		RequiredEvidenceTools: []string{"write"},
		ExpectedResults: []ExpectedResult{{
			Type:        ExpectedResultTypeFile,
			Description: "attached report",
			Required:    true,
		}},
	})

	assertSameStrings(t, contract.RequiredEvidenceTools, []string{"write", toolcontract.FileDeliverToolName})
	if contract.ArtifactRequirement != ArtifactRequirementRequired {
		t.Fatalf("expected required artifact, got %q", contract.ArtifactRequirement)
	}
}

func TestNormalizePersistedActiveGoalRestoresFileDeliveryInvariant(t *testing.T) {
	activeGoal := normalizePersistedActiveGoal(ActiveGoal{OutcomeContract: OutcomeContract{
		ExpectedResults: []ExpectedResult{{
			Type:        ExpectedResultTypeFile,
			Description: "attached report",
			Required:    true,
		}},
	}})

	assertSameStrings(t, activeGoal.OutcomeContract.RequiredEvidenceTools, []string{toolcontract.FileDeliverToolName})
	if activeGoal.OutcomeContract.ArtifactRequirement != ArtifactRequirementRequired {
		t.Fatalf("expected persisted required artifact, got %q", activeGoal.OutcomeContract.ArtifactRequirement)
	}
}

func TestNormalizeOutcomeContractDoesNotAddDeliveryForMessageResult(t *testing.T) {
	contract := normalizeOutcomeContract(OutcomeContract{ExpectedResults: []ExpectedResult{{
		Type:        ExpectedResultTypeMessage,
		Description: "final reply",
		Required:    true,
	}}})

	if len(contract.RequiredEvidenceTools) != 0 {
		t.Fatalf("expected no delivery requirement, got %+v", contract.RequiredEvidenceTools)
	}
	if contract.ArtifactRequirement != ArtifactRequirementNone {
		t.Fatalf("expected no artifact requirement, got %q", contract.ArtifactRequirement)
	}
}

func assertSameStrings(t *testing.T, actual []string, expected []string) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("expected %+v, got %+v", expected, actual)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("expected %+v, got %+v", expected, actual)
		}
	}
}
