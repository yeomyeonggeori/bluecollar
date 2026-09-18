package intake

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func measurementToolNames() []string {
	return []string{
		"attendance_add", "attendance_delete", "attendance_list", "attendance_update",
		"company_document_download", "company_document_list", "company_document_register",
		"company_document_search", "company_document_update", "company_document_upload",
		"company_holiday_add", "company_holiday_delete", "company_holiday_list", "company_holiday_update",
		"company_info_get", "company_info_set", "company_metric_list", "company_metric_record",
		"company_record_add", "company_record_delete", "company_record_list", "company_record_update",
		"crm_activity_list", "crm_activity_save", "crm_contact_add", "crm_contact_archive",
		"crm_contact_list", "crm_contact_update", "crm_opportunity_add", "crm_opportunity_archive",
		"crm_opportunity_list", "crm_opportunity_move", "crm_opportunity_update",
		"crm_organization_add", "crm_organization_list", "crm_organization_update",
		"event_add", "event_delete", "event_list", "event_update",
		"image_generate", "leave_balance", "leave_decide", "leave_delete", "leave_list",
		"leave_request", "leave_update", "mail_message_list", "mail_message_read",
		"mail_message_search", "mail_message_send", "message_context", "message_search",
		"message_send", "message_update", "person_invite", "person_list", "person_update",
		"site_list", "site_serve", "site_unserve", "task_add", "task_delete", "task_list",
		"task_update", "team_add", "team_list", "team_update", "web_fetch", "web_search",
	}
}

func TestEveryToolAboveTheThresholdIsSelectedAndTheRestAreNot(t *testing.T) {
	candidateToolNames := []string{"task_add", "task_list", "web_search"}
	probabilities := map[string]float64{"task_add": 0.91, "task_list": likelyToolProbabilityThreshold, "web_search": likelyToolProbabilityThreshold - 0.01}

	selectedToolNames := selectLikelyToolNames(probabilities, candidateToolNames)

	if strings.Join(selectedToolNames, ",") != "task_add,task_list" {
		t.Fatalf("expected the two tools at or above the threshold in probability order, got %v", selectedToolNames)
	}
}

func TestTheSelectionStopsAtTheCountLimitAndKeepsTheLikeliestTools(t *testing.T) {
	candidateToolNames := measurementToolNames()
	probabilities := map[string]float64{}
	for index, toolName := range candidateToolNames {
		probabilities[toolName] = 0.99 - float64(index)/10000
	}

	selectedToolNames := selectLikelyToolNames(probabilities, candidateToolNames)

	if len(selectedToolNames) != likelyToolCountLimit {
		t.Fatalf("expected the cap to hold at %d, got %d", likelyToolCountLimit, len(selectedToolNames))
	}
	if selectedToolNames[0] != candidateToolNames[0] || selectedToolNames[likelyToolCountLimit-1] != candidateToolNames[likelyToolCountLimit-1] {
		t.Fatalf("expected the likeliest tools to survive the cap, got %v", selectedToolNames)
	}
}

func TestTiedToolsAreOrderedByNameWhateverOrderTheCandidatesArrivedIn(t *testing.T) {
	probabilities := map[string]float64{"task_add": 0.8, "task_list": 0.8, "web_search": 0.8}

	forwardSelection := selectLikelyToolNames(probabilities, []string{"task_add", "task_list", "web_search"})
	reversedSelection := selectLikelyToolNames(probabilities, []string{"web_search", "task_list", "task_add"})

	if strings.Join(forwardSelection, ",") != "task_add,task_list,web_search" {
		t.Fatalf("expected ties to be broken by name, got %v", forwardSelection)
	}
	if strings.Join(reversedSelection, ",") != strings.Join(forwardSelection, ",") {
		t.Fatalf("expected the candidate order not to decide the selection, got %v and %v", forwardSelection, reversedSelection)
	}
}

func TestATurnThatNeedsNoToolSelectsNone(t *testing.T) {
	probabilities := map[string]float64{"task_add": 0.1, "task_list": 0.02}

	if selectedToolNames := selectLikelyToolNames(probabilities, []string{"task_add", "task_list"}); len(selectedToolNames) != 0 {
		t.Fatalf("expected nothing to be selected, got %v", selectedToolNames)
	}
}

