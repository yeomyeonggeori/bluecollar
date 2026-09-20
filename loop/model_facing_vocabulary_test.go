package loop

import (
	"strconv"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestThePromptNeverNamesARetiredAction(t *testing.T) {
	retiredWords := []string{"finish", "ask_input", "file_deliver", "plan_update", "request_tools"}
	for name, promptText := range modelFacingPromptStrings() {
		for _, retiredWord := range retiredWords {
			if strings.Contains(promptText, retiredWord) {
				t.Errorf("%s still names %q to the model, which can only reply and continue now", name, retiredWord)
			}
		}
	}
}

func modelFacingPromptStrings() map[string]string {
	promptStrings := map[string]string{}
	for stateName, state := range kernelPromptStates() {
		request := buildAgentActionRequest(state, true, false)
		promptStrings[stateName+" action schema"] = string(request.StructuredOutputSchema.Document)
		for index, message := range request.Messages {
			promptStrings[stateName+" message "+strconv.Itoa(index)] = message.Content
		}
	}
	return promptStrings
}

func kernelPromptStates() map[string]agentTaskState {
	kernelToolSet := newTestToolSet([]string{
		toolcontract.ReadToolName,
		toolcontract.FileWriteToolName,
		toolcontract.FileEditToolName,
		toolcontract.ShellToolName,
		toolcontract.PlanToolName,
		toolcontract.FindToolsToolName,
	})
	request := AgentTurnRequest{
		Prompt:            "어제 회의록을 정리해서 파일로 보내줘",
		WorkspaceRootPath: "/workspace",
		ConversationID:    "conversation-1",
		RequesterName:     "이샘플",
		TaskLevel:         TaskLevelMedium,
		ToolSet:           kernelToolSet,
		OutcomeContract: OutcomeContract{
			ArtifactRequirement: ArtifactRequirementRequired,
			ExpectedResults:     []ExpectedResult{{ID: "attached-file", Type: ExpectedResultTypeFile, Description: "정리된 회의록 파일", Required: true}},
		},
	}
	failedState := buildInitialAgentTaskState(request, TurnOptions{}, "task-run-1")
	failedState.Observations = []turnObservation{toolFailureObservation("obs-001", toolcontract.ShellToolName, "command exited with status 1")}
	return map[string]agentTaskState{
		"kernel turn":                buildInitialAgentTaskState(request, TurnOptions{}, "task-run-1"),
		"kernel turn with a failure": failedState,
	}
}
