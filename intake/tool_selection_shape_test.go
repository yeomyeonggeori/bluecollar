package intake

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type questionRecordingDecisionModel struct {
	outcome            intaketest.Outcome
	singleToolChoice   map[string]float64
	mutex              sync.Mutex
	askedQuestionNames []string
}

func (decisionModel *questionRecordingDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	for questionName := range request.Questions {
		decisionModel.askedQuestionNames = append(decisionModel.askedQuestionNames, questionName)
	}
	decisionModel.mutex.Unlock()
	answers := intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return decisionModel.outcome })
	if decisionModel.singleToolChoice != nil {
		for questionName := range request.Questions {
			if strings.HasSuffix(questionName, "."+agentcontract.IntakeQuestionSingleToolChoice) {
				answers[questionName] = model.DecisionAnswer{
					Type:          model.DecisionQuestionTypeChoice,
					Probabilities: decisionModel.singleToolChoice,
				}
			}
		}
	}
	return model.DecisionResponse{Answers: answers}, nil
}

func (decisionModel *questionRecordingDecisionModel) askedAboutEveryTool() bool {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	for _, questionName := range decisionModel.askedQuestionNames {
		if _, isToolQuestion := toolNameOfQuestion(questionName); isToolQuestion {
			return true
		}
	}
	return false
}

func (decisionModel *questionRecordingDecisionModel) askedForOneChoice() bool {
	decisionModel.mutex.Lock()
	defer decisionModel.mutex.Unlock()
	for _, questionName := range decisionModel.askedQuestionNames {
		if strings.HasSuffix(questionName, "."+agentcontract.IntakeQuestionSingleToolChoice) {
			return true
		}
	}
	return false
}

func outcomeNeedingToolCount(count agentcontract.ExpectedToolCount, toolNames ...string) intaketest.Outcome {
	outcome := startTaskOutcome()
	outcome.TurnDecision.ExpectedToolCount = count
	outcome.TurnDecision.InitialToolNames = toolNames
	return outcome
}

func selectedToolNamesOfFirstMessage(t *testing.T, decisionModel model.DecisionModel, toolNames []string) []string {
	t.Helper()
	request := addressedDecisionRequest("이번 주 할 일 목록 보여줘")
	request.ToolSet = newTestToolSet(toolNames)
	decisions, errorValue := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }).Decide(context.Background(), request, &agentcontract.IntakeCallLedger{})
	if errorValue != nil {
		t.Fatalf("decide: %v", errorValue)
	}
	return decisions.Messages[0].TurnFields.InitialToolNames
}

func TestWorkThatNeedsOneToolAsksOneChoiceRatherThanEveryTool(t *testing.T) {
	decisionModel := &questionRecordingDecisionModel{outcome: outcomeNeedingToolCount(agentcontract.ExpectedToolCountOne, "task_add")}

	selected := selectedToolNamesOfFirstMessage(t, decisionModel, measurementToolNames())

	if decisionModel.askedAboutEveryTool() {
		t.Fatalf("expected work that needs one tool to skip the per-tool questions, but they were asked")
	}
	if !decisionModel.askedForOneChoice() {
		t.Fatalf("expected one choice over the catalog, and none was asked")
	}
	if len(selected) != 1 || selected[0] != "task_add" {
		t.Fatalf("expected the chosen tool alone, got %v", selected)
	}
}

func TestWorkThatNeedsSeveralToolsStillAsksAboutEveryTool(t *testing.T) {
	decisionModel := &questionRecordingDecisionModel{outcome: outcomeNeedingToolCount(agentcontract.ExpectedToolCountSeveral, "task_add", "message_send")}

	selectedToolNamesOfFirstMessage(t, decisionModel, measurementToolNames())

	if !decisionModel.askedAboutEveryTool() {
		t.Fatalf("expected work that needs several tools to be judged tool by tool, and it was not")
	}
	if decisionModel.askedForOneChoice() {
		t.Fatalf("expected no single-tool choice when the work needs several, and one was asked")
	}
}

func TestOneToolChoiceExposesEveryToolTheBeliefCovers(t *testing.T) {
	decisionModel := &questionRecordingDecisionModel{
		outcome:          outcomeNeedingToolCount(agentcontract.ExpectedToolCountOne, "task_add"),
		singleToolChoice: map[string]float64{"task_add": 0.52, "task_update": 0.41, "task_list": 0.04},
	}

	selected := selectedToolNamesOfFirstMessage(t, decisionModel, measurementToolNames())

	if len(selected) != 2 {
		t.Fatalf("expected a split belief to expose both tools it covers, got %v", selected)
	}
}

func TestOneToolChoiceLeavesOutWhatTheBeliefBarelyTouches(t *testing.T) {
	decisionModel := &questionRecordingDecisionModel{
		outcome:          outcomeNeedingToolCount(agentcontract.ExpectedToolCountOne, "task_add"),
		singleToolChoice: map[string]float64{"task_add": 0.94, "task_update": 0.03, "task_list": 0.03},
	}

	selected := selectedToolNamesOfFirstMessage(t, decisionModel, measurementToolNames())

	if len(selected) != 1 {
		t.Fatalf("expected a settled belief to expose one tool, got %v", selected)
	}
}

func TestOneToolChoiceStopsAtTheTailEvenWhenTheBeliefIsNotCovered(t *testing.T) {
	decisionModel := &questionRecordingDecisionModel{
		outcome: outcomeNeedingToolCount(agentcontract.ExpectedToolCountOne, "task_add"),
		singleToolChoice: map[string]float64{
			"task_add": 0.25, "task_update": 0.22, "task_list": 0.20, "message_send": 0.18,
			"web_search": 0.04, "web_fetch": 0.04, "event_add": 0.04, "event_list": 0.03,
		},
	}

	selected := selectedToolNamesOfFirstMessage(t, decisionModel, measurementToolNames())

	if len(selected) != 4 {
		t.Fatalf("expected the tail below the floor to stay out even though the belief is only 0.85 covered, got %v", selected)
	}
}