func TestTheAlwaysExposedKernelToolsAreNeverAskedAbout(t *testing.T) {
	request := addressedDecisionRequest("워크스페이스 파일 정리해서 결과 알려줘")
	request.ToolSet = newTestToolSet(append(toolcontract.KernelToolNames(), "task_add", "message_send"))
	builderRequest, builder := decisionQuestionBuilder(request)

	state := buildDecisionState(builderRequest, decisionToolDescriptions(request.ToolSet, builder.toolNames))
	questions := builder.toolQuestions(builder.toolNames)

	for _, kernelToolName := range toolcontract.KernelToolNames() {
		if containsString(builder.toolNames, kernelToolName) {
			t.Fatalf("expected %s to be exposed without being asked about, got %v", kernelToolName, builder.toolNames)
		}
		for questionName := range questions {
			if askedToolName, isToolQuestion := toolNameOfQuestion(questionName); isToolQuestion && askedToolName == kernelToolName {
				t.Fatalf("expected no question about %s, got %s", kernelToolName, questionName)
			}
		}
		for _, describedTool := range state.AvailableTools {
			if describedTool.Name == kernelToolName {
				t.Fatalf("expected %s to stay out of the state, got %v", kernelToolName, state.AvailableTools)
			}
		}
	}
	if !containsString(builder.toolNames, "task_add") || !containsString(builder.toolNames, "message_send") {
		t.Fatalf("expected the extension tools to stay candidates, got %v", builder.toolNames)
	}
}

func TestThePerToolQuestionStaysSmallEnoughToRepeatPerMessage(t *testing.T) {
	request := addressedDecisionRequest("지난 분기 매출 정리해서 덱 만들어줘")
	request.ToolSet = newTestToolSet(measurementToolNames())
	_, builder := decisionQuestionBuilder(request)

	questionBytes := decisionQuestionsByteCount(builder.toolQuestions(builder.toolNames))

	if averageBytes := questionBytes / len(builder.toolNames); averageBytes > perToolQuestionByteBudget {
		t.Fatalf("expected a tool question to stay within %d bytes, got %d; shared guidance belongs in the state", perToolQuestionByteBudget, averageBytes)
	}
}

const perToolQuestionByteBudget = 220

func TestTheToolGuidanceIsCarriedOnceByTheStateRatherThanByEachQuestion(t *testing.T) {
	request := addressedDecisionRequest("지난 분기 매출 정리해서 덱 만들어줘")
	request.ToolSet = newTestToolSet([]string{"task_add", "task_list"})
	builderRequest, builder := decisionQuestionBuilder(request)

	state := buildDecisionState(builderRequest, decisionToolDescriptions(request.ToolSet, builder.toolNames))
	if !strings.Contains(state.ToolGuidance, "at any point before the work is done") {
		t.Fatalf("expected the state to ask about the whole job, got %q", state.ToolGuidance)
	}
	if !strings.Contains(state.ToolGuidance, "Do not raise a tool because it exists") {
		t.Fatalf("expected the guidance to pair the must-check with a must-not-invent, got %q", state.ToolGuidance)
	}
	for questionName, question := range builder.toolQuestions(builder.toolNames) {
		if strings.Contains(question.Instructions, "at any point before the work is done") {
			t.Fatalf("expected %s to carry no copy of the shared guidance, got %q", questionName, question.Instructions)
		}
	}
}

func TestTheStateCarriesNoToolGuidanceWhenNoToolIsCallable(t *testing.T) {
	request := addressedDecisionRequest("안녕하세요")

	state := buildDecisionState(request, nil)

	if state.ToolGuidance != "" {
		t.Fatalf("expected no tool guidance without tools, got %q", state.ToolGuidance)
	}
}

func TestATurnSelectsTheLikelyToolsRatherThanTheConfidentOnes(t *testing.T) {
	request := addressedDecisionRequest("이번 주 회의 일정 정리해서 공유해줘")
	request.ToolSet = newTestToolSet([]string{"event_list", "message_send", "web_search"})
	outcome := startTaskOutcome()
	outcome.TurnDecision.InitialToolNames = nil
	outcome.ToolProbabilities = map[string]float64{"event_list": 0.94, "message_send": 0.44, "web_search": 0.04}
	planner := NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil, func() float64 { return 1 })

	decision := decideOnce(t, planner, request)

	if strings.Join(decision.TurnFields.InitialToolNames, ",") != "event_list,message_send" {
		t.Fatalf("expected the later-step tool to come along, got %v", decision.TurnFields.InitialToolNames)
	}
}

