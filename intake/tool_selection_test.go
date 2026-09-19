package intake

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func numberedToolNames(count int) []string {
	toolNames := make([]string, 0, count)
	for index := range count {
		toolNames = append(toolNames, "candidate_tool_"+strconv.Itoa(index))
	}
	return toolNames
}

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
	candidateToolNames := numberedToolNames(likelyToolCountLimit + 5)
	probabilities := map[string]float64{}
	for index, toolName := range candidateToolNames {
		probabilities[toolName] = 0.99 - float64(index)/100000
	}

	selectedToolNames := selectLikelyToolNames(probabilities, candidateToolNames)

	if len(selectedToolNames) != likelyToolCountLimit {
		t.Fatalf("expected the cap to hold at %d, got %d", likelyToolCountLimit, len(selectedToolNames))
	}
	if selectedToolNames[0] != candidateToolNames[0] || selectedToolNames[likelyToolCountLimit-1] != candidateToolNames[likelyToolCountLimit-1] {
		t.Fatalf("expected the likeliest tools to survive the cap, got %v", selectedToolNames)
	}
}

func TestTheLikelyToolLimitIsTakenFromTheExposureCapRatherThanDeclaredBesideIt(t *testing.T) {
	if likelyToolCountLimit >= toolcontract.MaxExtensionCallableToolCount {
		t.Fatalf("expected the likely tools to fit under the exposure cap of %d, got a limit of %d", toolcontract.MaxExtensionCallableToolCount, likelyToolCountLimit)
	}
	if spareSlots := toolcontract.MaxExtensionCallableToolCount - likelyToolCountLimit; spareSlots != toolExposureGroupsRankedBelowTheLikelyTools {
		t.Fatalf("expected one exposure slot for each of the %d groups ranked below the pinned tools, got %d spare", toolExposureGroupsRankedBelowTheLikelyTools, spareSlots)
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
	candidateToolNames := resolveCallableToolNames(request)

	state := buildDecisionState(request, decisionToolDescriptions(request.ToolSet, candidateToolNames).tools)
	questions := toolQuestionsFor(request, candidateToolNames)

	for _, kernelToolName := range toolcontract.KernelToolNames() {
		if containsString(candidateToolNames, kernelToolName) {
			t.Fatalf("expected %s to be exposed without being asked about, got %v", kernelToolName, candidateToolNames)
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
	if !containsString(candidateToolNames, "task_add") || !containsString(candidateToolNames, "message_send") {
		t.Fatalf("expected the extension tools to stay candidates, got %v", candidateToolNames)
	}
}

func TestTheRequestIsIdenticalWhateverOrderTheToolsWereRegisteredIn(t *testing.T) {
	forwardRequest := addressedDecisionRequest("이번 주 회의 일정 정리해서 공유해줘")
	forwardRequest.ToolSet = newTestToolSet(measurementToolNames())
	reversedRequest := addressedDecisionRequest("이번 주 회의 일정 정리해서 공유해줘")
	reversedRequest.ToolSet = newTestToolSet(reversedToolNames(measurementToolNames()))

	forwardDocument := decisionRequestDocument(t, toolSelectionRequestFor(forwardRequest))
	reversedDocument := decisionRequestDocument(t, toolSelectionRequestFor(reversedRequest))

	if forwardDocument != reversedDocument {
		t.Fatal("expected registration order to leave the request unchanged; the order of the descriptions moves a mid-range probability by about 0.15")
	}
}

func reversedToolNames(toolNames []string) []string {
	reversedNames := make([]string, 0, len(toolNames))
	for index := len(toolNames) - 1; index >= 0; index-- {
		reversedNames = append(reversedNames, toolNames[index])
	}
	return reversedNames
}

func decisionRequestDocument(t *testing.T, request model.DecisionRequest) string {
	t.Helper()
	document, errorValue := json.Marshal(request)
	if errorValue != nil {
		t.Fatalf("expected the decision request to serialize: %v", errorValue)
	}
	return string(document)
}

func toolQuestionsFor(request agentcontract.IntakeDecisionRequest, toolNames []string) map[string]model.DecisionQuestion {
	return newQuestionBuilder(request).toolQuestions([]string{decisionMessageKey(0)}, toolNames)
}

func toolSelectionRequestFor(request agentcontract.IntakeDecisionRequest) model.DecisionRequest {
	candidateToolNames := resolveCallableToolNames(request)
	return toolSelectionRequestPart(request, []string{decisionMessageKey(0)}, decisionToolDescriptions(request.ToolSet, candidateToolNames).tools)
}

func TestThePerToolQuestionStaysSmallEnoughToRepeatPerMessage(t *testing.T) {
	request := addressedDecisionRequest("지난 분기 매출 정리해서 덱 만들어줘")
	request.ToolSet = newTestToolSet(measurementToolNames())
	candidateToolNames := resolveCallableToolNames(request)

	questionBytes := decisionQuestionsByteCount(toolQuestionsFor(request, candidateToolNames))

	if averageBytes := questionBytes / len(candidateToolNames); averageBytes > perToolQuestionByteBudget {
		t.Fatalf("expected a tool question to stay within %d bytes, got %d; shared guidance belongs in the state", perToolQuestionByteBudget, averageBytes)
	}
}

const perToolQuestionByteBudget = 220

func TestTheToolGuidanceIsCarriedOnceByTheStateRatherThanByEachQuestion(t *testing.T) {
	request := addressedDecisionRequest("지난 분기 매출 정리해서 덱 만들어줘")
	request.ToolSet = newTestToolSet([]string{"task_add", "task_list"})
	candidateToolNames := resolveCallableToolNames(request)

	state := buildDecisionState(request, decisionToolDescriptions(request.ToolSet, candidateToolNames).tools)
	if !strings.Contains(state.ToolGuidance, "at any point before the work is done") {
		t.Fatalf("expected the state to ask about the whole job, got %q", state.ToolGuidance)
	}
	if !strings.Contains(state.ToolGuidance, "Do not raise a tool because it exists") {
		t.Fatalf("expected the guidance to pair the must-check with a must-not-invent, got %q", state.ToolGuidance)
	}
	for questionName, question := range toolQuestionsFor(request, candidateToolNames) {
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

	if callLedger.Records[0].ToolSelection != nil {
		t.Fatalf("expected the routing call to carry no tool selection, got %+v", callLedger.Records[0].ToolSelection)
	}
	toolSelection := recordedToolSelection(callLedger)
	if toolSelection == nil {
		t.Fatal("expected the selection call to carry its own record")
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
	request := burstDecisionRequest(burstMessageCountThatOverflowsTheBudget, measurementToolNames())
	messageKeys := burstMessageKeys(request)
	candidateToolNames := resolveCallableToolNames(request)

	requests := planToolSelection(request, messageKeys, candidateToolNames).requests

	if len(requests) < 2 {
		t.Fatalf("expected a burst this size to be split, got %d request(s)", len(requests))
	}
	askedToolQuestions := map[string]bool{}
	for _, decisionRequest := range requests {
		if byteCount := decisionRequestByteCount(decisionRequest); byteCount > decisionRequestByteBudget {
			t.Fatalf("expected every part to fit the budget, got %d bytes", byteCount)
		}
		for questionName := range decisionRequest.Questions {
			if _, isToolQuestion := toolNameOfQuestion(questionName); !isToolQuestion {
				t.Fatalf("expected the selection call to ask about tools and nothing else, got %s", questionName)
			}
			if askedToolQuestions[questionName] {
				t.Fatalf("expected %s to be asked once, got it twice", questionName)
			}
			askedToolQuestions[questionName] = true
		}
	}
	if len(askedToolQuestions) != len(messageKeys)*len(candidateToolNames) {
		t.Fatalf("expected every tool question to survive the split, got %d", len(askedToolQuestions))
	}
}

func burstMessageKeys(request agentcontract.IntakeDecisionRequest) []string {
	messageKeys := []string{}
	for index := range request.Messages {
		messageKeys = append(messageKeys, decisionMessageKey(index))
	}
	return messageKeys
}

func TestASplitRequestAsksOnlyAboutTheToolsItsOwnStateDescribes(t *testing.T) {
	request := burstDecisionRequest(burstMessageCountThatOverflowsTheBudget, measurementToolNames())

	for _, decisionRequest := range planToolSelection(request, burstMessageKeys(request), resolveCallableToolNames(request)).requests {
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
	request := burstDecisionRequest(burstMessageCountThatOverflowsTheBudget, measurementToolNames())
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

func TestAFailedSelectionStartsTheTaskWithNoLikelyToolsAndSaysSoOnTheLedger(t *testing.T) {
	request := burstDecisionRequest(burstMessageCountThatOverflowsTheBudget, measurementToolNames())
	outcome := startTaskOutcome()
	outcome.ToolProbabilities = map[string]float64{"web_search": 0.88}
	callLedger := &agentcontract.IntakeCallLedger{}

	decisions, errorValue := NewDecisionPlanner(&partFailingDecisionModel{outcome: outcome}, nil, func() float64 { return 1 }).Decide(context.Background(), request, callLedger)

	if errorValue != nil {
		t.Fatalf("expected a failed selection to leave the task startable: %v", errorValue)
	}
	if len(decisions.Messages) != len(request.Messages) {
		t.Fatalf("expected every message to keep its routing decision, got %d", len(decisions.Messages))
	}
	for _, decision := range decisions.Messages {
		if len(decision.TurnFields.InitialToolNames) != 0 {
			t.Fatalf("expected no tool to be pre-exposed after a failed selection, got %v", decision.TurnFields.InitialToolNames)
		}
	}
	if !hasFailedSelectionRecord(callLedger) {
		t.Fatalf("expected the failed selection call to be recorded, got %+v", callLedger.Records)
	}
}

func hasFailedSelectionRecord(callLedger *agentcontract.IntakeCallLedger) bool {
	for _, record := range callLedger.Records {
		if record.IsError && record.DecidedMessageCount == 0 {
			return true
		}
	}
	return false
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

const burstMessageCountThatOverflowsTheBudget = 14

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

func recordedToolSelection(callLedger *agentcontract.IntakeCallLedger) *agentcontract.ToolSelectionRecord {
	for _, record := range callLedger.Records {
		if record.ToolSelection != nil {
			return record.ToolSelection
		}
	}
	return nil
}

func TestOnlyAMessageRoutedToWorkCostsAToolSelectionCall(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		route             agentcontract.TurnRoute
		classification    agentcontract.IntakeClassification
		expectedCallCount int
	}{
		{name: "start_task", route: agentcontract.TurnRouteStartTask, classification: agentcontract.IntakeClassificationBoundedTask, expectedCallCount: 2},
		{name: "continue_task", route: agentcontract.TurnRouteContinueTask, classification: agentcontract.IntakeClassificationBoundedTask, expectedCallCount: 2},
		{name: "an answer that needs a tool", route: agentcontract.TurnRouteAnswerQuestion, classification: agentcontract.IntakeClassificationBoundedTask, expectedCallCount: 2},
		{name: "answer_question", route: agentcontract.TurnRouteAnswerQuestion, classification: agentcontract.IntakeClassificationQuickReply, expectedCallCount: 1},
		{name: "clarify", route: agentcontract.TurnRouteClarify, classification: agentcontract.IntakeClassificationNeedsConfirmation, expectedCallCount: 1},
		{name: "consume", route: agentcontract.TurnRouteConsume, classification: agentcontract.IntakeClassificationQuickReply, expectedCallCount: 1},
		{name: "give_up", route: agentcontract.TurnRouteGiveUp, classification: agentcontract.IntakeClassificationUnsupported, expectedCallCount: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := addressedDecisionRequest("이번 주 회의 일정 정리해서 공유해줘")
			request.ToolSet = newTestToolSet([]string{"event_list", "message_send"})
			outcome := startTaskOutcome()
			outcome.TurnDecision.Route = testCase.route
			outcome.TurnDecision.Classification = testCase.classification
			outcome.TurnDecision.InitialToolNames = nil
			decisionModel := intaketest.NewDecisionModel(outcome)

			if _, errorValue := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }).Decide(context.Background(), request, nil); errorValue != nil {
				t.Fatalf("expected the decision to answer: %v", errorValue)
			}

			if len(decisionModel.Requests()) != testCase.expectedCallCount {
				t.Fatalf("expected %d decision call(s), got %d", testCase.expectedCallCount, len(decisionModel.Requests()))
			}
		})
	}
}
