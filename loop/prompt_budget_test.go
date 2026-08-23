package loop

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const promptBudgetPath = "testdata/prompt-budget.json"

type promptBudget struct {
	MaximumTotalBytes int                        `json:"maximumTotalBytes"`
	Fixtures          map[string]promptSizeParts `json:"fixtures"`
}

type promptSizeParts struct {
	TotalBytes        int `json:"totalBytes"`
	InstructionBytes  int `json:"instructionBytes"`
	ToolCatalogBytes  int `json:"toolCatalogBytes"`
	ActionSchemaBytes int `json:"actionSchemaBytes"`
	MessageBytes      int `json:"messageBytes"`
}

func TestTheAssembledPromptStaysWithinItsBudget(t *testing.T) {
	budget := readPromptBudget(t)
	measured := map[string]promptSizeParts{}
	for name, state := range promptBudgetFixtures() {
		measured[name] = measureAssembledPrompt(state)
	}

	for _, name := range sortedFixtureNames(measured) {
		parts := measured[name]
		if parts.TotalBytes > budget.MaximumTotalBytes {
			t.Errorf("%s assembles %d bytes against a ceiling of %d; move text out of the prompt, or raise the ceiling in %s with a reason in the pull request", name, parts.TotalBytes, budget.MaximumTotalBytes, promptBudgetPath)
		}
		if parts != budget.Fixtures[name] {
			t.Errorf("%s changed what it puts in front of the model.\nwas  %+v\nnow  %+v\nUpdate %s so the delta is in the diff a reviewer reads.", name, budget.Fixtures[name], parts, promptBudgetPath)
		}
	}
}

func measureAssembledPrompt(state agentTaskState) promptSizeParts {
	request := buildAgentActionRequest(state, true, false)
	messageBytes := 0
	for _, message := range request.Messages {
		messageBytes += len(message.Content)
		for _, part := range message.Parts {
			messageBytes += len(part.Text)
		}
	}
	return promptSizeParts{
		TotalBytes:        messageBytes + len(request.StructuredOutputSchema.Document),
		InstructionBytes:  len(systemInstructionFor(state.Options, state.Request).Text()),
		ToolCatalogBytes:  len(buildAgentToolDescription(modelCallableToolSet(state.Request.ToolSet, false))),
		ActionSchemaBytes: len(request.StructuredOutputSchema.Document),
		MessageBytes:      messageBytes,
	}
}

func promptBudgetFixtures() map[string]agentTaskState {
	turnStartedAt := time.Date(2026, time.August, 22, 9, 0, 0, 0, time.UTC)
	return map[string]agentTaskState{
		"workspace task": {
			TurnStartedAt: turnStartedAt,
			Request: AgentTurnRequest{
				Prompt:            "fix the failing test",
				TurnStartedAt:     turnStartedAt,
				WorkspaceRootPath: "/workspace",
				ToolSet:           newTestToolSet([]string{toolcontract.TerminalRunToolName, toolcontract.FileReadToolName, toolcontract.FileEditToolName}),
			},
		},
		"conversation task with skills": {
			TurnStartedAt: turnStartedAt,
			Request: AgentTurnRequest{
				Prompt:         "add tomorrow's standup to the calendar",
				TurnStartedAt:  turnStartedAt,
				ConversationID: "conversation-1",
				RequesterName:  "이샘플",
				ToolSet:        newTestToolSet([]string{toolcontract.SkillSearchToolName, toolcontract.AskInputToolName, toolcontract.PlanUpdateToolName}),
				AvailableSkills: []SkillInstruction{
					{Name: "calendar", ToolReferences: []string{"calendar_create", "calendar_list"}},
					{Name: "direct-message", ToolReferences: []string{"message_send"}},
				},
			},
		},
	}
}

func sortedFixtureNames(measured map[string]promptSizeParts) []string {
	names := make([]string, 0, len(measured))
	for name := range measured {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func readPromptBudget(t *testing.T) promptBudget {
	t.Helper()
	document, errorValue := os.ReadFile(promptBudgetPath)
	if errorValue != nil {
		t.Fatalf("reading %s failed: %v", promptBudgetPath, errorValue)
	}
	budget := promptBudget{}
	if errorValue := json.Unmarshal(document, &budget); errorValue != nil {
		t.Fatalf("parsing %s failed: %v", promptBudgetPath, errorValue)
	}
	return budget
}