func TestTheLedgerCarriesTheProbabilitiesTheSelectionWasMadeFrom(t *testing.T) {
	request := addressedDecisionRequest("이번 주 회의 일정 정리해서 공유해줘")
	request.ToolSet = newTestToolSet([]string{"event_list", "message_send", "web_search"})
	outcome := startTaskOutcome()
	outcome.TurnDecision.InitialToolNames = nil
	outcome.ToolProbabilities = map[string]float64{"event_list": 0.94, "message_send": 0.44, "web_search": 0.01}
	callLedger := &agentcontract.IntakeCallLedger{}

	if _, errorValue := NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil, func() float64 { return 1 }).Decide(context.Background(), request, callLedger); errorValue != nil {
		t.Fatalf("expected the decision call to answer: %v", errorValue)
	}

	toolSelection := callLedger.Records[0].ToolSelection
	if toolSelection == nil {
		t.Fatal("expected the decision record to carry the tool selection")
	}
	if toolSelection.ProbabilityThreshold != likelyToolProbabilityThreshold || toolSelection.CountLimit != likelyToolCountLimit {
		t.Fatalf("expected the rule to be recorded with the selection, got %+v", toolSelection)
	}
	if toolSelection.Probabilities["m1.message_send"] != 0.44 {
		t.Fatalf("expected an unselected tool above the floor to stay diagnosable, got %+v", toolSelection.Probabilities)
	}
	if _, isRecorded := toolSelection.Probabilities["m1.web_search"]; isRecorded {
		t.Fatalf("expected a tool below the floor to be left out, got %+v", toolSelection.Probabilities)
	}
	if strings.Join(toolSelection.SelectedToolNames, ",") != "m1.event_list,m1.message_send" {
		t.Fatalf("expected the selected set to be recorded, got %v", toolSelection.SelectedToolNames)
	}
}

func TestToolQuestionsSplitAcrossRequestsWhenOneWouldOverflowTheBudget(t *testing.T) {
	request := burstDecisionRequest(4, measurementToolNames())

	requests := planDecisionRequests(request)

	if len(requests) < 2 {
		t.Fatalf("expected a burst this size to be split, got %d request(s)", len(requests))
	}
	askedToolQuestions := map[string]bool{}
	nonToolRequestCount := 0
	for _, decisionRequest := range requests {
		if byteCount := decisionRequestByteCount(decisionRequest); byteCount > decisionRequestByteBudget {
			t.Fatalf("expected every part to fit the budget, got %d bytes", byteCount)
		}
		hasNonToolQuestion := false
		for questionName := range decisionRequest.Questions {
			if strings.Contains(questionName, "."+agentcontract.IntakeQuestionPrefixTool) {
				if askedToolQuestions[questionName] {
					t.Fatalf("expected %s to be asked once, got it twice", questionName)
				}
				askedToolQuestions[questionName] = true
				continue
			}
			hasNonToolQuestion = true
		}
		if hasNonToolQuestion {
			nonToolRequestCount++
		}
	}
	if nonToolRequestCount != 1 {
		t.Fatalf("expected the addressing and routing questions to ride in exactly one request, got %d", nonToolRequestCount)
	}
	_, builder := decisionQuestionBuilder(request)
	if len(askedToolQuestions) != len(builder.toolQuestions(builder.toolNames)) {
		t.Fatalf("expected every tool question to survive the split, got %d", len(askedToolQuestions))
	}
}

func TestASplitGivesEveryPartAlmostTheSameNumberOfTools(t *testing.T) {
	for _, partCount := range []int{2, 3, 7, 11} {
		parts := balancedToolNameParts(measurementToolNames(), partCount)
		if len(parts) != partCount {
			t.Fatalf("expected %d parts, got %d", partCount, len(parts))
		}
		smallestPartSize, largestPartSize, totalSize := len(parts[0]), len(parts[0]), 0
		for _, part := range parts {
			smallestPartSize = min(smallestPartSize, len(part))
			largestPartSize = max(largestPartSize, len(part))
			totalSize += len(part)
		}
		if largestPartSize-smallestPartSize > 1 {
			t.Fatalf("expected balanced parts, got sizes between %d and %d", smallestPartSize, largestPartSize)
		}
		if totalSize != len(measurementToolNames()) {
			t.Fatalf("expected every tool to land in a part, got %d of %d", totalSize, len(measurementToolNames()))
		}
	}
}

func TestASplitRequestAsksOnlyAboutTheToolsItsOwnStateDescribes(t *testing.T) {
	request := burstDecisionRequest(4, measurementToolNames())

	for _, decisionRequest := range planDecisionRequests(request) {
		state, isDecisionState := decisionRequest.State.(decisionState)
		if !isDecisionState {
			t.Fatalf("expected a decision state, got %T", decisionRequest.State)
		}
		describedToolName := map[string]bool{}
		for _, describedTool := range state.AvailableTools {
			describedToolName[describedTool.Name] = true
		}
		for questionName := range decisionRequest.Questions {
			toolName, isToolQuestion := toolNameOfQuestion(questionName)
			if isToolQuestion && !describedToolName[toolName] {
				t.Fatalf("expected %s to describe %s in its own state", questionName, toolName)
			}
		}
	}
}

func TestASplitDecisionMergesTheProbabilitiesOfEveryPart(t *testing.T) {
	request := burstDecisionRequest(4, measurementToolNames())
	outcome := startTaskOutcome()
	outcome.TurnDecision.InitialToolNames = nil
	outcome.ToolProbabilities = map[string]float64{"web_search": 0.88, "task_update": 0.77}
	decisionModel := intaketest.NewDecisionModel(outcome)

	decisions, errorValue := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }).Decide(context.Background(), request, nil)

	if errorValue != nil {
		t.Fatalf("expected the split decision to answer: %v", errorValue)
	}
	if len(decisionModel.Requests()) < 2 {
		t.Fatalf("expected the burst to be split, got %d request(s)", len(decisionModel.Requests()))
	}
	for _, decision := range decisions.Messages {
		if strings.Join(decision.TurnFields.InitialToolNames, ",") != "web_search,task_update" {
			t.Fatalf("expected tools from both parts, got %v", decision.TurnFields.InitialToolNames)
		}
	}
}

func TestOnePartFailingTakesTheFailurePathRatherThanSelectingFromTheRest(t *testing.T) {
	request := burstDecisionRequest(4, measurementToolNames())
	outcome := startTaskOutcome()
	outcome.ToolProbabilities = map[string]float64{"web_search": 0.88}

	decisions, errorValue := NewDecisionPlanner(&partFailingDecisionModel{outcome: outcome}, nil, func() float64 { return 1 }).Decide(context.Background(), request, nil)

	if errorValue == nil {
		t.Fatalf("expected a partial answer to fail the decision, got %+v", decisions)
	}
	if len(decisions.Messages) != 0 {
		t.Fatalf("expected no decision to be read from a partial answer, got %+v", decisions)
	}
}

type partFailingDecisionModel struct {
	outcome       intaketest.Outcome
	mutex         sync.Mutex
	answeredCount int
}

func (decisionModel *partFailingDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	decisionModel.answeredCount++
	answeredCount := decisionModel.answeredCount
	decisionModel.mutex.Unlock()
	if answeredCount > 1 {
		return model.DecisionResponse{}, context.DeadlineExceeded
	}
	return model.DecisionResponse{Answers: intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return decisionModel.outcome })}, nil
}

func burstDecisionRequest(messageCount int, toolNames []string) agentcontract.IntakeDecisionRequest {
	request := addressedDecisionRequest("지난 분기 매출 정리해서 덱 만들어줘")
	request.ToolSet = newTestToolSet(toolNames)
	for index := 1; index < messageCount; index++ {
		request.Messages = append(request.Messages, request.Messages[0])
	}
	return request
}

func toolNameOfQuestion(questionName string) (string, bool) {
	separatorIndex := strings.Index(questionName, "."+agentcontract.IntakeQuestionPrefixTool)
	if separatorIndex < 0 {
		return "", false
	}
	return questionName[separatorIndex+len("."+agentcontract.IntakeQuestionPrefixTool):], true
}
